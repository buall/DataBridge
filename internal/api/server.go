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
	"databridge/internal/mysql"
	"databridge/internal/postgres"
	"databridge/internal/store"
)

type errorContext string

const (
	errorContextConnectionTest      errorContext = "connection_test"
	errorContextDatabaseDiscovery   errorContext = "database_discovery"
	errorContextDatabaseCreate      errorContext = "database_create"
	errorContextTableDiscovery      errorContext = "table_discovery"
	errorContextDeleteConnection    errorContext = "delete_connection"
	errorContextCreateMigration     errorContext = "create_migration"
	errorContextMigrationRuntime    errorContext = "migration_runtime"
	errorContextInvalidJSON         errorContext = "invalid_json"
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
	protected.HandleFunc("DELETE /api/connections/{id}", server.deleteConnection)
	protected.HandleFunc("POST /api/connections/{id}/refresh", server.refreshConnection)
	protected.HandleFunc("GET /api/connections/{id}/databases", server.listDatabases)
	protected.HandleFunc("POST /api/connections/{id}/databases", server.createDatabase)
	protected.HandleFunc("POST /api/connections/{id}/seed", server.seedConnectionData)
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
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "登录失败，请检查用户名和密码")
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
		version, err := serverVersion(versionCtx, items[index])
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
	input.Type = normalizeDatabaseType(input.Type)
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	latency, version, err := testConnection(ctx, input)
	if err != nil {
		writeError(w, http.StatusBadGateway, "connection_failed", userFacingErrorMessage(errorContextConnectionTest, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "latency": latency.Milliseconds(), "version": version})
}

func (s *Server) createConnection(w http.ResponseWriter, r *http.Request) {
	var input model.ConnectionInput
	if !decodeJSON(w, r, &input) || !validateConnection(w, input) {
		return
	}
	input.Type = normalizeDatabaseType(input.Type)
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	latency, version, err := testConnection(ctx, input)
	if err != nil {
		writeError(w, http.StatusBadGateway, "connection_failed", userFacingErrorMessage(errorContextConnectionTest, err))
		return
	}
	connection, err := s.store.AddConnection(input, latency, version)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextConnectionTest, err))
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
	latency, version, err := testConnection(ctx, model.ConnectionInput{
		Type:     current.Type,
		Name:     input.Name,
		Note:     input.Note,
		Host:     input.Host,
		Port:     input.Port,
		Username: input.Username,
		Password: password,
		SSLMode:  input.SSLMode,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "connection_failed", userFacingErrorMessage(errorContextConnectionTest, err))
		return
	}
	connection, err := s.store.UpdateConnection(current.ID, input, latency, version)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextConnectionTest, err))
		return
	}
	connection.Region = s.connectionRegion(r.Context(), connection.Host)
	writeJSON(w, http.StatusOK, connection)
}

func (s *Server) deleteConnection(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteConnection(r.PathValue("id")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "connection not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextDeleteConnection, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) refreshConnection(w http.ResponseWriter, r *http.Request) {
	connection, err := s.store.Connection(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "connection not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	latency, version, err := testConnection(ctx, model.ConnectionInput{
		Type:     connection.Type,
		Name:     connection.Name,
		Host:     connection.Host,
		Port:     connection.Port,
		Username: connection.Username,
		Password: connection.Password,
		SSLMode:  connection.SSLMode,
	})
	if err != nil {
		_ = s.store.UpdateConnectionHealth(connection.ID, "warning", 0, connection.Version)
		writeError(w, http.StatusBadGateway, "connection_failed", userFacingErrorMessage(errorContextConnectionTest, err))
		return
	}
	if err := s.store.UpdateConnectionHealth(connection.ID, "connected", latency, version); err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextConnectionTest, err))
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
	items, err := databases(ctx, connection)
	if err != nil {
		writeError(w, http.StatusBadGateway, "database_discovery_failed", userFacingErrorMessage(errorContextDatabaseDiscovery, err))
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
		writeError(w, http.StatusBadRequest, "invalid_database", "数据库名格式不正确，请使用字母或下划线开头，只包含字母、数字、下划线或短横线")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := createDatabase(ctx, connection, name); err != nil {
		writeError(w, http.StatusBadGateway, "database_create_failed", userFacingErrorMessage(errorContextDatabaseCreate, err))
		return
	}
	items, err := databases(ctx, connection)
	if err != nil {
		writeError(w, http.StatusBadGateway, "database_created_refresh_failed", userFacingErrorMessage(errorContextDatabaseDiscovery, err))
		return
	}
	writeJSON(w, http.StatusCreated, items)
}

