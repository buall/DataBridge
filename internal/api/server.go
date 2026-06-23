package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/smtp"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"databridge/internal/backup"
	"databridge/internal/migrate"
	"databridge/internal/model"
	"databridge/internal/mysql"
	"databridge/internal/postgres"
	"databridge/internal/remotestorage"
	"databridge/internal/store"
)

type errorContext string

const (
	errorContextConnectionTest    errorContext = "connection_test"
	errorContextDatabaseDiscovery errorContext = "database_discovery"
	errorContextDatabaseCreate    errorContext = "database_create"
	errorContextTableDiscovery    errorContext = "table_discovery"
	errorContextDeleteConnection  errorContext = "delete_connection"
	errorContextRemoteStorage     errorContext = "remote_storage"
	errorContextCreateMigration   errorContext = "create_migration"
	errorContextMigrationRuntime  errorContext = "migration_runtime"
	errorContextCreateBackup      errorContext = "create_backup"
	errorContextBackupRuntime     errorContext = "backup_runtime"
	errorContextInvalidJSON       errorContext = "invalid_json"
)

type Server struct {
	store     *store.Store
	engine    *migrate.Engine
	backup    *backup.Engine
	geoClient *http.Client
	geoMu     sync.Mutex
	geoCache  map[string]string
	auth      authConfig
}

var appLocation = mustLoadLocation("Asia/Shanghai")

func mustLoadLocation(name string) *time.Location {
	location, err := time.LoadLocation(name)
	if err != nil {
		log.Printf("load timezone %s failed: %v", name, err)
		return time.UTC
	}
	return location
}

func New(appStore *store.Store) http.Handler {
	server := &Server{
		store:     appStore,
		engine:    migrate.New(),
		backup:    backup.New(),
		geoClient: &http.Client{Timeout: 2 * time.Second},
		geoCache:  make(map[string]string),
		auth:      loadAuthConfig(),
	}
	server.reconcileScheduledBackupRuns()
	server.startBackupScheduler()
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
	protected.HandleFunc("GET /api/storages", server.listStorages)
	protected.HandleFunc("POST /api/storages", server.createStorage)
	protected.HandleFunc("POST /api/storages/object/buckets", server.listObjectBuckets)
	protected.HandleFunc("POST /api/storages/{id}/buckets", server.listStoredObjectBuckets)
	protected.HandleFunc("POST /api/storages/{id}/buckets/create", server.createStoredObjectBucket)
	protected.HandleFunc("PUT /api/storages/{id}", server.updateStorage)
	protected.HandleFunc("DELETE /api/storages/{id}", server.deleteStorage)
	protected.HandleFunc("POST /api/storages/{id}/test", server.testStorage)
	protected.HandleFunc("POST /api/migrations", server.createMigration)
	protected.HandleFunc("GET /api/migrations/{id}", server.getMigration)
	protected.HandleFunc("POST /api/migrations/{id}/cancel", server.cancelMigration)
	protected.HandleFunc("DELETE /api/migrations/{id}", server.deleteMigration)
	protected.HandleFunc("GET /api/migrations", server.listMigrations)
	protected.HandleFunc("GET /api/settings/smtp", server.getSMTPSettings)
	protected.HandleFunc("PUT /api/settings/smtp", server.updateSMTPSettings)
	protected.HandleFunc("POST /api/backups", server.createBackup)
	protected.HandleFunc("POST /api/backups/notification-test", server.testBackupNotification)
	protected.HandleFunc("GET /api/backups/{id}", server.getBackup)
	protected.HandleFunc("PUT /api/backups/{id}", server.updateBackup)
	protected.HandleFunc("POST /api/backups/{id}/trigger", server.triggerBackup)
	protected.HandleFunc("POST /api/backups/{id}/restore/precheck", server.restoreBackupPrecheck)
	protected.HandleFunc("POST /api/backups/{id}/restore", server.restoreBackup)
	protected.HandleFunc("POST /api/backups/{id}/cancel", server.cancelBackup)
	protected.HandleFunc("GET /api/backups/{id}/artifacts/{database}/download", server.downloadBackupArtifact)
	protected.HandleFunc("DELETE /api/backups/{id}/artifacts/{database}", server.deleteBackupArtifact)
	protected.HandleFunc("DELETE /api/backups/{id}", server.deleteBackup)
	protected.HandleFunc("GET /api/backups", server.listBackups)
	mux.Handle("/api/", server.auth.authMiddleware(protected))
	mux.Handle("/", staticHandler())
	return recoverMiddleware(server.loggingMiddleware(corsMiddleware(mux)))
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

func (s *Server) listStorages(w http.ResponseWriter, _ *http.Request) {
	items, err := s.store.Storages()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextRemoteStorage, err))
		return
	}
	writeJSON(w, http.StatusOK, redactStorageSecrets(items))
}

func (s *Server) createStorage(w http.ResponseWriter, r *http.Request) {
	var input model.StorageInput
	if !decodeJSON(w, r, &input) || !validateStorage(w, input) {
		return
	}
	item, err := s.store.AddStorage(input)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextRemoteStorage, err))
		return
	}
	writeJSON(w, http.StatusCreated, redactStorageSecret(item))
}

func (s *Server) listObjectBuckets(w http.ResponseWriter, r *http.Request) {
	var input model.StorageInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if normalizeStorageType(input.Type) != "object" {
		writeError(w, http.StatusBadRequest, "invalid_storage", "只有对象存储支持获取 Bucket 列表")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	buckets, err := remotestorage.ListObjectBuckets(ctx, input)
	if err != nil {
		writeError(w, http.StatusBadGateway, "bucket_discovery_failed", userFacingErrorMessage(errorContextRemoteStorage, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"buckets": buckets})
}

func (s *Server) listStoredObjectBuckets(w http.ResponseWriter, r *http.Request) {
	current, err := s.store.Storage(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "storage not found")
		return
	}
	var input model.StorageInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if normalizeStorageType(nonEmpty(input.Type, current.Type)) != "object" {
		writeError(w, http.StatusBadRequest, "invalid_storage", "只有对象存储支持获取 Bucket 列表")
		return
	}
	input.Type = "object"
	input.Endpoint = nonEmpty(input.Endpoint, current.Endpoint)
	input.Region = nonEmpty(input.Region, current.Region)
	input.AccessKey = nonEmpty(input.AccessKey, current.AccessKey)
	input.SecretKey = nonEmpty(input.SecretKey, current.SecretKey)
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	buckets, err := remotestorage.ListObjectBuckets(ctx, input)
	if err != nil {
		writeError(w, http.StatusBadGateway, "bucket_discovery_failed", userFacingErrorMessage(errorContextRemoteStorage, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"buckets": buckets})
}

func (s *Server) createStoredObjectBucket(w http.ResponseWriter, r *http.Request) {
	current, err := s.store.Storage(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "storage not found")
		return
	}
	if current.Type != "object" {
		writeError(w, http.StatusBadRequest, "invalid_storage", "只有对象存储支持创建 Bucket")
		return
	}
	var input struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	bucketName := strings.Trim(strings.TrimSpace(input.Name), "/")
	if bucketName == "" {
		writeError(w, http.StatusBadRequest, "invalid_bucket", "请输入 Bucket 名称")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	if err := remotestorage.CreateObjectBucket(ctx, current, bucketName); err != nil {
		writeError(w, http.StatusBadGateway, "bucket_create_failed", userFacingErrorMessage(errorContextRemoteStorage, err))
		return
	}
	updated, err := s.store.UpdateStorage(current.ID, model.UpdateStorageInput{
		Type:      current.Type,
		Name:      current.Name,
		Provider:  current.Provider,
		Endpoint:  current.Endpoint,
		Bucket:    bucketName,
		Region:    current.Region,
		AccessKey: current.AccessKey,
		Path:      current.Path,
		AuthMode:  current.AuthMode,
		Note:      current.Note,
		Verified:  true,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextRemoteStorage, err))
		return
	}
	buckets, err := remotestorage.ListObjectBuckets(ctx, model.StorageInput{
		Type:      current.Type,
		Endpoint:  current.Endpoint,
		Region:    current.Region,
		AccessKey: current.AccessKey,
		SecretKey: current.SecretKey,
	})
	if err != nil {
		buckets = []string{bucketName}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"storage": redactStorageSecret(updated), "buckets": buckets})
}

func (s *Server) updateStorage(w http.ResponseWriter, r *http.Request) {
	current, err := s.store.Storage(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "storage not found")
		return
	}
	var input model.UpdateStorageInput
	if !decodeJSON(w, r, &input) || !validateStorageUpdate(w, input, current.Type) {
		return
	}
	item, err := s.store.UpdateStorage(current.ID, input)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextRemoteStorage, err))
		return
	}
	writeJSON(w, http.StatusOK, redactStorageSecret(item))
}

func (s *Server) deleteStorage(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteStorage(r.PathValue("id")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "storage not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextRemoteStorage, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) testStorage(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.Storage(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "storage not found")
		return
	}
	status := "connected"
	message := "远端存储配置可用"
	switch item.Type {
	case "object":
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		if err := remotestorage.TestObjectStorage(ctx, item); err != nil {
			status = "warning"
			message = userFacingErrorMessage(errorContextRemoteStorage, err)
			log.Printf("remote storage[%s] object test failed: %v", item.ID, err)
		} else {
			message = "对象存储测试通过"
		}
	case "google_drive", "one_drive":
		status = "needs_auth"
		message = "该存储需要完成 OAuth 授权后才能测试"
	default:
		status = "warning"
		message = "当前存储类型暂不支持在线测试"
	}
	if err := s.store.UpdateStorageStatus(item.ID, status); err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextRemoteStorage, err))
		return
	}
	item.Status = status
	writeJSON(w, http.StatusOK, map[string]any{"storage": redactStorageSecret(item), "message": message})
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

