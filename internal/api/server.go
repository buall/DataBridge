package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"databridge/internal/migrate"
	"databridge/internal/model"
	"databridge/internal/postgres"
	"databridge/internal/store"
)

type Server struct {
	store     *store.Store
	engine    *migrate.Engine
	geoClient *http.Client
	geoMu     sync.Mutex
	geoCache  map[string]string
	auth      authConfig
}

func New(appStore *store.Store) http.Handler {
	server := &Server{
		store:     appStore,
		engine:    migrate.New(),
		geoClient: &http.Client{Timeout: 2 * time.Second},
		geoCache:  make(map[string]string),
		auth:      loadAuthConfig(),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/healthz", server.health)
	mux.HandleFunc("GET /api/auth/session", server.authSession)
	mux.HandleFunc("POST /api/auth/login", server.login)
	mux.HandleFunc("POST /api/auth/logout", server.logout)
	protected := http.NewServeMux()
	protected.HandleFunc("GET /api/connections", server.listConnections)
	protected.HandleFunc("POST /api/connections/test", server.testConnection)
	protected.HandleFunc("POST /api/connections", server.createConnection)
	protected.HandleFunc("PUT /api/connections/{id}", server.updateConnection)
	protected.HandleFunc("POST /api/connections/{id}/refresh", server.refreshConnection)
	protected.HandleFunc("GET /api/connections/{id}/databases", server.listDatabases)
	protected.HandleFunc("POST /api/connections/{id}/databases", server.createDatabase)
	protected.HandleFunc("GET /api/connections/{id}/databases/{database}/tables", server.listTables)
	protected.HandleFunc("POST /api/migrations", server.createMigration)
	protected.HandleFunc("GET /api/migrations/{id}", server.getMigration)
	protected.HandleFunc("POST /api/migrations/{id}/cancel", server.cancelMigration)
	protected.HandleFunc("GET /api/migrations", server.listMigrations)
	mux.Handle("/api/", server.auth.authMiddleware(protected))
	mux.Handle("/", staticHandler())
	return recoverMiddleware(corsMiddleware(mux))
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) authSession(w http.ResponseWriter, r *http.Request) {
	authenticated := s.auth.enabled() && s.auth.validSession(r)
	if !s.auth.enabled() && s.auth.localOnlyBypass && isLocalRequest(r) {
		authenticated = true
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": authenticated,
		"username":      s.auth.username,
		"configured":    s.auth.enabled(),
	})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if !s.auth.checkPassword(strings.TrimSpace(input.Username), input.Password) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "用户名或密码不正确")
		return
	}
	s.auth.setSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "username": s.auth.username})
}

func (s *Server) logout(w http.ResponseWriter, _ *http.Request) {
	s.auth.clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) listConnections(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.Connections()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", err.Error())
		return
	}
	items = s.withConnectionVersions(r.Context(), items)
	items = s.withConnectionRegions(r.Context(), items)
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) withConnectionVersions(ctx context.Context, items []model.Connection) []model.Connection {
	for index := range items {
		if items[index].Version != "" {
			continue
		}
		versionCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		version, err := postgres.ServerVersion(versionCtx, items[index])
		cancel()
		if err != nil {
			log.Printf("connection[%s] version discovery failed: %v", items[index].ID, err)
			continue
		}
		items[index].Version = version
		if err := s.store.UpdateConnectionVersion(items[index].ID, version); err != nil {
			log.Printf("connection[%s] version persistence failed: %v", items[index].ID, err)
		}
	}
	return items
}

func (s *Server) withConnectionRegions(ctx context.Context, items []model.Connection) []model.Connection {
	for index := range items {
		items[index].Region = s.connectionRegion(ctx, items[index].Host)
	}
	return items
}

