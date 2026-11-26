package api

import (
	"context"
	"databridge/internal/migrate"
	"databridge/internal/model"
	"databridge/internal/store"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"
)

func (s *Server) createMigration(w http.ResponseWriter, r *http.Request) {
	var request model.MigrationRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.ConflictStrategy == "" {
		request.ConflictStrategy = "backup"
	}
	source, err := s.store.Connection(request.SourceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_source", "未找到源端连接，请重新选择")
		return
	}
	target, err := s.store.Connection(request.TargetID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_target", "未找到目标端连接，请重新选择")
		return
	}
	if request.SourceID == "" || request.TargetID == "" || request.SourceDatabase == "" || request.TargetDatabase == "" || (len(request.Tables) == 0 && source.Type != "MongoDB" && source.Type != "Neo4j") {
		writeError(w, http.StatusBadRequest, "invalid_request", "请完整选择源端、目标端、数据库，并至少勾选一张表")
		return
	}
	if source.Type != target.Type {
		writeError(w, http.StatusBadRequest, "invalid_request", "源端和目标端必须是同一种数据库类型")
		return
	}
	if source.Type == "MongoDB" && len(request.Tables) == 0 {
		request.Tables = []string{request.SourceDatabase}
	}
	if source.Type == "Neo4j" && len(request.Tables) == 0 {
		request.Tables = []string{request.SourceDatabase}
	}
	if request.Engine == "" {
		if source.Type == "PostgreSQL" {
			request.Engine = "copy"
		} else {
			request.Engine = "native"
		}
	}
	if request.Engine != "copy" && request.Engine != "native" {
		writeError(w, http.StatusBadRequest, "invalid_request", "不支持当前迁移引擎，请重新选择")
		return
	}
	if request.Engine == "copy" && source.Type != "PostgreSQL" {
		writeError(w, http.StatusBadRequest, "invalid_request", "内置 COPY 引擎仅支持 PostgreSQL")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now()
	value := model.Migration{
		ID: store.NewID(), Name: request.Name, Status: model.StatusPending, Stage: "pending",
		TablesTotal: len(request.Tables), StartedAt: now, SourceName: source.Name,
		TargetName: target.Name, SourceDatabase: request.SourceDatabase,
		TargetDatabase: request.TargetDatabase, Request: request,
		Results: []model.TableResult{},
		Events:  []model.Event{{Time: now, Level: "INFO", Message: "迁移任务已创建，迁移引擎：" + migrationEngineLabel(request.Engine)}},
	}
	if value.Name == "" {
		value.Name = request.SourceDatabase + " → " + request.TargetDatabase
	}
	if err := s.store.AddMigration(value, cancel); err != nil {
		cancel()
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextCreateMigration, err))
		return
	}
	go s.runMigration(ctx, value.ID, source, target, request)
	writeJSON(w, http.StatusAccepted, value)
}