func (s *Server) createBackup(w http.ResponseWriter, r *http.Request) {
	var request model.BackupRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	normalizeBackupSchedule(&request)
	if request.SourceID == "" || request.SourceDatabase == "" || request.Destination == "" {
		if request.SourceID != "" && len(request.SourceDatabases) > 0 && request.Destination != "" {
			request.SourceDatabase = strings.Join(request.SourceDatabases, ", ")
		}
	}
	if request.SourceID == "" || request.SourceDatabase == "" || request.Destination == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "请完整选择源端数据库和备份目标")
		return
	}
	source, err := s.store.Connection(request.SourceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_source", "未找到源端连接，请重新选择")
		return
	}
	destinationLabel := request.Destination
	if request.DestinationType == "" || request.DestinationType == "storage" {
		storage, err := s.store.Storage(request.Destination)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_destination", "未找到备份目标存储，请重新选择")
			return
		}
		if storage.Type == "object" {
			request.DestinationType = "object"
			request.Storage = &storage
			destinationLabel = storage.Name + " / " + storage.Bucket
		} else if storage.Type == "local" {
			request.DestinationType = "local"
			request.Destination = storage.Path
			destinationLabel = storage.Name + " / " + storage.Path
		} else {
			writeError(w, http.StatusBadRequest, "invalid_destination", "该存储类型暂不支持备份写入")
			return
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now().In(appLocation)
	status := model.StatusPending
	stage := "pending"
	progress := 0
	var nextRunAt *time.Time
	if request.ScheduleMode == "scheduled" {
		status = model.StatusScheduled
		stage = "scheduled"
		next, err := nextBackupRun(request, now)
		if err != nil {
			cancel()
			writeError(w, http.StatusBadRequest, "invalid_schedule", err.Error())
			return
		}
		nextRunAt = &next
	}
	value := model.BackupJob{
		ID: store.NewID(), Name: request.Name, Status: status, Stage: stage, Progress: progress,
		StartedAt: now, SourceName: source.Name, SourceType: source.Type,
		SourceDatabase: request.SourceDatabase, DestinationType: request.DestinationType,
		Destination: destinationLabel, Request: request, ScheduleMode: request.ScheduleMode,
		ScheduleFrequency: request.ScheduleFrequency, ScheduleTime: request.ScheduleTime,
		ScheduleWeekday: request.ScheduleWeekday, ScheduleMonthDay: request.ScheduleMonthDay,
		Notification: request.Notification, NotificationEnabled: request.Notification.Enabled,
		NotificationMethod: request.Notification.Method, NotificationTarget: request.Notification.Target,
		NextRunAt: nextRunAt,
		Events:    []model.Event{{Time: now, Level: "INFO", Message: "备份任务已创建"}},
	}
	if request.ScheduleMode == "scheduled" {
		value.Events = append(value.Events, model.Event{Time: now, Level: "INFO", Message: "定时备份计划已保存，等待下一次执行"})
	}
	if value.Name == "" {
		value.Name = request.SourceDatabase + " 备份"
	}
	if err := s.store.AddBackup(value, cancel); err != nil {
		cancel()
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextCreateBackup, err))
		return
	}
	if request.ScheduleMode != "scheduled" {
		go s.runBackup(ctx, value.ID, source, request)
	}
	writeJSON(w, http.StatusAccepted, value)
}

func (s *Server) runBackup(ctx context.Context, id string, source model.Connection, request model.BackupRequest) {
	log.Printf("backup[%s] started: %s/%s -> %s", id, source.Name, request.SourceDatabase, request.DestinationType)
	started := time.Now()
	versionID := store.NewID()
	_ = s.store.UpdateBackup(id, func(item *model.BackupJob) {
		item.Status = model.StatusRunning
		item.Stage = "export"
		item.Progress = 5
		item.StartedAt = started
		item.LastRunAt = &started
		item.FinishedAt = nil
		item.Error = ""
		item.Artifacts = nil
		item.ArtifactVersions = append([]model.BackupArtifactVersion{{
			ID:        versionID,
			StartedAt: started,
			Status:    model.StatusRunning,
			Stage:     "export",
		}}, item.ArtifactVersions...)
		if len(item.ArtifactVersions) > 20 {
			item.ArtifactVersions = item.ArtifactVersions[:20]
		}
	})
	reporter := func(progress backup.Progress) {
		_ = s.store.UpdateBackup(id, func(item *model.BackupJob) {
			item.Stage = progress.Stage
			item.Progress = backupProgress(progress.Stage, progress.Progress)
			if progress.ArtifactName != "" {
				item.ArtifactName = progress.ArtifactName
			}
			if progress.ArtifactSize > 0 {
				item.ArtifactSize = progress.ArtifactSize
			}
			if progress.ArtifactPath != "" {
				item.ArtifactPath = progress.ArtifactPath
			}
			if progress.EventMessage != "" {
				item.Events = append(item.Events, model.Event{Time: time.Now(), Level: progress.EventLevel, Message: progress.EventMessage})
				if len(item.Events) > 100 {
					item.Events = item.Events[len(item.Events)-100:]
				}
			}
		})
	}
	databases := backupDatabases(request)
	var err error
	for index, database := range databases {
		current := request
		current.SourceDatabase = database
		reporter(backup.Progress{Stage: "export", EventLevel: "INFO", EventMessage: fmt.Sprintf("开始备份数据库 %s（%d/%d）", database, index+1, len(databases)), Progress: index * 90 / len(databases)})
		if runErr := s.backup.Run(ctx, source, current, func(progress backup.Progress) {
			if len(databases) > 1 {
				progress.Progress = index*90/len(databases) + backupProgress(progress.Stage, progress.Progress)/len(databases)
			}
			if progress.ArtifactName != "" {
				s.recordBackupArtifact(id, versionID, database, progress)
			}
			reporter(progress)
		}); runErr != nil {
			err = runErr
			break
		}
	}
	finished := time.Now()
	_ = s.store.UpdateBackup(id, func(item *model.BackupJob) {
		item.FinishedAt = &finished
		if item.ScheduleMode == "scheduled" {
			next, nextErr := nextBackupRun(item.Request, finished)
			if nextErr == nil {
				item.NextRunAt = &next
			}
		}
		if err == nil {
			if item.ScheduleMode == "scheduled" {
				item.Status = model.StatusScheduled
				item.Stage = "scheduled"
			} else {
				item.Status = model.StatusCompleted
				item.Stage = "completed"
			}
			item.Progress = 100
			finishBackupArtifactVersion(item, versionID, model.StatusCompleted, "completed", finished, "")
			item.Events = append(item.Events, model.Event{Time: finished, Level: "INFO", Message: "备份任务完成"})
			if item.ScheduleMode == "scheduled" && item.NextRunAt != nil {
				item.Events = append(item.Events, model.Event{Time: finished, Level: "INFO", Message: "下一次备份将在 " + item.NextRunAt.Format("2006-01-02 15:04") + " 执行"})
			}
			return
		}
		if errors.Is(err, context.Canceled) {
			item.Status = model.StatusCancelled
			item.Stage = "cancelled"
			finishBackupArtifactVersion(item, versionID, model.StatusCancelled, "cancelled", finished, "任务已取消")
			item.Events = append(item.Events, model.Event{Time: finished, Level: "WARN", Message: "任务已取消"})
		} else {
			item.Status = model.StatusFailed
			item.Stage = "failed"
			item.Error = userFacingErrorMessage(errorContextBackupRuntime, err)
			finishBackupArtifactVersion(item, versionID, model.StatusFailed, "failed", finished, err.Error())
			item.Events = append(item.Events, model.Event{Time: finished, Level: "ERROR", Message: err.Error()})
		}
	})
	if updatedJob, loadErr := s.store.Backup(id); loadErr == nil && updatedJob.ScheduleMode == "scheduled" && updatedJob.Notification.Enabled {
		if notifyErr := s.sendBackupNotification(updatedJob, err == nil); notifyErr != nil {
			log.Printf("backup[%s] notification failed: %v", id, notifyErr)
			_ = s.store.UpdateBackup(id, func(item *model.BackupJob) {
				item.Events = append(item.Events, model.Event{Time: time.Now(), Level: "WARN", Message: "备份通知发送失败：" + notifyErr.Error()})
			})
		} else {
			_ = s.store.UpdateBackup(id, func(item *model.BackupJob) {
				item.Events = append(item.Events, model.Event{Time: time.Now(), Level: "INFO", Message: "备份通知已发送"})
			})
		}
	}
}

func (s *Server) recordBackupArtifact(id, versionID, database string, progress backup.Progress) {
	_ = s.store.UpdateBackup(id, func(item *model.BackupJob) {
		artifact := model.BackupArtifact{
			Database: database,
			Name:     progress.ArtifactName,
			Size:     progress.ArtifactSize,
			Path:     progress.ArtifactPath,
		}
		recordArtifactInVersion(item, versionID, artifact)
		for index := range item.Artifacts {
			if item.Artifacts[index].Database == database {
				item.Artifacts[index] = artifact
				return
			}
		}
		item.Artifacts = append(item.Artifacts, artifact)
	})
}

func recordArtifactInVersion(item *model.BackupJob, versionID string, artifact model.BackupArtifact) {
	for versionIndex := range item.ArtifactVersions {
		if item.ArtifactVersions[versionIndex].ID != versionID {
			continue
		}
		for artifactIndex := range item.ArtifactVersions[versionIndex].Artifacts {
			if item.ArtifactVersions[versionIndex].Artifacts[artifactIndex].Database == artifact.Database {
				item.ArtifactVersions[versionIndex].Artifacts[artifactIndex] = artifact
				return
			}
		}
		item.ArtifactVersions[versionIndex].Artifacts = append(item.ArtifactVersions[versionIndex].Artifacts, artifact)
		return
	}
}

func finishBackupArtifactVersion(item *model.BackupJob, versionID string, status model.MigrationStatus, stage string, finished time.Time, message string) {
	for index := range item.ArtifactVersions {
		if item.ArtifactVersions[index].ID != versionID {
			continue
		}
		item.ArtifactVersions[index].Status = status
		item.ArtifactVersions[index].Stage = stage
		item.ArtifactVersions[index].FinishedAt = &finished
		item.ArtifactVersions[index].Error = message
		return
	}
}

func backupDatabases(request model.BackupRequest) []string {
	seen := map[string]bool{}
	var databases []string
	for _, database := range request.SourceDatabases {
		database = strings.TrimSpace(database)
		if database != "" && !seen[database] {
			seen[database] = true
			databases = append(databases, database)
		}
	}
	if len(databases) == 0 && strings.TrimSpace(request.SourceDatabase) != "" {
		databases = []string{strings.TrimSpace(request.SourceDatabase)}
	}
	return databases
}

func splitDatabaseList(value string) []string {
	seen := map[string]bool{}
	var databases []string
	for _, database := range strings.Split(value, ",") {
		database = strings.TrimSpace(database)
		if database != "" && !seen[database] {
			seen[database] = true
			databases = append(databases, database)
		}
	}
	return databases
}

func restoreTargetDatabases(sourceDatabases []string, input model.RestoreBackupRequest) (map[string]string, error) {
	if len(sourceDatabases) == 0 {
		return nil, fmt.Errorf("未找到备份中的源数据库")
	}
	targets := map[string]string{}
	for _, sourceDatabase := range sourceDatabases {
		target := strings.TrimSpace(input.TargetDatabases[sourceDatabase])
		if target == "" && len(sourceDatabases) == 1 {
			target = strings.TrimSpace(input.TargetDatabase)
		}
		if target == "" {
			target = sourceDatabase
		}
		targets[sourceDatabase] = target
	}
	return targets, nil
}