func (s *Server) testConnection(w http.ResponseWriter, r *http.Request) {
	var input model.ConnectionInput
	if !decodeJSON(w, r, &input) || !validateConnection(w, input) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	latency, version, err := postgres.Test(ctx, input)
	if err != nil {
		writeError(w, http.StatusBadGateway, "connection_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "latency": latency.Milliseconds(), "version": version})
}

func (s *Server) createConnection(w http.ResponseWriter, r *http.Request) {
	var input model.ConnectionInput
	if !decodeJSON(w, r, &input) || !validateConnection(w, input) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	latency, version, err := postgres.Test(ctx, input)
	if err != nil {
		writeError(w, http.StatusBadGateway, "connection_failed", err.Error())
		return
	}
	connection, err := s.store.AddConnection(input, latency, version)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", err.Error())
		return
	}
	connection.Region = s.connectionRegion(r.Context(), connection.Host)
	writeJSON(w, http.StatusCreated, connection)
}

func (s *Server) updateConnection(w http.ResponseWriter, r *http.Request) {
	current, err := s.store.Connection(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "connection not found")
		return
	}
	var input model.UpdateConnectionInput
	if !decodeJSON(w, r, &input) || !validateConnectionUpdate(w, input) {
		return
	}
	password := current.Password
	if strings.TrimSpace(input.Password) != "" {
		password = input.Password
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	latency, version, err := postgres.Test(ctx, model.ConnectionInput{
		Name:     input.Name,
		Note:     input.Note,
		Host:     input.Host,
		Port:     input.Port,
		Username: input.Username,
		Password: password,
		SSLMode:  input.SSLMode,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "connection_failed", err.Error())
		return
	}
	connection, err := s.store.UpdateConnection(current.ID, input, latency, version)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", err.Error())
		return
	}
	connection.Region = s.connectionRegion(r.Context(), connection.Host)
	writeJSON(w, http.StatusOK, connection)
}

func (s *Server) refreshConnection(w http.ResponseWriter, r *http.Request) {
	connection, err := s.store.Connection(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "connection not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	latency, version, err := postgres.Test(ctx, model.ConnectionInput{
		Name:     connection.Name,
		Host:     connection.Host,
		Port:     connection.Port,
		Username: connection.Username,
		Password: connection.Password,
		SSLMode:  connection.SSLMode,
	})
	if err != nil {
		_ = s.store.UpdateConnectionHealth(connection.ID, "warning", 0, connection.Version)
		writeError(w, http.StatusBadGateway, "connection_failed", err.Error())
		return
	}
	if err := s.store.UpdateConnectionHealth(connection.ID, "connected", latency, version); err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", err.Error())
		return
	}
	connection.Status = "connected"
	connection.LatencyMS = latency.Milliseconds()
	connection.Version = version
	connection.Region = s.connectionRegion(r.Context(), connection.Host)
	writeJSON(w, http.StatusOK, connection)
}

func (s *Server) connectionRegion(ctx context.Context, host string) string {
	key := strings.TrimSpace(strings.Trim(host, "[]"))
	if key == "" {
		return "未知"
	}
	s.geoMu.Lock()
	if value, ok := s.geoCache[key]; ok {
		s.geoMu.Unlock()
		return value
	}
	s.geoMu.Unlock()

	region := s.lookupRegion(ctx, key)

	s.geoMu.Lock()
	s.geoCache[key] = region
	s.geoMu.Unlock()
	return region
}

