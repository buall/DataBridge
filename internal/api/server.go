package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"databridge/internal/migrate"
	"databridge/internal/model"
	"databridge/internal/postgres"
	"databridge/internal/store"
)

type Server struct {
	store  *store.Store
	engine *migrate.Engine
}

func New(appStore *store.Store) http.Handler {
	server := &Server{store: appStore, engine: migrate.New()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/healthz", server.health)
	mux.HandleFunc("GET /api/connections", server.listConnections)
	mux.HandleFunc("POST /api/connections/test", server.testConnection)
	mux.HandleFunc("POST /api/connections", server.createConnection)
	mux.HandleFunc("GET /api/connections/{id}/databases", server.listDatabases)
	mux.HandleFunc("GET /api/connections/{id}/databases/{database}/tables", server.listTables)
	mux.HandleFunc("POST /api/migrations", server.createMigration)
	mux.HandleFunc("GET /api/migrations/{id}", server.getMigration)
	mux.HandleFunc("POST /api/migrations/{id}/cancel", server.cancelMigration)
	mux.HandleFunc("GET /api/migrations", server.listMigrations)
	mux.Handle("/", staticHandler())
	return recoverMiddleware(corsMiddleware(mux))
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) listConnections(w http.ResponseWriter, _ *http.Request) {
	items, err := s.store.Connections()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) testConnection(w http.ResponseWriter, r *http.Request) {
	var input model.ConnectionInput
	if !decodeJSON(w, r, &input) || !validateConnection(w, input) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	latency, err := postgres.Test(ctx, input)
	if err != nil {
		writeError(w, http.StatusBadGateway, "connection_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "latency": latency.Milliseconds()})
}

func (s *Server) createConnection(w http.ResponseWriter, r *http.Request) {
	var input model.ConnectionInput
	if !decodeJSON(w, r, &input) || !validateConnection(w, input) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	latency, err := postgres.Test(ctx, input)
	if err != nil {
		writeError(w, http.StatusBadGateway, "connection_failed", err.Error())
		return
	}
	connection, err := s.store.AddConnection(input, latency)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, connection)
}

func (s *Server) listDatabases(w http.ResponseWriter, r *http.Request) {
	connection, err := s.store.Connection(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "connection not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	items, err := postgres.Databases(ctx, connection)
	if err != nil {
		writeError(w, http.StatusBadGateway, "database_discovery_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) listTables(w http.ResponseWriter, r *http.Request) {
	connection, err := s.store.Connection(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "connection not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	items, err := postgres.Tables(ctx, connection, r.PathValue("database"))
	if err != nil {
		writeError(w, http.StatusBadGateway, "table_discovery_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) createMigration(w http.ResponseWriter, r *http.Request) {
	var request model.MigrationRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.SourceID == "" || request.TargetID == "" || request.SourceDatabase == "" || request.TargetDatabase == "" || len(request.Tables) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "source, target, databases and at least one table are required")
		return
	}
	if request.ConflictStrategy == "" {
		request.ConflictStrategy = "backup"
	}
	source, err := s.store.Connection(request.SourceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_source", "source connection not found")
		return
	}
	target, err := s.store.Connection(request.TargetID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_target", "target connection not found")
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
		Events:  []model.Event{{Time: now, Level: "INFO", Message: "迁移任务已创建"}},
	}
	if value.Name == "" {
		value.Name = request.SourceDatabase + " → " + request.TargetDatabase
	}
	if err := s.store.AddMigration(value, cancel); err != nil {
		cancel()
		writeError(w, http.StatusInternalServerError, "storage_error", err.Error())
		return
	}
	go s.runMigration(ctx, value.ID, source, target, request)
	writeJSON(w, http.StatusAccepted, value)
}

func (s *Server) runMigration(ctx context.Context, id string, source, target model.Connection, request model.MigrationRequest) {
	log.Printf("migration[%s] started: %s/%s -> %s/%s, tables=%d",
		id, source.Name, request.SourceDatabase, target.Name, request.TargetDatabase, len(request.Tables))
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
			if progress.EventMessage != "" {
				item.Events = append(item.Events, model.Event{Time: time.Now(), Level: progress.EventLevel, Message: progress.EventMessage})
				if len(item.Events) > 100 {
					item.Events = item.Events[len(item.Events)-100:]
				}
			}
		})
	}
	err := s.engine.Run(ctx, source, target, request, reporter)
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
			item.Events = append(item.Events, model.Event{Time: finished, Level: "WARN", Message: "任务已取消"})
		} else {
			item.Status = model.StatusFailed
			item.Stage = "failed"
			item.Error = err.Error()
			item.Events = append(item.Events, model.Event{Time: finished, Level: "ERROR", Message: err.Error()})
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

func (s *Server) cancelMigration(w http.ResponseWriter, r *http.Request) {
	if err := s.store.CancelMigration(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "migration not found")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (s *Server) listMigrations(w http.ResponseWriter, _ *http.Request) {
	items, err := s.store.Migrations()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func calculateProgress(progress migrate.Progress, tableTotal int) int {
	switch progress.Stage {
	case "precheck":
		return 5
	case "schema":
		return 15
	case "data":
		if progress.RowsTotal > 0 {
			return 15 + int(float64(progress.RowsCopied)/float64(progress.RowsTotal)*70)
		}
		if tableTotal > 0 {
			return 15 + progress.TablesDone*70/tableTotal
		}
		return 15
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

func upsertResult(item *model.Migration, result model.TableResult) {
	for index := range item.Results {
		if item.Results[index].Table == result.Table {
			item.Results[index] = result
			return
		}
	}
	item.Results = append(item.Results, result)
}

func validateConnection(w http.ResponseWriter, input model.ConnectionInput) bool {
	if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Host) == "" || strings.TrimSpace(input.Username) == "" || input.Password == "" {
		writeError(w, http.StatusBadRequest, "invalid_connection", "name, host, username and password are required")
		return false
	}
	if input.Port == 0 {
		input.Port = 5432
	}
	if input.Port < 1 || input.Port > 65535 {
		writeError(w, http.StatusBadRequest, "invalid_connection", "port must be between 1 and 65535")
		return false
	}
	return true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("encode response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "message": message})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("panic serving %s: %v", r.URL.Path, recovered)
				writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func staticHandler() http.Handler {
	dist := http.Dir("dist")
	fileServer := http.FileServer(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		path := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if path == "." {
			path = "index.html"
		}
		file, err := dist.Open(path)
		if err == nil {
			info, statErr := file.Stat()
			_ = file.Close()
			if statErr == nil && !info.IsDir() {
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		if _, err := os.Stat("dist/index.html"); err == nil {
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
			return
		}
		if errors.Is(err, fs.ErrNotExist) {
			http.Error(w, "frontend not built; run npm run build", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, fmt.Sprintf("frontend unavailable (%s)", strconv.Itoa(http.StatusServiceUnavailable)), http.StatusServiceUnavailable)
	})
}