func selectedRestoreDatabases(available, selected []string) ([]string, error) {
	if len(available) == 0 {
		return nil, fmt.Errorf("未找到备份中的源数据库")
	}
	if len(selected) == 0 {
		return available, nil
	}
	allowed := map[string]bool{}
	for _, database := range available {
		allowed[database] = true
	}
	seen := map[string]bool{}
	var result []string
	for _, database := range selected {
		database = strings.TrimSpace(database)
		if database == "" || seen[database] {
			continue
		}
		if !allowed[database] {
			return nil, fmt.Errorf("选择的数据库 %s 不在该备份任务中", database)
		}
		seen[database] = true
		result = append(result, database)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("请至少选择一个要恢复的数据库")
	}
	return result, nil
}

func normalizeRestoreStrategy(strategy string) string {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case "recreate", "drop-create", "drop_create":
		return "recreate"
	default:
		return "append"
	}
}

func restoreStrategyLabel(strategy string) string {
	if strategy == "recreate" {
		return "重建目标库后恢复"
	}
	return "追加导入，冲突则失败"
}

func restoreTargetLabel(sourceDatabases []string, targetDatabases map[string]string) string {
	var labels []string
	for _, sourceDatabase := range sourceDatabases {
		targetDatabase := strings.TrimSpace(targetDatabases[sourceDatabase])
		if targetDatabase == "" {
			targetDatabase = sourceDatabase
		}
		labels = append(labels, targetDatabase)
	}
	return strings.Join(labels, ", ")
}

func artifactPathForRestoreDatabase(artifactPath, database string) string {
	if strings.TrimSpace(database) == "" {
		return artifactPath
	}
	base := filepath.Base(artifactPath)
	dir := filepath.Dir(artifactPath)
	if strings.Contains(base, sanitizeFilePart(database)) {
		return artifactPath
	}
	candidates, err := filepath.Glob(filepath.Join(dir, "*", base))
	if err != nil {
		return artifactPath
	}
	for _, candidate := range candidates {
		if strings.Contains(filepath.ToSlash(candidate), "/"+sanitizeFilePart(database)+"/") {
			return candidate
		}
	}
	return artifactPath
}

func sanitizeFilePart(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "database"
	}
	var builder strings.Builder
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' || char == '_' {
			builder.WriteRune(char)
		} else {
			builder.WriteRune('_')
		}
	}
	return strings.Trim(builder.String(), "_")
}

func backupProgress(stage string, reported int) int {
	if reported > 0 {
		return reported
	}
	switch stage {
	case "download":
		return 20
	case "export":
		return 35
	case "extract":
		return 25
	case "restore":
		return 75
	case "validation":
		return 95
	case "upload":
		return 85
	case "completed":
		return 100
	default:
		return 10
	}
}

func formatBytes(value int64) string {
	if value < 1024 {
		return fmt.Sprintf("%d B", value)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	size := float64(value)
	index := -1
	for size >= 1024 && index < len(units)-1 {
		size /= 1024
		index++
	}
	return fmt.Sprintf("%.1f %s", size, units[index])
}

func (s *Server) startBackupScheduler() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			s.runDueBackups()
		}
	}()
}

func (s *Server) runDueBackups() {
	s.reconcileScheduledBackupRuns()
	items, err := s.store.Backups()
	if err != nil {
		log.Printf("backup scheduler list failed: %v", err)
		return
	}
	now := time.Now().In(appLocation)
	for _, item := range items {
		if item.ScheduleMode != "scheduled" || item.Status != model.StatusScheduled || item.NextRunAt == nil || item.NextRunAt.After(now) {
			continue
		}
		if _, err := s.triggerScheduledBackup(item, "定时备份已触发"); err != nil {
			log.Printf("backup scheduler trigger failed: %s: %v", item.ID, err)
		}
	}
}

func (s *Server) reconcileScheduledBackupRuns() {
	items, err := s.store.Backups()
	if err != nil {
		log.Printf("backup scheduler reconcile list failed: %v", err)
		return
	}
	now := time.Now().In(appLocation)
	for _, item := range items {
		s.reconcileScheduledBackupRun(item, now)
	}
}

func (s *Server) reconcileScheduledBackupRun(item model.BackupJob, now time.Time) {
	request, ok := scheduleRequestForBackup(item)
	if !ok {
		return
	}
	if item.NextRunAt != nil && scheduleClockMatches(request, *item.NextRunAt) {
		return
	}
	next, err := nextBackupRun(request, now)
	if err != nil {
		log.Printf("backup scheduler reconcile failed: %s: %v", item.ID, err)
		return
	}
	if err := s.store.UpdateBackup(item.ID, func(job *model.BackupJob) {
		if job.ScheduleMode != "scheduled" || job.Status != model.StatusScheduled {
			return
		}
		job.Request = request
		job.ScheduleMode = request.ScheduleMode
		job.ScheduleFrequency = request.ScheduleFrequency
		job.ScheduleTime = request.ScheduleTime
		job.ScheduleWeekday = request.ScheduleWeekday
		job.ScheduleMonthDay = request.ScheduleMonthDay
		job.NextRunAt = &next
		job.Events = append(job.Events, model.Event{Time: now, Level: "INFO", Message: "已按应用时区修正下一次执行时间"})
	}); err != nil {
		log.Printf("backup scheduler reconcile update failed: %s: %v", item.ID, err)
	}
}

func scheduleRequestForBackup(item model.BackupJob) (model.BackupRequest, bool) {
	if item.ScheduleMode != "scheduled" || item.Status != model.StatusScheduled {
		return model.BackupRequest{}, false
	}
	request := item.Request
	request.ScheduleMode = "scheduled"
	if request.ScheduleFrequency == "" {
		request.ScheduleFrequency = item.ScheduleFrequency
	}
	if request.ScheduleTime == "" {
		request.ScheduleTime = item.ScheduleTime
	}
	if request.ScheduleWeekday == 0 {
		request.ScheduleWeekday = item.ScheduleWeekday
	}
	if request.ScheduleMonthDay == 0 {
		request.ScheduleMonthDay = item.ScheduleMonthDay
	}
	normalizeBackupSchedule(&request)
	return request, true
}

func scheduleClockMatches(request model.BackupRequest, value time.Time) bool {
	hour, minute, second, err := parseScheduleTime(request.ScheduleTime)
	if err != nil {
		return true
	}
	local := value.In(appLocation)
	return local.Hour() == hour && local.Minute() == minute && local.Second() == second
}

func (s *Server) triggerScheduledBackup(item model.BackupJob, message string) (model.BackupJob, error) {
	now := time.Now().In(appLocation)
	if item.ScheduleMode != "scheduled" || item.Status != model.StatusScheduled {
		return item, fmt.Errorf("仅已计划的定时备份可以触发")
	}
	request, err := s.backfillBackupRequest(item)
	if err != nil {
		_ = s.store.UpdateBackup(item.ID, func(job *model.BackupJob) {
			job.Status = model.StatusFailed
			job.Stage = "failed"
			job.Error = err.Error()
			job.Events = append(job.Events, model.Event{Time: now, Level: "ERROR", Message: err.Error()})
		})
		return item, err
	}
	source, err := s.store.Connection(request.SourceID)
	if err != nil {
		err = fmt.Errorf("未找到源端连接，请重新配置定时备份")
		_ = s.store.UpdateBackup(item.ID, func(job *model.BackupJob) {
			job.Status = model.StatusFailed
			job.Stage = "failed"
			job.Error = err.Error()
			job.Events = append(job.Events, model.Event{Time: now, Level: "ERROR", Message: err.Error()})
		})
		return item, err
	}
	request, err = s.hydrateBackupRequest(request)
	if err != nil {
		_ = s.store.UpdateBackup(item.ID, func(job *model.BackupJob) {
			job.Status = model.StatusFailed
			job.Stage = "failed"
			job.Error = userFacingErrorMessage(errorContextCreateBackup, err)
			job.Events = append(job.Events, model.Event{Time: now, Level: "ERROR", Message: err.Error()})
		})
		return item, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := s.store.UpdateBackup(item.ID, func(job *model.BackupJob) {
		job.Request = request
		job.Status = model.StatusPending
		job.Stage = "pending"
		job.Progress = 0
		job.Error = ""
		job.Events = append(job.Events, model.Event{Time: now, Level: "INFO", Message: message})
	}); err != nil {
		cancel()
		return item, err
	}
	updated, err := s.store.Backup(item.ID)
	if err != nil {
		cancel()
		return item, err
	}
	go s.runBackup(ctx, item.ID, source, request)
	return updated, nil
}

func (s *Server) hydrateBackupRequest(request model.BackupRequest) (model.BackupRequest, error) {
	if request.DestinationType == "object" {
		storage, err := s.store.Storage(request.Destination)
		if err != nil {
			return request, fmt.Errorf("未找到备份目标存储，请重新选择")
		}
		request.Storage = &storage
	}
	return request, nil
}

func (s *Server) backfillBackupRequest(item model.BackupJob) (model.BackupRequest, error) {
	request := item.Request
	if request.SourceID == "" {
		connections, err := s.store.Connections()
		if err != nil {
			return request, fmt.Errorf("读取源端连接失败")
		}
		for _, connection := range connections {
			if connection.Name == item.SourceName {
				request.SourceID = connection.ID
				break
			}
		}
	}
	if request.SourceDatabase == "" {
		request.SourceDatabase = item.SourceDatabase
	}
	if len(request.SourceDatabases) == 0 {
		request.SourceDatabases = backupDatabases(request)
	}
	if request.DestinationType == "" {
		request.DestinationType = item.DestinationType
	}
	if request.Destination == "" {
		storages, err := s.store.Storages()
		if err != nil {
			return request, fmt.Errorf("读取备份目标失败")
		}
		for _, storage := range storages {
			if item.Destination == storage.Name+" / "+storage.Bucket || item.Destination == storage.Name+" / "+storage.Path || item.Destination == storage.Name {
				request.Destination = storage.ID
				break
			}
		}
	}
	if request.ScheduleMode == "" {
		request.ScheduleMode = item.ScheduleMode
	}
	if request.ScheduleFrequency == "" {
		request.ScheduleFrequency = item.ScheduleFrequency
	}
	if request.ScheduleTime == "" {
		request.ScheduleTime = item.ScheduleTime
	}
	if request.ScheduleWeekday == 0 {
		request.ScheduleWeekday = item.ScheduleWeekday
	}
	if request.ScheduleMonthDay == 0 {
		request.ScheduleMonthDay = item.ScheduleMonthDay
	}
	if !request.Notification.Enabled && request.Notification.Method == "" && request.Notification.Target == "" {
		request.Notification.Enabled = item.Notification.Enabled || item.NotificationEnabled
		if item.Notification.Method != "" || item.Notification.Target != "" {
			request.Notification.Method = item.Notification.Method
			request.Notification.Target = item.Notification.Target
		} else {
			request.Notification.Method = item.NotificationMethod
			request.Notification.Target = item.NotificationTarget
		}
	}
	if request.Name == "" {
		request.Name = item.Name
	}
	if request.SourceID == "" {
		return request, fmt.Errorf("未找到源端连接，请重新配置定时备份")
	}
	if request.Destination == "" {
		return request, fmt.Errorf("未找到备份目标存储，请重新配置定时备份")
	}
	return request, nil
}

func normalizeBackupSchedule(request *model.BackupRequest) {
	if request.ScheduleMode != "scheduled" {
		request.ScheduleMode = "once"
		request.ScheduleFrequency = ""
		request.ScheduleTime = ""
		request.ScheduleWeekday = 0
		request.ScheduleMonthDay = 0
		request.Notification = model.BackupNotification{}
		request.NotificationEnabled = false
		request.NotificationMethod = ""
		request.NotificationTarget = ""
		return
	}
	if request.ScheduleFrequency == "" {
		request.ScheduleFrequency = "daily"
	}
	if request.ScheduleTime == "" {
		request.ScheduleTime = "02:00:00"
	}
	if request.ScheduleWeekday < 0 || request.ScheduleWeekday > 6 {
		request.ScheduleWeekday = 1
	}
	if request.ScheduleMonthDay < 1 || request.ScheduleMonthDay > 28 {
		request.ScheduleMonthDay = 1
	}
	normalizeBackupNotification(request)
	if request.Notification.Enabled {
		if request.Notification.Method != "webhook" && request.Notification.Method != "email" {
			request.Notification.Method = "webhook"
		}
		request.Notification.Target = strings.TrimSpace(request.Notification.Target)
		if request.Notification.Target == "" {
			request.Notification.Enabled = false
		}
	}
	request.NotificationEnabled = request.Notification.Enabled
	request.NotificationMethod = request.Notification.Method
	request.NotificationTarget = request.Notification.Target
}

func normalizeBackupNotification(request *model.BackupRequest) {
	if request.Notification.Enabled || request.Notification.Method != "" || request.Notification.Target != "" {
		if request.Notification.Method == "" {
			request.Notification.Method = request.NotificationMethod
		}
		if request.Notification.Target == "" {
			request.Notification.Target = request.NotificationTarget
		}
		request.Notification.Enabled = request.Notification.Enabled || request.NotificationEnabled
	}
	if request.Notification.Method == "" {
		request.Notification.Method = request.NotificationMethod
	}
	if request.Notification.Target == "" {
		request.Notification.Target = request.NotificationTarget
	}
	if request.Notification.Enabled == false {
		request.Notification.Enabled = request.NotificationEnabled
	}
	request.NotificationEnabled = request.Notification.Enabled
	request.NotificationMethod = request.Notification.Method
	request.NotificationTarget = request.Notification.Target
}

func (s *Server) sendBackupNotification(job model.BackupJob, success bool) error {
	if !job.Notification.Enabled {
		return nil
	}
	switch job.Notification.Method {
	case "webhook":
		return s.sendBackupWebhook(job, success)
	case "email":
		return s.sendBackupEmail(job, success)
	default:
		return fmt.Errorf("不支持的通知方式")
	}
}

func (s *Server) sendBackupWebhook(job model.BackupJob, success bool) error {
	target, err := url.Parse(strings.TrimSpace(job.Notification.Target))
	if err != nil || target.Scheme == "" || target.Host == "" {
		return fmt.Errorf("webhook 地址无效")
	}
	message := backupNotificationText(job, success)
	markdownMessage := backupNotificationMarkdown(job, success)
	payload := map[string]any{
		"jobId":          job.ID,
		"jobName":        job.Name,
		"success":        success,
		"status":         backupNotificationStatus(success),
		"text":           message,
		"markdown":       markdownMessage,
		"sourceName":     job.SourceName,
		"sourceType":     job.SourceType,
		"sourceDatabase": job.SourceDatabase,
		"destination":    job.Destination,
		"finishedAt":     time.Now().Format(time.RFC3339),
	}
	payload = webhookPayloadForTarget(target.Host, payload, message, markdownMessage)
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("构造 webhook 通知失败")
	}
	req, err := http.NewRequest(http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("创建 webhook 请求失败")
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("发送 webhook 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook 返回 %d", resp.StatusCode)
	}
	return nil
}