func (s *Server) seedConnectionData(w http.ResponseWriter, r *http.Request) {
	connection, err := s.store.Connection(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "connection not found")
		return
	}
	var request model.SeedDataRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	request.Database = strings.TrimSpace(request.Database)
	if request.Database == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "请选择要写入数据的数据库")
		return
	}
	request.Mode = strings.ToLower(strings.TrimSpace(request.Mode))
	if request.Mode == "" {
		request.Mode = "append"
	}
	if request.Mode != "append" && request.Mode != "replace" {
		writeError(w, http.StatusBadRequest, "invalid_request", "请选择正确的写入方式")
		return
	}
	if request.Rows <= 0 {
		request.Rows = 100
	}
	if request.Rows > 50000 {
		writeError(w, http.StatusBadRequest, "invalid_request", "单次最多写入 50000 条假数据")
		return
	}
	request.Template = strings.ToLower(strings.TrimSpace(request.Template))
	if request.Template == "" {
		request.Template = "users"
	}
	if request.Template != "users" && request.Template != "orders" && request.Template != "products" {
		writeError(w, http.StatusBadRequest, "invalid_request", "请选择正确的写入模板")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), seedTimeoutForRows(request.Rows))
	defer cancel()
	result, err := seedData(ctx, connection, request.Database, request.Rows, request.Mode, request.Template)
	if err != nil {
		log.Printf("seed connection[%s] type=%s database=%s template=%s mode=%s rows=%d failed: %v",
			connection.ID, connection.Type, request.Database, request.Template, request.Mode, request.Rows, err)
		_ = s.store.AddAuditLog(store.AuditLog{
			Level:          "ERROR",
			Action:         "seed_data",
			ResourceType:   "connection",
			ResourceID:     connection.ID,
			ResourceName:   connection.Name,
			ConnectionName: connection.Name,
			Database:       request.Database,
			Status:         "failed",
			Message:        "写入假数据失败",
			Detail:         fmt.Sprintf("type=%s template=%s mode=%s rows=%d error=%v", connection.Type, request.Template, request.Mode, request.Rows, err),
		})
		writeError(w, http.StatusBadGateway, "seed_failed", "写入假数据失败，请确认数据库可访问且账号具备建表和写入权限")
		return
	}
	log.Printf("seed connection[%s] type=%s database=%s table=%s template=%s mode=%s rows=%d success",
		connection.ID, connection.Type, result.Database, result.Table, result.Template, result.Mode, result.Rows)
	_ = s.store.AddAuditLog(store.AuditLog{
		Level:          "INFO",
		Action:         "seed_data",
		ResourceType:   "connection",
		ResourceID:     connection.ID,
		ResourceName:   connection.Name,
		ConnectionName: connection.Name,
		Database:       result.Database,
		Status:         "success",
		Message:        "写入假数据完成",
		Detail:         fmt.Sprintf("type=%s table=%s template=%s mode=%s rows=%d", connection.Type, result.Table, result.Template, result.Mode, result.Rows),
	})
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) listTables(w http.ResponseWriter, r *http.Request) {
	connection, err := s.store.Connection(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "connection not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	items, err := tables(ctx, connection, r.PathValue("database"))
	if err != nil {
		writeError(w, http.StatusBadGateway, "table_discovery_failed", userFacingErrorMessage(errorContextTableDiscovery, err))
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
		writeError(w, http.StatusBadRequest, "invalid_request", "请完整选择源端、目标端、数据库，并至少勾选一张表")
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
	if source.Type != target.Type {
		writeError(w, http.StatusBadRequest, "invalid_request", "源端和目标端必须是同一种数据库类型")
		return
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

func (s *Server) runMigration(ctx context.Context, id string, source, target model.Connection, request model.MigrationRequest) {
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
			item.Events = append(item.Events, model.Event{Time: finished, Level: "WARN", Message: "任务已取消"})
		} else {
			item.Status = model.StatusFailed
			item.Stage = "failed"
			item.Error = userFacingErrorMessage(errorContextMigrationRuntime, err)
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
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextCreateMigration, err))
		return
	}
	auditLogs, err := s.store.AuditLogs()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextCreateMigration, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"migrations": items,
		"auditLogs":  auditLogs,
	})
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