func (s *Server) acquireJobSlot(ctx context.Context) bool {
	if s.jobSem == nil {
		return true
	}
	select {
	case s.jobSem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *Server) releaseJobSlot() {
	if s.jobSem == nil {
		return
	}
	<-s.jobSem
}

func (s *Server) runMigration(ctx context.Context, id string, source, target model.Connection, request model.MigrationRequest) {
	defer s.store.ClearMigrationCancel(id)
	if !s.acquireJobSlot(ctx) {
		_ = s.store.UpdateMigration(id, func(item *model.Migration) {
			now := time.Now()
			item.Status = model.StatusCancelled
			item.Stage = "cancelled"
			item.FinishedAt = &now
			item.Error = "任务在排队等待执行时已取消"
			appendJobEvent(&item.Events, model.Event{Time: now, Level: "WARN", Message: item.Error})
		})
		return
	}
	defer s.releaseJobSlot()
	if request.Engine == "" {
		if source.Type == "PostgreSQL" {
			request.Engine = "copy"
		} else {
			request.Engine = "native"
		}
	}
	log.Printf("migration[%s] started: %s/%s -> %s/%s, tables=%d, engine=%s",
		id, source.Name, request.SourceDatabase, target.Name, request.TargetDatabase, len(request.Tables), request.Engine)
	_ = s.store.UpdateMigration(id, func(item *model.Migration) {
		item.Status = model.StatusRunning
		item.Stage = "precheck"
	})
	reporter := func(progress migrate.Progress) {
		if progress.EventMessage != "" {
			log.Printf("migration[%s] stage=%s table=%s rows=%d/%d: %s",
				id, progress.Stage, progress.CurrentTable, progress.RowsCopied, progress.RowsTotal, progress.EventMessage)
		}
		_ = s.store.UpdateMigration(id, func(item *model.Migration) {
			item.Stage = progress.Stage
			item.CurrentTable = progress.CurrentTable
			item.TablesDone = progress.TablesDone
			if progress.RowsTotal > 0 {
				item.RowsTotal = progress.RowsTotal
			}
			item.RowsCopied = progress.RowsCopied
			item.Progress = calculateProgress(progress, item.TablesTotal)
			if progress.Result != nil {
				upsertResult(item, *progress.Result)
			}
			appendJobEvent(&item.Events, model.Event{Time: time.Now(), Level: progress.EventLevel, Message: progress.EventMessage})
		})
	}
	var err error
	switch request.Engine {
	case "native":
		err = s.engine.RunNative(ctx, source, target, request, reporter)
	case "copy":
		err = s.engine.Run(ctx, source, target, request, reporter)
	default:
		err = fmt.Errorf("unsupported migration engine %q", request.Engine)
	}
	finished := time.Now()
	if err != nil {
		log.Printf("migration[%s] failed after %s: %v", id, finished.Sub(requestStartedAt(s.store, id)), err)
	} else {
		log.Printf("migration[%s] completed after %s", id, finished.Sub(requestStartedAt(s.store, id)))
	}
	_ = s.store.UpdateMigration(id, func(item *model.Migration) {
		item.FinishedAt = &finished
		if err == nil {
			item.Status = model.StatusCompleted
			item.Stage = "completed"
			item.Progress = 100
			return
		}
		if errors.Is(err, context.Canceled) {
			item.Status = model.StatusCancelled
			item.Stage = "cancelled"
			appendJobEvent(&item.Events, model.Event{Time: finished, Level: "WARN", Message: "任务已取消"})
		} else {
			item.Status = model.StatusFailed
			item.Stage = "failed"
			item.Error = userFacingErrorMessage(errorContextMigrationRuntime, err)
			appendJobEvent(&item.Events, model.Event{Time: finished, Level: "ERROR", Message: err.Error()})
		}
	})
}

func requestStartedAt(appStore *store.Store, id string) time.Time {
	item, err := appStore.Migration(id)
	if err != nil {
		return time.Now()
	}
	return item.StartedAt
}

func (s *Server) getMigration(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.Migration(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "migration not found")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) updateMigrationFavorite(w http.ResponseWriter, r *http.Request) {
	var input model.FavoriteInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.store.SetMigrationFavorite(r.PathValue("id"), input.Favorite)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "migration not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "storage_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, compactMigrationListJob(item))
}

func (s *Server) cancelMigration(w http.ResponseWriter, r *http.Request) {
	if err := s.store.CancelMigration(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "migration not found")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (s *Server) deleteMigration(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	item, err := s.store.Migration(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "migration not found")
		return
	}
	if item.Status == model.StatusRunning || item.Status == model.StatusPending {
		writeError(w, http.StatusConflict, "task_running", "任务正在执行，请先停止后再删除")
		return
	}
	if err := s.store.DeleteMigration(id); err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextDeleteConnection, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) listMigrations(w http.ResponseWriter, _ *http.Request) {
	items, err := s.store.MigrationsLimit(100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextCreateMigration, err))
		return
	}
	for index := range items {
		items[index] = compactMigrationListJob(items[index])
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) listLogs(w http.ResponseWriter, _ *http.Request) {
	items, err := s.store.MigrationsLimit(100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextCreateMigration, err))
		return
	}
	for index := range items {
		items[index] = compactMigrationLogJob(items[index])
	}
	auditLogs, err := s.store.AuditLogsLimit(200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextCreateMigration, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"migrations": items,
		"auditLogs":  auditLogs,
	})
}

func compactMigrationListJob(job model.Migration) model.Migration {
	job.Results = []model.TableResult{}
	job.Events = []model.Event{}
	return job
}

func compactMigrationLogJob(job model.Migration) model.Migration {
	job.Results = []model.TableResult{}
	const eventLimit = 20
	if len(job.Events) > eventLimit {
		job.Events = append([]model.Event(nil), job.Events[len(job.Events)-eventLimit:]...)
	}
	return job
}

func calculateProgress(progress migrate.Progress, tableTotal int) int {
	switch progress.Stage {
	case "precheck":
		return 5
	case "target-prep":
		return 10
	case "schema":
		return 15
	case "dump":
		return 30
	case "data":
		if progress.RowsTotal > 0 {
			return 15 + int(float64(progress.RowsCopied)/float64(progress.RowsTotal)*70)
		}
		if tableTotal > 0 {
			return 15 + progress.TablesDone*70/tableTotal
		}
		return 15
	case "restore":
		return 80
	case "constraints":
		return 88
	case "validation":
		return 95
	case "completed":
		return 100
	default:
		return 0
	}
}

func migrationEngineLabel(engine string) string {
	if engine == "native" {
		return "原生工具"
	}
	return "内置 COPY"
}

func upsertResult(item *model.Migration, result model.TableResult) {
	for index := range item.Results {
		if item.Results[index].Table == result.Table {
			item.Results[index] = result
			return
		}
	}
	item.Results = append(item.Results, result)
}