func webhookPayloadForTarget(host string, payload map[string]any, message string, markdownMessage string) map[string]any {
	lowerHost := strings.ToLower(host)
	switch {
	case strings.Contains(lowerHost, "dingtalk.com"):
		title := strings.SplitN(message, "\n", 2)[0]
		return map[string]any{"msgtype": "markdown", "markdown": map[string]string{"title": title, "text": markdownMessage}}
	case strings.Contains(lowerHost, "feishu.cn"), strings.Contains(lowerHost, "larksuite.com"):
		return map[string]any{"msg_type": "interactive", "card": map[string]any{"elements": []map[string]any{{"tag": "markdown", "content": markdownMessage}}}}
	case strings.Contains(lowerHost, "qyapi.weixin.qq.com"):
		return map[string]any{"msgtype": "markdown", "markdown": map[string]string{"content": markdownMessage}}
	case strings.Contains(lowerHost, "hooks.slack.com"):
		return map[string]any{"text": markdownMessage}
	default:
		return payload
	}
}

func (s *Server) sendBackupEmail(job model.BackupJob, success bool) error {
	settings, err := s.store.SMTPSettings()
	if err != nil {
		return err
	}
	if !settings.Enabled || settings.Host == "" || settings.Port <= 0 || settings.From == "" {
		return fmt.Errorf("未配置邮件服务")
	}
	to := strings.TrimSpace(job.Notification.Target)
	if to == "" {
		return fmt.Errorf("邮件收件人不能为空")
	}
	addr := net.JoinHostPort(settings.Host, strconv.Itoa(settings.Port))
	subject := fmt.Sprintf("DataBridge 备份%s：%s", backupNotificationStatus(success), job.Name)
	msg := strings.Join([]string{
		"To: " + to,
		"From: " + settings.From,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"",
		backupNotificationText(job, success),
	}, "\r\n")
	var auth smtp.Auth
	if settings.Username != "" {
		auth = smtp.PlainAuth("", settings.Username, settings.Password, settings.Host)
	}
	return smtp.SendMail(addr, auth, settings.From, []string{to}, []byte(msg))
}

func backupNotificationStatus(success bool) string {
	if success {
		return "成功"
	}
	return "失败"
}

func backupNotificationText(job model.BackupJob, success bool) string {
	return strings.Join(backupNotificationLines(job, success), "\n")
}

func backupNotificationMarkdown(job model.BackupJob, success bool) string {
	lines := backupNotificationLines(job, success)
	if len(lines) > 0 {
		lines[0] = fmt.Sprintf("**%s**", lines[0])
	}
	return strings.Join(lines, "\n")
}

func backupNotificationLines(job model.BackupJob, success bool) []string {
	lines := []string{
		fmt.Sprintf("DataBridge 备份%s", backupNotificationStatus(success)),
		fmt.Sprintf("任务名称：%s", job.Name),
	}
	if finishedAt := backupNotificationFinishedAt(job); finishedAt != "" {
		lines = append(lines, fmt.Sprintf("备份时间：%s", finishedAt))
	}
	lines = append(lines,
		fmt.Sprintf("源数据库：%s", job.SourceDatabase),
		fmt.Sprintf("目标位置：%s", job.Destination),
	)
	lines = append(lines, backupNotificationArtifactLines(job)...)
	return lines
}

func backupNotificationFinishedAt(job model.BackupJob) string {
	if job.FinishedAt == nil || job.FinishedAt.IsZero() {
		return ""
	}
	return job.FinishedAt.In(appLocation).Format("2006-01-02 15:04:05")
}

func backupNotificationArtifactLines(job model.BackupJob) []string {
	artifacts := make([]model.BackupArtifact, 0, len(job.Artifacts)+1)
	for _, artifact := range job.Artifacts {
		if strings.TrimSpace(artifact.Name) == "" {
			continue
		}
		artifacts = append(artifacts, artifact)
	}
	if len(artifacts) == 0 && strings.TrimSpace(job.ArtifactName) != "" {
		artifacts = append(artifacts, model.BackupArtifact{
			Database: job.SourceDatabase,
			Name:     job.ArtifactName,
			Size:     job.ArtifactSize,
			Path:     job.ArtifactPath,
		})
	}
	if len(artifacts) == 0 {
		return nil
	}
	lines := make([]string, 0, len(artifacts)+1)
	lines = append(lines, "备份文件：")
	for _, artifact := range artifacts {
		name := strings.TrimSpace(artifact.Name)
		if name == "" {
			continue
		}
		label := strings.TrimSpace(artifact.Database)
		if label == "" {
			label = job.SourceDatabase
		}
		size := formatNotificationBytes(artifact.Size)
		lines = append(lines, fmt.Sprintf("- %s：%s（%s）", label, name, size))
	}
	return lines
}

func formatNotificationBytes(value int64) string {
	if value <= 0 {
		return "未知大小"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	size := float64(value)
	unit := 0
	for size >= 1024 && unit < len(units)-1 {
		size /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d %s", value, units[unit])
	}
	return fmt.Sprintf("%.2f %s", size, units[unit])
}

func (s *Server) testBackupNotification(w http.ResponseWriter, r *http.Request) {
	var input model.BackupNotificationTestRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Method = strings.TrimSpace(input.Method)
	input.Target = strings.TrimSpace(input.Target)
	if input.Method != "webhook" && input.Method != "email" {
		writeError(w, http.StatusBadRequest, "invalid_request", "不支持的通知方式")
		return
	}
	if input.Target == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "请填写通知目标")
		return
	}
	now := time.Now()
	job := model.BackupJob{
		ID:             "notification-test",
		Name:           "通知测试",
		SourceName:     "DataBridge",
		SourceType:     "System",
		SourceDatabase: "system",
		Destination:    "notification-test",
		FinishedAt:     &now,
		Notification: model.BackupNotification{
			Enabled: true,
			Method:  input.Method,
			Target:  input.Target,
		},
	}
	if err := s.sendBackupNotification(job, true); err != nil {
		writeError(w, http.StatusBadRequest, "notification_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "测试通知已发送"})
}

func nextBackupRun(request model.BackupRequest, after time.Time) (time.Time, error) {
	after = after.In(appLocation)
	hour, minute, second, err := parseScheduleTime(request.ScheduleTime)
	if err != nil {
		return time.Time{}, err
	}
	location := appLocation
	candidate := time.Date(after.Year(), after.Month(), after.Day(), hour, minute, second, 0, location)
	switch request.ScheduleFrequency {
	case "daily":
		if !candidate.After(after) {
			candidate = candidate.AddDate(0, 0, 1)
		}
		return candidate, nil
	case "weekly":
		weekday := time.Weekday(request.ScheduleWeekday)
		days := (int(weekday) - int(candidate.Weekday()) + 7) % 7
		candidate = candidate.AddDate(0, 0, days)
		if !candidate.After(after) {
			candidate = candidate.AddDate(0, 0, 7)
		}
		return candidate, nil
	case "monthly":
		day := request.ScheduleMonthDay
		candidate = time.Date(after.Year(), after.Month(), min(day, daysInMonth(after.Year(), after.Month())), hour, minute, second, 0, location)
		if !candidate.After(after) {
			nextMonth := after.AddDate(0, 1, 0)
			candidate = time.Date(nextMonth.Year(), nextMonth.Month(), min(day, daysInMonth(nextMonth.Year(), nextMonth.Month())), hour, minute, second, 0, location)
		}
		return candidate, nil
	default:
		return time.Time{}, fmt.Errorf("不支持的定时频率")
	}
}

func parseScheduleTime(value string) (int, int, int, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 && len(parts) != 3 {
		return 0, 0, 0, fmt.Errorf("请填写有效的执行时间")
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("请填写有效的执行时间")
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("请填写有效的执行时间")
	}
	second := 0
	if len(parts) == 3 {
		second, err = strconv.Atoi(parts[2])
		if err != nil {
			return 0, 0, 0, fmt.Errorf("请填写有效的执行时间")
		}
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 || second < 0 || second > 59 {
		return 0, 0, 0, fmt.Errorf("请填写有效的执行时间")
	}
	return hour, minute, second, nil
}

func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, appLocation).Day()
}

