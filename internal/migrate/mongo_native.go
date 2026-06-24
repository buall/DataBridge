package migrate

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"databridge/internal/model"
	dbmongo "databridge/internal/mongo"
)

func (e *Engine) RunNativeMongo(
	ctx context.Context,
	source model.Connection,
	target model.Connection,
	request model.MigrationRequest,
	report Reporter,
) error {
	for _, name := range []string{"mongodump", "mongorestore"} {
		if _, err := mongoToolPath(name); err != nil {
			return err
		}
	}

	sourceCollections, err := dbmongo.CollectionNames(ctx, source, request.SourceDatabase)
	if err != nil {
		return fmt.Errorf("inspect source collections: %w", err)
	}
	selectedCollections := normalizeMongoCollections(request.Tables)
	if len(selectedCollections) == 0 {
		selectedCollections = sourceCollections
	}
	allowed := make(map[string]bool, len(sourceCollections))
	for _, collection := range sourceCollections {
		allowed[collection] = true
	}
	filteredCollections := make([]string, 0, len(selectedCollections))
	for _, collection := range selectedCollections {
		if allowed[collection] {
			filteredCollections = append(filteredCollections, collection)
		}
	}
	selectedCollections = filteredCollections
	if len(selectedCollections) == 0 {
		return fmt.Errorf("no source collections selected")
	}
	report(Progress{
		Stage:        "precheck",
		TablesDone:   0,
		RowsTotal:    int64(len(selectedCollections)),
		RowsCopied:   0,
		EventLevel:   "INFO",
		EventMessage: fmt.Sprintf("MongoDB 原生工具预检查完成，共选择 %d 个集合", len(selectedCollections)),
	})

	switch request.ConflictStrategy {
	case "backup", "overwrite":
		report(Progress{Stage: "target-prep", EventLevel: "INFO", EventMessage: "正在重建目标 MongoDB 数据库"})
		if err := dbmongo.DropDatabase(ctx, target, request.TargetDatabase); err != nil {
			return fmt.Errorf("drop target database: %w", err)
		}
	case "skip":
		targetCollections, err := dbmongo.CollectionNames(ctx, target, request.TargetDatabase)
		if err == nil && len(targetCollections) > 0 {
			return fmt.Errorf("target database %s exists and strategy is skip", request.TargetDatabase)
		}
	default:
		return fmt.Errorf("unsupported conflict strategy %q", request.ConflictStrategy)
	}

	tempDir, err := os.MkdirTemp("", "databridge-mongo-migrate-*")
	if err != nil {
		return fmt.Errorf("create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	report(Progress{Stage: "dump", EventLevel: "INFO", EventMessage: "正在使用 mongodump 导出源端数据库"})
	for _, collection := range selectedCollections {
		if err := runMongoTool(ctx, "mongodump", []string{
			"--host", source.Host,
			"--port", fmt.Sprintf("%d", source.Port),
			"--username", source.Username,
			"--password", source.Password,
			"--authenticationDatabase", "admin",
			"--db", request.SourceDatabase,
			"--collection", collection,
			"--out", tempDir,
		}, "dump", report); err != nil {
			return fmt.Errorf("mongodump: %w", err)
		}
	}

	report(Progress{Stage: "restore", EventLevel: "INFO", EventMessage: "正在使用 mongorestore 导入目标数据库"})
	restoreArgs := []string{
		"--host", target.Host,
		"--port", fmt.Sprintf("%d", target.Port),
		"--username", target.Username,
		"--password", target.Password,
		"--authenticationDatabase", "admin",
		"--nsFrom", request.SourceDatabase + ".*",
		"--nsTo", request.TargetDatabase + ".*",
		tempDir,
	}
	restoreArgs = append(restoreArgs, mongoRestoreIncludeArgs(request.SourceDatabase, selectedCollections)...)
	if err := runMongoTool(ctx, "mongorestore", restoreArgs, "restore", report); err != nil {
		return fmt.Errorf("mongorestore: %w", err)
	}

	targetCollections, err := dbmongo.CollectionNames(ctx, target, request.TargetDatabase)
	if err != nil {
		return fmt.Errorf("validate target collections: %w", err)
	}
	targetCollectionSet := make(map[string]bool, len(targetCollections))
	for _, collection := range targetCollections {
		targetCollectionSet[collection] = true
	}
	var copied int64
	for _, collection := range selectedCollections {
		if targetCollectionSet[collection] {
			copied++
		}
	}
	result := model.TableResult{
		Table:      fmt.Sprintf("%s (%d collections)", request.SourceDatabase, len(selectedCollections)),
		SourceRows: int64(len(selectedCollections)),
		TargetRows: copied,
		CopiedRows: copied,
		Valid:      copied == int64(len(selectedCollections)),
	}
	report(Progress{
		Stage:      "validation",
		TablesDone: 1,
		RowsTotal:  int64(len(selectedCollections)),
		RowsCopied: copied,
		Result:     &result,
	})
	report(Progress{
		Stage:        "completed",
		TablesDone:   1,
		RowsTotal:    int64(len(selectedCollections)),
		RowsCopied:   copied,
		EventLevel:   "INFO",
		EventMessage: fmt.Sprintf("MongoDB 集合迁移完成，已恢复 %d / %d 个集合", copied, len(selectedCollections)),
	})
	return nil
}

func normalizeMongoCollections(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		name := strings.TrimSpace(value)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		result = append(result, name)
	}
	return result
}

func mongoRestoreIncludeArgs(database string, collections []string) []string {
	if len(collections) == 0 {
		return nil
	}
	args := make([]string, 0, len(collections)*2)
	for _, collection := range collections {
		args = append(args, "--nsInclude", database+"."+collection)
	}
	return args
}
func runMongoTool(ctx context.Context, name string, baseArgs []string, stage string, report Reporter, extraArgs ...string) error {
	path, err := mongoToolPath(name)
	if err != nil {
		return err
	}
	args := append(append([]string{}, baseArgs...), extraArgs...)
	cmd := exec.CommandContext(ctx, path, args...)
	output, err := cmd.CombinedOutput()
	if text := strings.TrimSpace(string(output)); text != "" {
		report(Progress{Stage: stage, EventLevel: "INFO", EventMessage: text})
	}
	if err != nil {
		return err
	}
	report(Progress{Stage: stage, EventLevel: "INFO", EventMessage: name + " 执行完成"})
	return nil
}

func mongoToolPath(name string) (string, error) {
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	for _, candidate := range []string{
		filepath.Join("/opt/homebrew/bin", name),
		filepath.Join("/usr/local/bin", name),
	} {
		if stat, err := os.Stat(candidate); err == nil && !stat.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%s not found in PATH", name)
}