func validateConnection(w http.ResponseWriter, input model.ConnectionInput) bool {
	input.Type = normalizeDatabaseType(input.Type)
	if input.Type == "" {
		writeError(w, http.StatusBadRequest, "invalid_connection", "请选择数据库类型")
		return false
	}
	if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Host) == "" || strings.TrimSpace(input.Username) == "" || input.Password == "" {
		writeError(w, http.StatusBadRequest, "invalid_connection", "连接名称、主机地址、用户名和密码不能为空")
		return false
	}
	if input.Port == 0 {
		input.Port = defaultPort(input.Type)
	}
	if input.Port < 1 || input.Port > 65535 {
		writeError(w, http.StatusBadRequest, "invalid_connection", "端口号必须在 1 到 65535 之间")
		return false
	}
	return true
}

func validateConnectionUpdate(w http.ResponseWriter, input model.UpdateConnectionInput) bool {
	if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Host) == "" || strings.TrimSpace(input.Username) == "" {
		writeError(w, http.StatusBadRequest, "invalid_connection", "连接名称、主机地址和用户名不能为空")
		return false
	}
	if input.Port == 0 {
		input.Port = 1
	}
	if input.Port < 1 || input.Port > 65535 {
		writeError(w, http.StatusBadRequest, "invalid_connection", "端口号必须在 1 到 65535 之间")
		return false
	}
	return true
}

func normalizeDatabaseType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "postgresql", "postgres", "pg":
		return "PostgreSQL"
	case "mysql":
		return "MySQL"
	default:
		return ""
	}
}

func defaultPort(databaseType string) int {
	if databaseType == "MySQL" {
		return 3306
	}
	return 5432
}

func testConnection(ctx context.Context, input model.ConnectionInput) (time.Duration, string, error) {
	switch input.Type {
	case "PostgreSQL":
		return postgres.Test(ctx, input)
	case "MySQL":
		return mysql.Test(ctx, input)
	default:
		return 0, "", fmt.Errorf("unsupported database type %q", input.Type)
	}
}

func serverVersion(ctx context.Context, connection model.Connection) (string, error) {
	switch connection.Type {
	case "PostgreSQL":
		return postgres.ServerVersion(ctx, connection)
	case "MySQL":
		return mysql.ServerVersion(ctx, connection)
	default:
		return "", fmt.Errorf("unsupported database type %q", connection.Type)
	}
}

func databases(ctx context.Context, connection model.Connection) ([]model.DatabaseInfo, error) {
	switch connection.Type {
	case "PostgreSQL":
		return postgres.Databases(ctx, connection)
	case "MySQL":
		return mysql.Databases(ctx, connection)
	default:
		return nil, fmt.Errorf("unsupported database type %q", connection.Type)
	}
}

func createDatabase(ctx context.Context, connection model.Connection, name string) error {
	switch connection.Type {
	case "PostgreSQL":
		return postgres.CreateDatabase(ctx, connection, name)
	case "MySQL":
		return mysql.CreateDatabase(ctx, connection, name)
	default:
		return fmt.Errorf("unsupported database type %q", connection.Type)
	}
}

func tables(ctx context.Context, connection model.Connection, database string) ([]model.TableInfo, error) {
	switch connection.Type {
	case "PostgreSQL":
		return postgres.Tables(ctx, connection, database)
	case "MySQL":
		return mysql.Tables(ctx, connection, database)
	default:
		return nil, fmt.Errorf("unsupported database type %q", connection.Type)
	}
}

func seedData(ctx context.Context, connection model.Connection, database string, rows int, mode, template string) (model.SeedDataResult, error) {
	switch connection.Type {
	case "PostgreSQL":
		return postgres.SeedData(ctx, connection, database, rows, mode, template)
	case "MySQL":
		return mysql.SeedData(ctx, connection, database, rows, mode, template)
	default:
		return model.SeedDataResult{}, fmt.Errorf("unsupported database type %q", connection.Type)
	}
}

func seedTimeoutForRows(rows int) time.Duration {
	switch {
	case rows <= 1000:
		return 45 * time.Second
	case rows <= 10000:
		return 2 * time.Minute
	case rows <= 30000:
		return 4 * time.Minute
	default:
		return 6 * time.Minute
	}
}