func (s *Server) getBackup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	item, err := s.store.Backup(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "backup not found")
		return
	}
	s.reconcileScheduledBackupRun(item, time.Now().In(appLocation))
	if compactBackupResponse(r) {
		item = compactBackupJob(item)
	}
	writeJSON(w, http.StatusOK, item)
}

func compactBackupResponse(r *http.Request) bool {
	value := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("compact")))
	return value == "1" || value == "true"
}

func compactBackupJob(job model.BackupJob) model.BackupJob {
	job.ArtifactVersions = nil
	job.Request = model.BackupRequest{}
	return job
}

func (s *Server) updateBackup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	current, err := s.store.Backup(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "backup not found")
		return
	}
	if current.ScheduleMode != "scheduled" || current.Status != model.StatusScheduled {
		writeError(w, http.StatusBadRequest, "invalid_request", "仅已计划的定时备份可以编辑")
		return
	}
	var input model.BackupRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	request, err := s.backfillBackupRequest(current)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if strings.TrimSpace(input.Name) != "" {
		request.Name = strings.TrimSpace(input.Name)
	}
	request.ScheduleMode = "scheduled"
	request.ScheduleFrequency = input.ScheduleFrequency
	request.ScheduleTime = input.ScheduleTime
	request.ScheduleWeekday = input.ScheduleWeekday
	request.ScheduleMonthDay = input.ScheduleMonthDay
	request.NotificationEnabled = input.NotificationEnabled
	request.NotificationMethod = input.NotificationMethod
	request.NotificationTarget = input.NotificationTarget
	request.Notification = input.Notification
	normalizeBackupSchedule(&request)
	now := time.Now().In(appLocation)
	next, err := nextBackupRun(request, now)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_schedule", err.Error())
		return
	}
	if err := s.store.UpdateBackup(id, func(item *model.BackupJob) {
		item.Name = request.Name
		item.Request = request
		item.ScheduleMode = request.ScheduleMode
		item.ScheduleFrequency = request.ScheduleFrequency
		item.ScheduleTime = request.ScheduleTime
		item.ScheduleWeekday = request.ScheduleWeekday
		item.ScheduleMonthDay = request.ScheduleMonthDay
		item.Notification = request.Notification
		item.NotificationEnabled = request.Notification.Enabled
		item.NotificationMethod = request.Notification.Method
		item.NotificationTarget = request.Notification.Target
		item.NextRunAt = &next
		item.Events = append(item.Events, model.Event{Time: now, Level: "INFO", Message: "定时备份计划已更新"})
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextCreateBackup, err))
		return
	}
	updated, err := s.store.Backup(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextCreateBackup, err))
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) triggerBackup(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.Backup(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "backup not found")
		return
	}
	updated, err := s.triggerScheduledBackup(item, "手动触发定时备份")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, updated)
}

func (s *Server) restoreBackup(w http.ResponseWriter, r *http.Request) {
	sourceJob, err := s.store.Backup(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "backup not found")
		return
	}
	var input model.RestoreBackupRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	input.TargetDatabase = strings.TrimSpace(input.TargetDatabase)
	plan, err := s.buildRestorePlan(sourceJob, input)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if plan.TargetID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "请选择恢复目标连接")
		return
	}
	restoreJob, err := backupJobForArtifactVersion(sourceJob, plan.ArtifactVersionID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_artifact", err.Error())
		return
	}
	if !backupArtifactRestorable(restoreJob) {
		writeError(w, http.StatusBadRequest, "invalid_request", backupArtifactUnavailableMessage(restoreJob))
		return
	}
	restoreSource, err := s.restoreSource(restoreJob)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_artifact", err.Error())
		return
	}
	target, err := s.store.Connection(plan.TargetID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_target", "未找到目标连接，请重新选择")
		return
	}
	if target.Type != sourceJob.SourceType {
		writeError(w, http.StatusBadRequest, "invalid_target", "恢复目标类型必须与备份源类型一致")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now().In(appLocation)
	summaryArtifact := restoreArtifactSummary(restoreJob)
	value := model.BackupJob{
		ID: store.NewID(), Name: fmt.Sprintf("恢复 %s 到 %s", strings.Join(plan.SourceDatabases, ", "), plan.TargetLabel),
		Status: model.StatusPending, Stage: "pending", Progress: 0, StartedAt: now,
		SourceName: sourceJob.SourceName, SourceType: sourceJob.SourceType, SourceDatabase: strings.Join(plan.SourceDatabases, ", "),
		DestinationType: "restore", Destination: target.Name + " / " + plan.TargetLabel,
		ArtifactName: summaryArtifact.Name, ArtifactSize: summaryArtifact.Size, ArtifactPath: restoreSource.Path,
		Events: []model.Event{{Time: now, Level: "INFO", Message: "恢复任务已创建，来源备份任务 " + sourceJob.ID}, {Time: now, Level: "INFO", Message: "恢复备份版本：" + restoreVersionLabel(restoreJob, plan.ArtifactVersionID)}, {Time: now, Level: "INFO", Message: "恢复策略：" + restoreStrategyLabel(plan.Strategy)}},
	}
	if err := s.store.AddBackup(value, cancel); err != nil {
		cancel()
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextCreateBackup, err))
		return
	}
	go s.runRestore(ctx, value.ID, target, plan.SourceDatabases, plan.TargetDatabases, plan.Strategy, restoreSource)
	writeJSON(w, http.StatusAccepted, value)
}

func (s *Server) restoreBackupPrecheck(w http.ResponseWriter, r *http.Request) {
	sourceJob, err := s.store.Backup(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "backup not found")
		return
	}
	var input model.RestoreBackupRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	input.TargetDatabase = strings.TrimSpace(input.TargetDatabase)
	plan, err := s.buildRestorePlan(sourceJob, input)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	target, targetErr := s.store.Connection(plan.TargetID)
	if targetErr != nil {
		writeJSON(w, http.StatusOK, model.RestorePrecheckResult{OK: false, Checks: []model.RestorePrecheckItem{{
			Key: "target", Label: "目标连接", Status: "failed", Message: "未找到目标连接，请重新选择",
		}}})
		return
	}
	if target.Type != sourceJob.SourceType {
		writeJSON(w, http.StatusOK, model.RestorePrecheckResult{OK: false, Checks: []model.RestorePrecheckItem{{
			Key: "target_type", Label: "目标类型", Status: "failed", Message: "恢复目标类型必须与备份源类型一致",
		}}})
		return
	}
	result := s.runRestorePrecheck(r.Context(), sourceJob, target, plan)
	writeJSON(w, http.StatusOK, result)
}

type restorePlan struct {
	TargetID          string
	ArtifactVersionID string
	SourceDatabases   []string
	TargetDatabases   map[string]string
	Strategy          string
	TargetLabel       string
}

func (s *Server) buildRestorePlan(sourceJob model.BackupJob, input model.RestoreBackupRequest) (restorePlan, error) {
	strategy := normalizeRestoreStrategy(input.Strategy)
	restoreJob, err := backupJobForArtifactVersion(sourceJob, strings.TrimSpace(input.ArtifactVersionID))
	if err != nil {
		return restorePlan{}, err
	}
	sourceDatabases := restoreSourceDatabasesForJob(restoreJob)
	if len(sourceDatabases) == 0 {
		sourceDatabases = splitDatabaseList(restoreJob.SourceDatabase)
	}
	selectedDatabases := input.SelectedDatabases
	if len(selectedDatabases) == 0 {
		selectedDatabases = input.SourceDatabases
	}
	selected, err := selectedRestoreDatabases(sourceDatabases, selectedDatabases)
	if err != nil {
		return restorePlan{}, err
	}
	targetDatabases, err := restoreTargetDatabases(selected, input)
	if err != nil {
		return restorePlan{}, err
	}
	return restorePlan{
		TargetID:          strings.TrimSpace(input.TargetID),
		ArtifactVersionID: strings.TrimSpace(input.ArtifactVersionID),
		SourceDatabases:   selected,
		TargetDatabases:   targetDatabases,
		Strategy:          strategy,
		TargetLabel:       restoreTargetLabel(selected, targetDatabases),
	}, nil
}