func (s *Server) lookupRegion(ctx context.Context, host string) string {
	ip, ok := parseIP(host)
	if !ok {
		resolved, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(resolved) == 0 {
			return "未知"
		}
		for _, item := range resolved {
			parsed, valid := parseIP(item.IP.String())
			if !valid {
				continue
			}
			if isLocalIP(parsed) {
				return "内网"
			}
			ip = parsed
			ok = true
			break
		}
		if !ok {
			return "未知"
		}
	}
	if isLocalIP(ip) {
		return "内网"
	}
	if !ip.Is4() {
		return "未知"
	}

	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(lookupCtx, http.MethodGet, "http://ip-api.com/json/"+ip.String()+"?fields=status,country,regionName,city", nil)
	if err != nil {
		return "未知"
	}
	response, err := s.geoClient.Do(request)
	if err != nil {
		return "未知"
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "未知"
	}
	var payload struct {
		Status     string `json:"status"`
		Country    string `json:"country"`
		RegionName string `json:"regionName"`
		City       string `json:"city"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return "未知"
	}
	if payload.Status != "success" {
		return "未知"
	}
	return formatRegionLabel(payload.Country, payload.RegionName, payload.City)
}

func parseIP(value string) (netip.Addr, bool) {
	addr, err := netip.ParseAddr(strings.TrimSpace(strings.Trim(value, "[]")))
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

func isLocalIP(addr netip.Addr) bool {
	return addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsUnspecified()
}

func formatRegionLabel(country, regionName, city string) string {
	country = strings.TrimSpace(country)
	regionName = strings.TrimSpace(regionName)
	city = strings.TrimSpace(city)
	if country == "" {
		return "未知"
	}
	countryCN := translateCountry(country)
	switch country {
	case "Hong Kong":
		return "香港"
	case "Taiwan":
		return "台湾"
	case "Macao":
		return "澳门"
	case "China":
		if city != "" && city != countryCN {
			return city
		}
		if regionName != "" && regionName != countryCN {
			return regionName
		}
		return countryCN
	default:
		return countryCN
	}
}

func translateCountry(country string) string {
	switch country {
	case "China":
		return "中国"
	case "Hong Kong":
		return "香港"
	case "Taiwan":
		return "台湾"
	case "Macao":
		return "澳门"
	case "United States":
		return "美国"
	case "Singapore":
		return "新加坡"
	case "Japan":
		return "日本"
	case "South Korea":
		return "韩国"
	case "Germany":
		return "德国"
	case "United Kingdom":
		return "英国"
	case "France":
		return "法国"
	case "Canada":
		return "加拿大"
	case "Australia":
		return "澳大利亚"
	case "Netherlands":
		return "荷兰"
	case "India":
		return "印度"
	case "Indonesia":
		return "印度尼西亚"
	case "Thailand":
		return "泰国"
	case "Malaysia":
		return "马来西亚"
	case "Vietnam":
		return "越南"
	default:
		return country
	}
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

func (s *Server) createDatabase(w http.ResponseWriter, r *http.Request) {
	connection, err := s.store.Connection(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "connection not found")
		return
	}
	var request model.CreateDatabaseRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	name := strings.TrimSpace(request.Name)
	if !validDatabaseName(name) {
		writeError(w, http.StatusBadRequest, "invalid_database", "database name must start with a letter or underscore and contain only letters, numbers, underscores or hyphens")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := postgres.CreateDatabase(ctx, connection, name); err != nil {
		writeError(w, http.StatusBadGateway, "database_create_failed", err.Error())
		return
	}
	items, err := postgres.Databases(ctx, connection)
	if err != nil {
		writeError(w, http.StatusBadGateway, "database_created_refresh_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, items)
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
	if request.Engine == "" {
		request.Engine = "copy"
	}
	if request.Engine != "copy" && request.Engine != "native" {
		writeError(w, http.StatusBadRequest, "invalid_request", "unsupported migration engine")
		return
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
		Events:  []model.Event{{Time: now, Level: "INFO", Message: "迁移任务已创建，迁移引擎：" + migrationEngineLabel(request.Engine)}},
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
	if request.Engine == "" {
		request.Engine = "copy"
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
			if progress.EventMessage != "" {
				item.Events = append(item.Events, model.Event{Time: time.Now(), Level: progress.EventLevel, Message: progress.EventMessage})
				if len(item.Events) > 100 {
					item.Events = item.Events[len(item.Events)-100:]
				}
			}
		})
	}
	var err error
	switch request.Engine {
	case "native":
		err = s.engine.RunNativePostgres(ctx, source, target, request, reporter)
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
		return "pg_dump / pg_restore"
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

func validateConnectionUpdate(w http.ResponseWriter, input model.UpdateConnectionInput) bool {
	if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Host) == "" || strings.TrimSpace(input.Username) == "" {
		writeError(w, http.StatusBadRequest, "invalid_connection", "name, host and username are required")
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

func validDatabaseName(name string) bool {
	if name == "" || len(name) > 63 {
		return false
	}
	for index, value := range name {
		letter := value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
		digit := value >= '0' && value <= '9'
		if index == 0 && !letter && value != '_' {
			return false
		}
		if !letter && !digit && value != '_' && value != '-' {
			return false
		}
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
	allowedOrigins := parseAllowedOrigins(os.Getenv("DATABRIDGE_CORS_ORIGIN"))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && allowedOrigins[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func parseAllowedOrigins(value string) map[string]bool {
	origins := make(map[string]bool)
	for _, origin := range strings.Split(value, ",") {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			origins[origin] = true
		}
	}
	return origins
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
