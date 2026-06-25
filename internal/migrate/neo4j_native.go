package migrate

import (
	"context"
	"fmt"

	"databridge/internal/backup"
	"databridge/internal/model"
	dbneo4j "databridge/internal/neo4j"
)

func (e *Engine) RunNativeNeo4j(
	ctx context.Context,
	source model.Connection,
	target model.Connection,
	request model.MigrationRequest,
	report Reporter,
) error {
	sourceNodes, sourceRelationships, err := dbneo4j.Counts(ctx, source, request.SourceDatabase)
	if err != nil {
		return fmt.Errorf("inspect source graph: %w", err)
	}
	report(Progress{
		Stage:        "precheck",
		TablesDone:   0,
		RowsTotal:    sourceNodes + sourceRelationships,
		EventLevel:   "INFO",
		EventMessage: fmt.Sprintf("Neo4j 预检查完成，源端 %d 个节点、%d 条关系", sourceNodes, sourceRelationships),
	})

	switch request.ConflictStrategy {
	case "backup", "overwrite":
		report(Progress{Stage: "target-prep", RowsTotal: sourceNodes + sourceRelationships, EventLevel: "INFO", EventMessage: "Neo4j 将清空目标图数据后导入"})
	case "skip":
		targetNodes, targetRelationships, err := dbneo4j.Counts(ctx, target, request.TargetDatabase)
		if err != nil {
			return fmt.Errorf("inspect target graph: %w", err)
		}
		if targetNodes > 0 || targetRelationships > 0 {
			return fmt.Errorf("target neo4j database %s contains data and strategy is skip", request.TargetDatabase)
		}
	default:
		return fmt.Errorf("unsupported conflict strategy %q", request.ConflictStrategy)
	}

	backupEngine := backup.New()
	archive, cleanup, err := backupEngine.ExportLocal(ctx, source, request.SourceDatabase, backupReporter(report))
	if err != nil {
		return err
	}
	defer cleanup()

	restoreStrategy := "recreate"
	if request.ConflictStrategy == "skip" {
		restoreStrategy = "append"
	}
	if err := backupEngine.Restore(ctx, target, request.TargetDatabase, archive, backup.RestoreOptions{Strategy: restoreStrategy, SourceDatabase: request.SourceDatabase}, backupReporter(report)); err != nil {
		return err
	}

	targetNodes, targetRelationships, err := dbneo4j.Counts(ctx, target, request.TargetDatabase)
	if err != nil {
		return fmt.Errorf("validate target graph: %w", err)
	}
	sourceTotal := sourceNodes + sourceRelationships
	targetTotal := targetNodes + targetRelationships
	result := model.TableResult{
		Table:      fmt.Sprintf("%s graph", request.SourceDatabase),
		SourceRows: sourceTotal,
		TargetRows: targetTotal,
		CopiedRows: targetTotal,
		Valid:      sourceNodes == targetNodes && sourceRelationships == targetRelationships,
	}
	if !result.Valid {
		result.Error = fmt.Sprintf("源端 %d 节点/%d 关系，目标端 %d 节点/%d 关系", sourceNodes, sourceRelationships, targetNodes, targetRelationships)
	}
	report(Progress{
		Stage:      "validation",
		TablesDone: 1,
		RowsTotal:  sourceTotal,
		RowsCopied: targetTotal,
		Result:     &result,
	})
	report(Progress{
		Stage:        "completed",
		TablesDone:   1,
		RowsTotal:    sourceTotal,
		RowsCopied:   targetTotal,
		EventLevel:   "INFO",
		EventMessage: fmt.Sprintf("Neo4j 图迁移完成，目标端 %d 个节点、%d 条关系", targetNodes, targetRelationships),
	})
	return nil
}

func backupReporter(report Reporter) backup.Reporter {
	return func(progress backup.Progress) {
		stage := progress.Stage
		switch stage {
		case "export", "compress", "upload":
			stage = "dump"
		case "extract":
			stage = "restore"
		}
		report(Progress{
			Stage:        stage,
			EventLevel:   progress.EventLevel,
			EventMessage: progress.EventMessage,
		})
	}
}