func (s *Server) runRestorePrecheck(ctx context.Context, sourceJob model.BackupJob, target model.Connection, plan restorePlan) model.RestorePrecheckResult {
	checks := []model.RestorePrecheckItem{}
	add := func(key, label, status, message string) {
		checks = append(checks, model.RestorePrecheckItem{Key: key, Label: label, Status: status, Message: message})
	}
	restoreJob, versionErr := backupJobForArtifactVersion(sourceJob, plan.ArtifactVersionID)
	if versionErr != nil {
		add("artifact", "备份产物", "failed", versionErr.Error())
		return restorePrecheckResult(checks)
	}
	if !backupArtifactRestorable(restoreJob) {
		add("artifact", "备份产物", "failed", backupArtifactUnavailableMessage(restoreJob))
		return restorePrecheckResult(checks)
	}
	if _, err := s.restoreSource(restoreJob); err != nil {
		add("artifact", "备份产物", "failed", err.Error())
	} else {
		add("artifact", "备份产物", "passed", "已找到可恢复的备份产物："+restoreVersionLabel(restoreJob, plan.ArtifactVersionID))
	}
	if err := restoreClientToolAvailable(target.Type); err != nil {
		add("client_tool", "客户端工具", "failed", userFacingErrorMessage(errorContextBackupRuntime, err))
	} else {
		add("client_tool", "客户端工具", "passed", restoreClientToolLabel(target.Type)+" 可用")
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	if _, err := serverVersion(timeoutCtx, target); err != nil {
		add("target_connection", "目标连接", "failed", userFacingErrorMessage(errorContextConnectionTest, err))
		return restorePrecheckResult(checks)
	}
	add("target_connection", "目标连接", "passed", target.Name+" 连接可用")
	for _, sourceDatabase := range plan.SourceDatabases {
		targetDatabase := strings.TrimSpace(plan.TargetDatabases[sourceDatabase])
		if !validRestoreDatabaseName(targetDatabase) {
			add("database_name_"+sourceDatabase, "目标库名", "failed", fmt.Sprintf("目标数据库 %s 名称不合法", targetDatabase))
			continue
		}
		if plan.Strategy == "append" {
			if _, err := tables(timeoutCtx, target, targetDatabase); err != nil {
				add("target_database_"+sourceDatabase, "目标数据库", "failed", fmt.Sprintf("%s：%s", targetDatabase, userFacingErrorMessage(errorContextTableDiscovery, err)))
			} else {
				add("target_database_"+sourceDatabase, "目标数据库", "passed", targetDatabase+" 可访问")
			}
			continue
		}
		if err := checkRestoreRecreatePrivilege(timeoutCtx, target, targetDatabase); err != nil {
			add("recreate_"+sourceDatabase, "重建权限", "failed", fmt.Sprintf("%s：%s", targetDatabase, userFacingErrorMessage(errorContextBackupRuntime, err)))
		} else {
			add("recreate_"+sourceDatabase, "重建权限", "passed", targetDatabase+" 可执行重建前置检查")
		}
	}
	return restorePrecheckResult(checks)
}

func restorePrecheckResult(checks []model.RestorePrecheckItem) model.RestorePrecheckResult {
	result := model.RestorePrecheckResult{OK: true, Checks: checks}
	for _, check := range checks {
		if check.Status == "failed" {
			result.OK = false
			break
		}
		if check.Status == "warning" {
			result.Warnings = append(result.Warnings, check.Message)
		}
	}
	return result
}

func backupArtifactRestorable(job model.BackupJob) bool {
	if job.Status == model.StatusRunning || job.Status == model.StatusPending {
		return false
	}
	if strings.TrimSpace(job.ArtifactName) != "" {
		return true
	}
	for _, artifact := range job.Artifacts {
		if strings.TrimSpace(artifact.Name) != "" {
			return true
		}
	}
	return false
}

func backupArtifactUnavailableMessage(job model.BackupJob) string {
	if job.Status == model.StatusRunning || job.Status == model.StatusPending {
		return "任务正在执行，当前备份产物暂不可恢复"
	}
	return "只有已生成备份文件的任务可以恢复"
}

func backupJobForArtifactVersion(job model.BackupJob, versionID string) (model.BackupJob, error) {
	versionID = strings.TrimSpace(versionID)
	if versionID == "" || versionID == "current" {
		return job, nil
	}
	for _, version := range job.ArtifactVersions {
		if version.ID != versionID {
			continue
		}
		if version.Status != model.StatusCompleted {
			return model.BackupJob{}, fmt.Errorf("选择的备份版本未成功完成，不能用于恢复")
		}
		if len(version.Artifacts) == 0 {
			return model.BackupJob{}, fmt.Errorf("选择的备份版本没有可恢复产物")
		}
		next := job
		next.Artifacts = version.Artifacts
		next.ArtifactName = ""
		next.ArtifactSize = 0
		next.ArtifactPath = ""
		next.StartedAt = version.StartedAt
		next.FinishedAt = version.FinishedAt
		if len(version.Artifacts) > 0 {
			next.ArtifactName = version.Artifacts[0].Name
			next.ArtifactSize = version.Artifacts[0].Size
			next.ArtifactPath = version.Artifacts[0].Path
		}
		return next, nil
	}
	return model.BackupJob{}, fmt.Errorf("未找到选择的备份版本")
}

func restoreSourceDatabasesForJob(job model.BackupJob) []string {
	seen := map[string]bool{}
	var databases []string
	for _, artifact := range job.Artifacts {
		database := strings.TrimSpace(artifact.Database)
		if database != "" && !seen[database] {
			seen[database] = true
			databases = append(databases, database)
		}
	}
	if len(databases) > 0 {
		return databases
	}
	databases = backupDatabases(job.Request)
	if len(databases) > 0 {
		return databases
	}
	return splitDatabaseList(job.SourceDatabase)
}

func restoreVersionLabel(job model.BackupJob, versionID string) string {
	if strings.TrimSpace(versionID) == "" || versionID == "current" {
		if job.FinishedAt != nil {
			return job.FinishedAt.In(appLocation).Format("2006-01-02 15:04:05")
		}
		return "当前最新产物"
	}
	if job.FinishedAt != nil {
		return job.FinishedAt.In(appLocation).Format("2006-01-02 15:04:05")
	}
	return versionID
}

func validRestoreDatabaseName(name string) bool {
	if name == "" || len(name) > 63 {
		return false
	}
	for _, value := range name {
		if value < 32 || value == 127 || value == '/' || value == '\\' || value == '\x00' {
			return false
		}
	}
	return true
}

func restoreClientToolAvailable(databaseType string) error {
	switch databaseType {
	case "PostgreSQL":
		_, err := backup.ToolPathForPrecheck("psql")
		return err
	case "MySQL":
		_, err := backup.ToolPathForPrecheck("mysql")
		return err
	default:
		return fmt.Errorf("unsupported restore target type %q", databaseType)
	}
}

func restoreClientToolLabel(databaseType string) string {
	if databaseType == "MySQL" {
		return "mysql"
	}
	return "psql"
}

func checkRestoreRecreatePrivilege(ctx context.Context, target model.Connection, database string) error {
	switch target.Type {
	case "PostgreSQL":
		maintenanceDatabase := "postgres"
		if strings.EqualFold(strings.TrimSpace(database), maintenanceDatabase) {
			maintenanceDatabase = "template1"
		}
		conn, err := postgres.Connect(ctx, target, maintenanceDatabase)
		if err != nil {
			return err
		}
		defer conn.Close(ctx)
		var canCreate bool
		if err := conn.QueryRow(ctx, `SELECT rolsuper OR rolcreatedb FROM pg_roles WHERE rolname = current_user`).Scan(&canCreate); err != nil {
			return err
		}
		if !canCreate {
			return fmt.Errorf("permission denied: current PostgreSQL user does not have CREATEDB privilege")
		}
		var canDrop bool
		if err := conn.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1) OR EXISTS (
			SELECT 1 FROM pg_database d JOIN pg_roles r ON r.oid = d.datdba
			WHERE d.datname = $1 AND (r.rolname = current_user OR (SELECT rolsuper FROM pg_roles WHERE rolname = current_user))
		)`, database).Scan(&canDrop); err != nil {
			return err
		}
		if !canDrop {
			return fmt.Errorf("permission denied: current PostgreSQL user is not owner of target database %s", database)
		}
		return nil
	case "MySQL":
		conn, err := mysql.Connect(ctx, target, "")
		if err != nil {
			return err
		}
		defer conn.Close()
		rows, err := conn.QueryContext(ctx, `SHOW GRANTS`)
		if err != nil {
			return err
		}
		defer rows.Close()
		grants := ""
		for rows.Next() {
			var grant string
			if err := rows.Scan(&grant); err != nil {
				return err
			}
			grants += strings.ToUpper(grant) + "\n"
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if strings.Contains(grants, "ALL PRIVILEGES") || (strings.Contains(grants, "CREATE") && strings.Contains(grants, "DROP")) {
			return nil
		}
		return fmt.Errorf("permission denied: current MySQL user does not have CREATE and DROP privileges")
	default:
		return fmt.Errorf("unsupported restore target type %q", target.Type)
	}
}

type restoreSource struct {
	Type      string
	Path      string
	Object    string
	Storage   *model.RemoteStorage
	Artifacts map[string]model.BackupArtifact
}

func (s *Server) restoreSource(job model.BackupJob) (restoreSource, error) {
	switch job.DestinationType {
	case "local":
		artifactPath := restoreArtifactPath(job)
		if artifactPath == "" {
			return restoreSource{}, fmt.Errorf("未找到本地备份文件路径")
		}
		if stat, err := os.Stat(artifactPath); err != nil || stat.IsDir() {
			return restoreSource{}, fmt.Errorf("本地备份文件不存在，请检查备份目录")
		}
		return restoreSource{Type: "local", Path: artifactPath, Artifacts: backupArtifactMap(job)}, nil
	case "object":
		request, err := s.backfillBackupRequest(job)
		if err != nil {
			return restoreSource{}, err
		}
		objectName := restoreArtifactObject(job)
		if request.Destination == "" || objectName == "" {
			return restoreSource{}, fmt.Errorf("未找到对象存储备份位置")
		}
		storage, err := s.store.Storage(request.Destination)
		if err != nil {
			return restoreSource{}, fmt.Errorf("未找到备份目标对象存储")
		}
		return restoreSource{Type: "object", Object: objectName, Storage: &storage, Artifacts: backupArtifactMap(job)}, nil
	default:
		return restoreSource{}, fmt.Errorf("该备份目标暂不支持恢复")
	}
}

func restoreArtifactObject(job model.BackupJob) string {
	if job.ArtifactName != "" {
		return job.ArtifactName
	}
	for _, artifact := range job.Artifacts {
		if strings.TrimSpace(artifact.Name) != "" {
			return artifact.Name
		}
	}
	return ""
}

func restoreArtifactPath(job model.BackupJob) string {
	if job.ArtifactPath != "" {
		return job.ArtifactPath
	}
	for _, artifact := range job.Artifacts {
		if strings.TrimSpace(artifact.Path) != "" {
			return artifact.Path
		}
	}
	if job.DestinationType == "local" && job.Request.Destination != "" && job.ArtifactName != "" {
		return filepath.Join(job.Request.Destination, filepath.FromSlash(job.ArtifactName))
	}
	for _, artifact := range job.Artifacts {
		if job.DestinationType == "local" && job.Request.Destination != "" && strings.TrimSpace(artifact.Name) != "" {
			return filepath.Join(job.Request.Destination, filepath.FromSlash(artifact.Name))
		}
	}
	return ""
}

func restoreArtifactSummary(job model.BackupJob) model.BackupArtifact {
	if job.ArtifactName != "" {
		return model.BackupArtifact{Name: job.ArtifactName, Size: job.ArtifactSize, Path: job.ArtifactPath}
	}
	for _, artifact := range job.Artifacts {
		if strings.TrimSpace(artifact.Name) != "" {
			return artifact
		}
	}
	return model.BackupArtifact{}
}

func backupArtifactMap(job model.BackupJob) map[string]model.BackupArtifact {
	items := map[string]model.BackupArtifact{}
	for _, artifact := range job.Artifacts {
		if strings.TrimSpace(artifact.Database) != "" && strings.TrimSpace(artifact.Name) != "" {
			items[artifact.Database] = artifact
		}
	}
	return items
}

func (s *Server) runRestore(ctx context.Context, id string, target model.Connection, sourceDatabases []string, targetDatabases map[string]string, strategy string, source restoreSource) {
	log.Printf("restore[%s] started: %s -> %s/%s", id, restoreSourceLabel(source), target.Name, restoreTargetLabel(sourceDatabases, targetDatabases))
	started := time.Now()
	_ = s.store.UpdateBackup(id, func(item *model.BackupJob) {
		item.Status = model.StatusRunning
		item.Stage = "restore"
		item.Progress = 15
		item.StartedAt = started
		item.LastRunAt = &started
		item.FinishedAt = nil
		item.Error = ""
	})
	reporter := func(progress backup.Progress) {
		_ = s.store.UpdateBackup(id, func(item *model.BackupJob) {
			item.Stage = progress.Stage
			item.Progress = backupProgress(progress.Stage, progress.Progress)
			if progress.EventMessage != "" {
				item.Events = append(item.Events, model.Event{Time: time.Now(), Level: progress.EventLevel, Message: progress.EventMessage})
				if len(item.Events) > 100 {
					item.Events = item.Events[len(item.Events)-100:]
				}
			}
		})
	}
	var err error
	for index, sourceDatabase := range sourceDatabases {
		if err == nil {
			targetDatabase := targetDatabases[sourceDatabase]
			reporter(backup.Progress{Stage: "restore", EventLevel: "INFO", EventMessage: fmt.Sprintf("开始恢复数据库 %s 到 %s（%d/%d）", sourceDatabase, targetDatabase, index+1, len(sourceDatabases)), Progress: 20 + index*70/len(sourceDatabases)})
			artifactPath, cleanup, prepareErr := s.prepareRestoreArtifact(ctx, restoreArtifactForDatabase(source, sourceDatabase), reporter)
			if prepareErr != nil {
				err = prepareErr
				if cleanup != nil {
					cleanup()
				}
				break
			}
			if restoreErr := s.backup.Restore(ctx, target, targetDatabase, artifactPathForRestoreDatabase(artifactPath, sourceDatabase), backup.RestoreOptions{Strategy: strategy}, func(progress backup.Progress) {
				if len(sourceDatabases) > 1 {
					progress.Progress = 20 + index*70/len(sourceDatabases) + backupProgress(progress.Stage, progress.Progress)/len(sourceDatabases)
				}
				reporter(progress)
			}); restoreErr != nil {
				err = restoreErr
			}
			if cleanup != nil {
				cleanup()
			}
		}
	}
	if err == nil {
		reporter(backup.Progress{Stage: "validation", EventLevel: "INFO", EventMessage: "正在校验恢复后的目标库"})
		for _, sourceDatabase := range sourceDatabases {
			targetDatabase := targetDatabases[sourceDatabase]
			if tables, tableErr := tables(ctx, target, targetDatabase); tableErr != nil {
				err = fmt.Errorf("restore validation: %w", tableErr)
				break
			} else {
				reporter(backup.Progress{Stage: "validation", EventLevel: "INFO", EventMessage: fmt.Sprintf("恢复校验完成，目标库 %s 发现 %d 张普通表", targetDatabase, len(tables)), Progress: 95})
			}
		}
	}
	finished := time.Now()
	_ = s.store.UpdateBackup(id, func(item *model.BackupJob) {
		item.FinishedAt = &finished
		if err == nil {
			item.Status = model.StatusCompleted
			item.Stage = "completed"
			item.Progress = 100
			item.Events = append(item.Events, model.Event{Time: finished, Level: "INFO", Message: "恢复任务完成"})
			return
		}
		if errors.Is(err, context.Canceled) {
			item.Status = model.StatusCancelled
			item.Stage = "cancelled"
			item.Events = append(item.Events, model.Event{Time: finished, Level: "WARN", Message: "恢复任务已取消"})
		} else {
			item.Status = model.StatusFailed
			item.Stage = "failed"
			item.Error = userFacingErrorMessage(errorContextBackupRuntime, err)
			item.Events = append(item.Events, model.Event{Time: finished, Level: "ERROR", Message: err.Error()})
		}
	})
}

func restoreSourceLabel(source restoreSource) string {
	if source.Type == "object" {
		return "object/" + source.Object
	}
	return "local/" + source.Path
}

func restoreArtifactForDatabase(source restoreSource, database string) restoreSource {
	artifact, ok := source.Artifacts[database]
	if !ok {
		return source
	}
	next := source
	if source.Type == "object" && artifact.Name != "" {
		next.Object = artifact.Name
	}
	if source.Type == "local" && artifact.Path != "" {
		next.Path = artifact.Path
	}
	return next
}

func (s *Server) prepareRestoreArtifact(ctx context.Context, source restoreSource, reporter func(backup.Progress)) (string, func(), error) {
	if source.Type == "local" {
		return source.Path, nil, nil
	}
	if source.Type != "object" || source.Storage == nil {
		return "", nil, fmt.Errorf("unsupported restore source %q", source.Type)
	}
	tempDir, err := os.MkdirTemp("", "databridge-restore-artifact-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tempDir) }
	targetPath := filepath.Join(tempDir, filepath.Base(source.Object))
	reporter(backup.Progress{Stage: "download", EventLevel: "INFO", EventMessage: "正在从对象存储下载备份文件"})
	size, err := remotestorage.DownloadObject(ctx, *source.Storage, source.Object, targetPath)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	reporter(backup.Progress{Stage: "download", EventLevel: "INFO", EventMessage: fmt.Sprintf("备份文件已下载到临时目录，大小 %s", formatBytes(size)), Progress: 20})
	return targetPath, cleanup, nil
}

func (s *Server) downloadBackupArtifact(w http.ResponseWriter, r *http.Request) {
	item, artifact, err := s.backupArtifactForRequest(r)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	fileName := filepath.Base(artifact.Name)
	if fileName == "." || fileName == string(filepath.Separator) || fileName == "" {
		fileName = "databridge-backup.tar.gz"
	}
	switch item.DestinationType {
	case "local":
		path := artifact.Path
		if path == "" {
			path = restoreArtifactPath(item)
		}
		file, err := os.Open(path)
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found", "本地备份文件不存在，请检查备份目录")
			return
		}
		defer file.Close()
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, sanitizeDownloadFileName(fileName)))
		if stat, err := file.Stat(); err == nil && !stat.IsDir() {
			w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
		}
		if _, err := io.Copy(w, file); err != nil {
			log.Printf("backup artifact download failed: %v", err)
		}
	case "object":
		storage, err := s.backupArtifactStorage(item)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_artifact", err.Error())
			return
		}
		tempDir, err := os.MkdirTemp("", "databridge-artifact-download-*")
		if err != nil {
			writeError(w, http.StatusInternalServerError, "download_failed", "准备下载备份文件失败")
			return
		}
		defer os.RemoveAll(tempDir)
		tempPath := filepath.Join(tempDir, filepath.Base(artifact.Name))
		size, err := remotestorage.DownloadObject(r.Context(), storage, artifact.Name, tempPath)
		if err != nil {
			writeError(w, http.StatusBadRequest, "download_failed", userFacingErrorMessage(errorContextBackupRuntime, err))
			return
		}
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, sanitizeDownloadFileName(fileName)))
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		http.ServeFile(w, r, tempPath)
	default:
		writeError(w, http.StatusBadRequest, "invalid_artifact", "该备份目标暂不支持下载")
	}
}

func (s *Server) deleteBackupArtifact(w http.ResponseWriter, r *http.Request) {
	item, artifact, err := s.backupArtifactForRequest(r)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	if item.Status == model.StatusRunning || item.Status == model.StatusPending {
		writeError(w, http.StatusConflict, "task_running", "任务正在执行，请先停止后再删除备份文件")
		return
	}
	switch item.DestinationType {
	case "local":
		path := artifact.Path
		if path == "" {
			path = restoreArtifactPath(item)
		}
		if path == "" {
			writeError(w, http.StatusBadRequest, "invalid_artifact", "未找到本地备份文件路径")
			return
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusBadRequest, "delete_failed", userFacingErrorMessage(errorContextBackupRuntime, err))
			return
		}
	case "object":
		storage, err := s.backupArtifactStorage(item)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_artifact", err.Error())
			return
		}
		if err := remotestorage.DeleteObject(r.Context(), storage, artifact.Name); err != nil {
			writeError(w, http.StatusBadRequest, "delete_failed", userFacingErrorMessage(errorContextBackupRuntime, err))
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "invalid_artifact", "该备份目标暂不支持删除备份文件")
		return
	}
	now := time.Now().In(appLocation)
	if err := s.store.UpdateBackup(item.ID, func(job *model.BackupJob) {
		removeBackupArtifact(job, artifact.Database)
		job.Events = append(job.Events, model.Event{Time: now, Level: "WARN", Message: "备份文件已删除：" + artifact.Name})
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextCreateBackup, err))
		return
	}
	updated, err := s.store.Backup(item.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextCreateBackup, err))
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) backupArtifactForRequest(r *http.Request) (model.BackupJob, model.BackupArtifact, error) {
	item, err := s.store.Backup(r.PathValue("id"))
	if err != nil {
		return model.BackupJob{}, model.BackupArtifact{}, fmt.Errorf("backup not found")
	}
	versionID := strings.TrimSpace(r.URL.Query().Get("artifactVersionId"))
	if versionID != "" {
		item, err = backupJobForArtifactVersion(item, versionID)
		if err != nil {
			return model.BackupJob{}, model.BackupArtifact{}, err
		}
	}
	database := strings.TrimSpace(r.PathValue("database"))
	for _, artifact := range backupArtifactsForDisplay(item) {
		if artifact.Database == database {
			return item, artifact, nil
		}
	}
	return model.BackupJob{}, model.BackupArtifact{}, fmt.Errorf("backup artifact not found")
}

func (s *Server) backupArtifactStorage(item model.BackupJob) (model.RemoteStorage, error) {
	request, err := s.backfillBackupRequest(item)
	if err != nil {
		return model.RemoteStorage{}, err
	}
	if request.Destination == "" {
		return model.RemoteStorage{}, fmt.Errorf("未找到对象存储备份位置")
	}
	storage, err := s.store.Storage(request.Destination)
	if err != nil {
		return model.RemoteStorage{}, fmt.Errorf("未找到备份目标对象存储")
	}
	return storage, nil
}

func backupArtifactsForDisplay(job model.BackupJob) []model.BackupArtifact {
	if len(job.Artifacts) > 0 {
		return job.Artifacts
	}
	if job.ArtifactName == "" {
		return nil
	}
	database := strings.TrimSpace(job.SourceDatabase)
	if database == "" {
		database = "default"
	}
	return []model.BackupArtifact{{Database: database, Name: job.ArtifactName, Size: job.ArtifactSize, Path: restoreArtifactPath(job)}}
}

func removeBackupArtifact(job *model.BackupJob, database string) {
	if len(job.Artifacts) > 0 {
		next := job.Artifacts[:0]
		for _, artifact := range job.Artifacts {
			if artifact.Database != database {
				next = append(next, artifact)
			}
		}
		job.Artifacts = next
	}
	if len(job.Artifacts) > 0 {
		job.ArtifactName = job.Artifacts[0].Name
		job.ArtifactSize = job.Artifacts[0].Size
		job.ArtifactPath = job.Artifacts[0].Path
		return
	}
	if job.Artifacts == nil || job.SourceDatabase == database || job.ArtifactName != "" {
		job.ArtifactName = ""
		job.ArtifactSize = 0
		job.ArtifactPath = ""
	}
}

func sanitizeDownloadFileName(value string) string {
	value = strings.ReplaceAll(value, `"`, "")
	value = strings.ReplaceAll(value, "\\", "_")
	value = strings.ReplaceAll(value, "/", "_")
	if strings.TrimSpace(value) == "" {
		return "databridge-backup.tar.gz"
	}
	return value
}

func (s *Server) cancelBackup(w http.ResponseWriter, r *http.Request) {
	if err := s.store.CancelBackup(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "backup not found")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (s *Server) deleteBackup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	item, err := s.store.Backup(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "backup not found")
		return
	}
	if item.Status == model.StatusRunning || item.Status == model.StatusPending {
		writeError(w, http.StatusConflict, "task_running", "任务正在执行，请先停止后再删除")
		return
	}
	if err := s.store.DeleteBackup(id); err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextCreateBackup, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) listBackups(w http.ResponseWriter, _ *http.Request) {
	s.reconcileScheduledBackupRuns()
	items, err := s.store.Backups()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextCreateBackup, err))
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) getSMTPSettings(w http.ResponseWriter, _ *http.Request) {
	settings, err := s.store.SMTPSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextCreateBackup, err))
		return
	}
	settings.Password = ""
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) updateSMTPSettings(w http.ResponseWriter, r *http.Request) {
	var input model.SMTPSettings
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Host = strings.TrimSpace(input.Host)
	input.Username = strings.TrimSpace(input.Username)
	input.From = strings.TrimSpace(input.From)
	if input.Enabled {
		if input.Host == "" || input.Port <= 0 || input.From == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "请完整填写 SMTP 主机、端口和发件人")
			return
		}
	}
	settings, err := s.store.UpdateSMTPSettings(input)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextCreateBackup, err))
		return
	}
	settings.Password = ""
	writeJSON(w, http.StatusOK, settings)
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