func userFacingErrorMessage(context errorContext, err error) string {
	if err == nil {
		return ""
	}
	raw := strings.TrimSpace(err.Error())
	lower := strings.ToLower(raw)

	switch {
	case strings.Contains(lower, "connection refused"):
		return "无法连接到数据库主机，请确认主机地址、端口和网络访问策略是否正确"
	case strings.Contains(lower, "i/o timeout"), strings.Contains(lower, "deadline exceeded"), strings.Contains(lower, "timeout"):
		return "连接数据库超时，请确认网络连通性、防火墙规则和数据库服务状态"
	case strings.Contains(lower, "no such host"), strings.Contains(lower, "lookup "):
		return "无法解析数据库主机地址，请确认填写的主机名或 IP 是否正确"
	case strings.Contains(lower, "access denied"), strings.Contains(lower, "password authentication failed"), strings.Contains(lower, "using password: yes"):
		return "数据库认证失败，请确认用户名、密码和授权范围是否正确"
	case strings.Contains(lower, "unknown database"), strings.Contains(lower, "database does not exist"):
		return "指定的数据库不存在，请先确认数据库名称是否正确"
	case strings.Contains(lower, "table ") && strings.Contains(lower, "not found"):
		if context == errorContextMigrationRuntime {
			return "迁移任务中的部分数据表不存在，可能是源端对象已变化，请重新选择表后再试"
		}
	case strings.Contains(lower, "already exists"), strings.Contains(lower, "duplicate database"):
		if context == errorContextDatabaseCreate {
			return "数据库已存在，请换一个名称或直接使用已有数据库"
		}
	case strings.Contains(lower, "target table"), strings.Contains(lower, "strategy is skip"):
		if context == errorContextMigrationRuntime {
			return "目标端存在同名表，当前冲突策略不允许继续迁移，请调整为备份或覆盖后重试"
		}
		return "目标端存在同名表，当前冲突策略不允许覆盖，请调整冲突策略后重试"
	case strings.Contains(lower, "mysqldump not found"), strings.Contains(lower, "mysql not found"), strings.Contains(lower, "pg_dump not found"), strings.Contains(lower, "pg_restore not found"), strings.Contains(lower, "psql not found"):
		if context == errorContextMigrationRuntime {
			return "迁移依赖的数据库客户端工具未安装或不在 PATH 中，请先检查运行环境"
		}
		return "数据库客户端工具未安装或不在 PATH 中，请先检查运行环境"
	case strings.Contains(lower, "mysqldump:"):
		return "源端导出失败，请确认源数据库可访问、账号具备导出权限，且客户端工具版本兼容"
	case strings.Contains(lower, "mysql restore:"):
		return "目标端导入失败，请确认目标数据库可访问、账号具备建表和写入权限，并检查目标端是否存在同名对象冲突"
	case strings.Contains(lower, "permission denied"), strings.Contains(lower, "command denied"), strings.Contains(lower, "must be owner"), strings.Contains(lower, "insufficient privilege"), strings.Contains(lower, "permission"), strings.Contains(lower, "denied to user"):
		if context == errorContextMigrationRuntime {
			return "迁移执行失败，当前数据库账号权限不足，请确认具备导出、建表、写入或对象管理权限"
		}
		return "当前数据库账号权限不足，请确认具备所需操作权限"
	case strings.Contains(lower, "validate "):
		return "迁移已完成导入，但逐表校验失败，请检查目标端表是否可读，以及迁移过程中是否有额外写入"
	case strings.Contains(lower, "invalid json"), strings.Contains(lower, "unknown field"), strings.Contains(lower, "cannot unmarshal"):
		return "请求参数格式不正确，请刷新页面后重试"
	}

	switch context {
	case errorContextConnectionTest:
		return "连接测试失败，请确认主机、端口、账号、密码和 SSL 配置是否正确"
	case errorContextDatabaseDiscovery:
		return "读取数据库列表失败，请确认账号权限和数据库服务状态"
	case errorContextDatabaseCreate:
		return "创建数据库失败，请确认数据库名称、账号权限和目标实例状态"
	case errorContextTableDiscovery:
		return "读取数据表列表失败，请确认数据库是否可访问，以及账号是否具备元数据读取权限"
	case errorContextDeleteConnection:
		return "删除连接失败，请稍后重试"
	case errorContextCreateMigration:
		return "创建迁移任务失败，请确认源端、目标端、数据库和表选择是否正确"
	case errorContextMigrationRuntime:
		return "迁移执行失败，请查看任务日志并确认数据库权限、对象冲突和客户端工具状态"
	case errorContextInvalidJSON:
		return "请求参数格式不正确，请刷新页面后重试"
	default:
		return raw
	}
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
		writeError(w, http.StatusBadRequest, "invalid_json", userFacingErrorMessage(errorContextInvalidJSON, err))
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