func validateStorage(w http.ResponseWriter, input model.StorageInput) bool {
	storageType := normalizeStorageType(input.Type)
	if storageType == "" {
		writeError(w, http.StatusBadRequest, "invalid_storage", "请选择远端存储类型")
		return false
	}
	if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Provider) == "" {
		writeError(w, http.StatusBadRequest, "invalid_storage", "存储名称和服务商不能为空")
		return false
	}
	if storageType == "object" && (strings.TrimSpace(input.Endpoint) == "" || strings.TrimSpace(input.Bucket) == "" || strings.TrimSpace(input.AccessKey) == "" || input.SecretKey == "") {
		writeError(w, http.StatusBadRequest, "invalid_storage", "对象存储需要填写 Endpoint、Bucket、Access Key 和 Secret Key")
		return false
	}
	return true
}

func validateStorageUpdate(w http.ResponseWriter, input model.UpdateStorageInput, currentType string) bool {
	storageType := normalizeStorageType(nonEmpty(input.Type, currentType))
	if storageType == "" {
		writeError(w, http.StatusBadRequest, "invalid_storage", "请选择远端存储类型")
		return false
	}
	if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Provider) == "" {
		writeError(w, http.StatusBadRequest, "invalid_storage", "存储名称和服务商不能为空")
		return false
	}
	if storageType == "object" && (strings.TrimSpace(input.Endpoint) == "" || strings.TrimSpace(input.Bucket) == "" || strings.TrimSpace(input.AccessKey) == "") {
		writeError(w, http.StatusBadRequest, "invalid_storage", "对象存储需要填写 Endpoint、Bucket 和 Access Key")
		return false
	}
	return true
}

func normalizeStorageType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "object", "object_storage", "object-storage", "s3":
		return "object"
	case "google_drive", "google-drive", "gdrive":
		return "google_drive"
	case "one_drive", "one-drive", "onedrive":
		return "one_drive"
	case "local":
		return "local"
	default:
		return ""
	}
}

func redactStorageSecrets(items []model.RemoteStorage) []model.RemoteStorage {
	for index := range items {
		items[index] = redactStorageSecret(items[index])
	}
	return items
}

func redactStorageSecret(item model.RemoteStorage) model.RemoteStorage {
	if item.SecretKey != "" {
		item.SecretKey = ""
	}
	return item
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
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
	case strings.Contains(lower, "signaturedoesnotmatch"), strings.Contains(lower, "invalidaccesskeyid"), strings.Contains(lower, "accessdenied"):
		if context == errorContextRemoteStorage {
			return "对象存储认证失败，请确认 Access Key、Secret Key、Region 和 Bucket 权限是否正确"
		}
	case strings.Contains(lower, "nosuchbucket"), strings.Contains(lower, "bucket"):
		if context == errorContextRemoteStorage {
			return "对象存储 Bucket 不存在或当前账号无权访问，请确认 Bucket 名称和授权策略"
		}
	case strings.Contains(lower, "endpoint must include"), strings.Contains(lower, "invalid endpoint"):
		if context == errorContextRemoteStorage {
			return "对象存储 Endpoint 格式不正确，请填写完整地址，例如 https://s3.example.com"
		}
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
	case errorContextCreateBackup:
		return "创建备份任务失败，请确认源端数据库和备份目标是否正确"
	case errorContextBackupRuntime:
		return "备份执行失败，请查看任务日志并确认数据库权限、客户端工具和存储目标状态"
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

type loggingResponseWriter struct {
	http.ResponseWriter
	status      int
	bytes       int
	body        []byte
	wroteHeader bool
}

func (w *loggingResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *loggingResponseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	w.bytes += len(data)
	if len(w.body) < 4096 {
		remaining := 4096 - len(w.body)
		if remaining > len(data) {
			remaining = len(data)
		}
		w.body = append(w.body, data[:remaining]...)
	}
	return w.ResponseWriter.Write(data)
}

func (w *loggingResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

type responseErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiBlue   = "\033[34m"
	ansiCyan   = "\033[36m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiRed    = "\033[31m"
	ansiGray   = "\033[90m"
)

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &loggingResponseWriter{ResponseWriter: w, status: http.StatusOK}
		startedAt := time.Now()
		next.ServeHTTP(recorder, r)

		if !strings.HasPrefix(r.URL.Path, "/api/") || s.shouldSkipRequestLog(r.URL.Path) {
			return
		}

		duration := time.Since(startedAt)
		clientIP := clientAddress(r)
		var payload responseErrorPayload
		if len(recorder.body) > 0 {
			_ = json.Unmarshal(recorder.body, &payload)
		}

		s.logAPIRequest(r, recorder.status, duration, clientIP, payload, recorder.bytes)
	})
}

func (s *Server) shouldSkipRequestLog(path string) bool {
	return path == "/api/healthz"
}

func clientAddress(r *http.Request) string {
	if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); forwarded != "" {
		return forwarded
	}
	if realIP := strings.TrimSpace(r.Header.Get("X-Real-Ip")); realIP != "" {
		return realIP
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	if strings.TrimSpace(r.RemoteAddr) != "" {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return "unknown"
}

func (s *Server) logAPIRequest(r *http.Request, status int, duration time.Duration, clientIP string, payload responseErrorPayload, responseBytes int) {
	message := fmt.Sprintf("%s %s -> %d", r.Method, r.URL.Path, status)
	detailParts := []string{
		"method=" + r.Method,
		"path=" + r.URL.Path,
		"status=" + strconv.Itoa(status),
		"duration=" + duration.String(),
		"client=" + clientIP,
		"response_bytes=" + strconv.Itoa(responseBytes),
	}
	if query := strings.TrimSpace(r.URL.RawQuery); query != "" {
		detailParts = append(detailParts, "query="+query)
	}
	if payload.Code != "" {
		detailParts = append(detailParts, "error_code="+payload.Code)
	}
	if payload.Message != "" {
		detailParts = append(detailParts, "error_message="+sanitizeLogValue(payload.Message))
	}
	detail := strings.Join(detailParts, " ")

	level := "INFO"
	action := "api_request"
	result := "success"
	if status >= http.StatusBadRequest {
		level = "ERROR"
		result = "failed"
	}

	log.Printf("%s", formatAPIConsoleLog(r, status, duration, clientIP, payload, responseBytes))
	if err := s.store.AddAuditLog(store.AuditLog{
		Level:        level,
		Action:       action,
		ResourceType: "http_request",
		ResourceID:   r.URL.Path,
		ResourceName: r.Method + " " + r.URL.Path,
		Status:       result,
		Message:      message,
		Detail:       detail,
	}); err != nil {
		log.Printf("api request audit persistence failed: %v", err)
	}
}

func sanitizeLogValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\t", " ")
	return value
}

func formatAPIConsoleLog(r *http.Request, status int, duration time.Duration, clientIP string, payload responseErrorPayload, responseBytes int) string {
	methodColor := ansiBlue
	switch r.Method {
	case http.MethodPost:
		methodColor = ansiCyan
	case http.MethodPut:
		methodColor = ansiYellow
	case http.MethodDelete:
		methodColor = ansiRed
	}

	statusColor := ansiGreen
	switch {
	case status >= 500:
		statusColor = ansiRed
	case status >= 400:
		statusColor = ansiYellow
	case status >= 300:
		statusColor = ansiBlue
	}

	durationColor := ansiGreen
	if duration >= time.Second {
		durationColor = ansiRed
	} else if duration >= 200*time.Millisecond {
		durationColor = ansiYellow
	}

	parts := []string{
		ansiGray + "api" + ansiReset,
		methodColor + ansiBold + padHTTPMethod(r.Method) + ansiReset,
		ansiBold + r.URL.Path + ansiReset,
		statusColor + ansiBold + strconv.Itoa(status) + ansiReset,
		durationColor + duration.String() + ansiReset,
		ansiGray + "client=" + clientIP + ansiReset,
		ansiGray + "bytes=" + strconv.Itoa(responseBytes) + ansiReset,
	}
	if query := strings.TrimSpace(r.URL.RawQuery); query != "" {
		parts = append(parts, ansiGray+"query="+query+ansiReset)
	}
	if payload.Code != "" {
		parts = append(parts, statusColor+"code="+payload.Code+ansiReset)
	}
	if payload.Message != "" {
		parts = append(parts, statusColor+"message="+sanitizeLogValue(payload.Message)+ansiReset)
	}
	return strings.Join(parts, " ")
}

func padHTTPMethod(method string) string {
	switch len(method) {
	case 3:
		return method + "   "
	case 4:
		return method + "  "
	case 5:
		return method + " "
	default:
		return method
	}
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
