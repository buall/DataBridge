package api

import (
	"databridge/internal/backup"
	"databridge/internal/migrate"
	"databridge/internal/store"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
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

const (
	maxStoredEvents       = 100
	maxCompactEvents      = 30
	maxStoredEventMessage = 4096
)

const (
	mongoOplogBackupMode               = "mongo_oplog"
	mongoReplicaSetScope               = "整个副本集"
	defaultMongoFullBackupIntervalDays = 7
	minimumMongoOplogWindowHours       = 48
	mongoOplogSafetyBufferHours        = 12
)

type Server struct {
	store        *store.Store
	engine       *migrate.Engine
	backup       *backup.Engine
	geoClient    *http.Client
	geoMu        sync.Mutex
	geoCache     map[string]string
	auth         authConfig
	loginLimiter *loginRateLimiter
	captcha      *captchaManager
	jobSem       chan struct{}
	oauthMu      sync.Mutex
	oauth        map[string]oneDriveOAuthState
}

type oneDriveOAuthState struct {
	StorageID string
	ExpiresAt time.Time
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

func maxConcurrentJobs() int {
	raw := strings.TrimSpace(os.Getenv("DATABRIDGE_MAX_CONCURRENT_JOBS"))
	if raw == "" {
		return 4
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 4
	}
	if n > 32 {
		return 32
	}
	return n
}

func New(appStore *store.Store) http.Handler {
	server := &Server{
		store:        appStore,
		engine:       migrate.New(),
		backup:       backup.New(),
		geoClient:    &http.Client{Timeout: 2 * time.Second},
		geoCache:     make(map[string]string),
		auth:         loadAuthConfig(),
		loginLimiter: newLoginRateLimiter(10, time.Minute),
		captcha:      newCaptchaManager(),
		jobSem:       make(chan struct{}, maxConcurrentJobs()),
		oauth:        make(map[string]oneDriveOAuthState),
	}
	server.reconcileScheduledBackupRuns()
	server.startBackupScheduler()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/healthz", server.health)
	mux.HandleFunc("GET /api/auth/session", server.authSession)
	mux.HandleFunc("GET /api/auth/captcha", server.authCaptcha)
	mux.HandleFunc("POST /api/auth/login", server.login)
	mux.HandleFunc("POST /api/auth/logout", server.logout)
	// OAuth providers redirect back without DataBridge's session context. The
	// single-use, expiring state token protects this callback from CSRF.
	mux.HandleFunc("GET /api/storages/{id}/onedrive/callback", server.oneDriveCallback)
	protected := http.NewServeMux()
	protected.HandleFunc("GET /api/connections", server.listConnections)
	protected.HandleFunc("POST /api/imports/excel/preview", server.previewBatchImport)
	protected.HandleFunc("POST /api/imports/excel", server.executeBatchImport)
	protected.HandleFunc("GET /api/exports/excel", server.exportExcel)
	protected.HandleFunc("POST /api/connections/test", server.testConnection)
	protected.HandleFunc("POST /api/connections", server.createConnection)
	protected.HandleFunc("PUT /api/connections/{id}", server.updateConnection)
	protected.HandleFunc("PUT /api/connections/{id}/favorite", server.updateConnectionFavorite)
	protected.HandleFunc("DELETE /api/connections/{id}", server.deleteConnection)
	protected.HandleFunc("POST /api/connections/{id}/duplicate", server.duplicateConnection)
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
	protected.HandleFunc("POST /api/storages/webdav/directories", server.listWebDAVDirectories)
	protected.HandleFunc("POST /api/storages/{id}/webdav/directories", server.listStoredWebDAVDirectories)
	protected.HandleFunc("POST /api/storages/{id}/webdav/list", server.listWebDAVDirectory)
	protected.HandleFunc("POST /api/storages/{id}/webdav/directories/create", server.createWebDAVDirectory)
	protected.HandleFunc("PUT /api/storages/{id}", server.updateStorage)
	protected.HandleFunc("PUT /api/storages/{id}/favorite", server.updateStorageFavorite)
	protected.HandleFunc("DELETE /api/storages/{id}", server.deleteStorage)
	protected.HandleFunc("POST /api/storages/{id}/test", server.testStorage)
	protected.HandleFunc("POST /api/storages/{id}/onedrive/authorize", server.authorizeOneDrive)
	protected.HandleFunc("POST /api/migrations", server.createMigration)
	protected.HandleFunc("GET /api/migrations/{id}", server.getMigration)
	protected.HandleFunc("POST /api/migrations/{id}/cancel", server.cancelMigration)
	protected.HandleFunc("DELETE /api/migrations/{id}", server.deleteMigration)
	protected.HandleFunc("PUT /api/migrations/{id}/favorite", server.updateMigrationFavorite)
	protected.HandleFunc("GET /api/migrations", server.listMigrations)
	protected.HandleFunc("GET /api/logs", server.listLogs)
	protected.HandleFunc("GET /api/settings/smtp", server.getSMTPSettings)
	protected.HandleFunc("PUT /api/settings/smtp", server.updateSMTPSettings)
	protected.HandleFunc("GET /api/settings/notification", server.getNotificationSettings)
	protected.HandleFunc("PUT /api/settings/notification", server.updateNotificationSettings)
	protected.HandleFunc("GET /api/settings/notification-recipients", server.listNotificationRecipients)
	protected.HandleFunc("POST /api/settings/notification-recipients", server.createNotificationRecipient)
	protected.HandleFunc("POST /api/settings/notification-recipients/test", server.testNotificationRecipientInput)
	protected.HandleFunc("PUT /api/settings/notification-recipients/{id}", server.updateNotificationRecipient)
	protected.HandleFunc("DELETE /api/settings/notification-recipients/{id}", server.deleteNotificationRecipient)
	protected.HandleFunc("POST /api/settings/notification-recipients/{id}/test", server.testNotificationRecipient)
	protected.HandleFunc("POST /api/backups", server.createBackup)
	protected.HandleFunc("POST /api/backups/notification-test", server.testBackupNotification)
	protected.HandleFunc("GET /api/backups/{id}", server.getBackup)
	protected.HandleFunc("GET /api/backups/{id}/records/export", server.exportBackupRecords)
	protected.HandleFunc("PUT /api/backups/{id}", server.updateBackup)
	protected.HandleFunc("PUT /api/backups/{id}/favorite", server.updateBackupFavorite)
	protected.HandleFunc("POST /api/backups/{id}/trigger", server.triggerBackup)
	protected.HandleFunc("POST /api/backups/{id}/pause", server.pauseBackupSchedule)
	protected.HandleFunc("POST /api/backups/{id}/resume", server.resumeBackupSchedule)
	protected.HandleFunc("POST /api/backups/{id}/restore/precheck", server.restoreBackupPrecheck)
	protected.HandleFunc("POST /api/backups/{id}/restore", server.restoreBackup)
	protected.HandleFunc("POST /api/backups/{id}/cancel", server.cancelBackup)
	protected.HandleFunc("GET /api/backups/{id}/artifacts/{database}/download", server.downloadBackupArtifact)
	protected.HandleFunc("DELETE /api/backups/{id}/artifacts/{database}", server.deleteBackupArtifact)
	protected.HandleFunc("DELETE /api/backups/{id}", server.deleteBackup)
	protected.HandleFunc("GET /api/backups", server.listBackups)
	mux.Handle("/api/", server.auth.authMiddleware(protected))
	mux.Handle("/", staticHandler())
	return recoverMiddleware(securityHeadersMiddleware(server.loggingMiddleware(corsMiddleware(mux))))
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
		"authenticated":   authenticated,
		"username":        s.auth.username,
		"configured":      s.auth.enabled(),
		"captchaRequired": s.captcha.Required(clientIP(r)),
	})
}

func (s *Server) authCaptcha(w http.ResponseWriter, _ *http.Request) {
	id, image, err := s.captcha.Create()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "captcha_error", "生成图形验证码失败，请稍后重试")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"id":    id,
		"image": image,
	})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !s.loginLimiter.Allow(ip) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "登录尝试过于频繁，请稍后再试")
		return
	}
	var input struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		CaptchaID   string `json:"captchaId"`
		CaptchaCode string `json:"captchaCode"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if s.captcha.Required(ip) {
		if strings.TrimSpace(input.CaptchaID) == "" || strings.TrimSpace(input.CaptchaCode) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"code":            "captcha_required",
				"message":         "请输入图形验证码",
				"captchaRequired": true,
			})
			return
		}
		if !s.captcha.Verify(input.CaptchaID, input.CaptchaCode) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"code":            "invalid_captcha",
				"message":         "图形验证码错误或已过期，请重新输入",
				"captchaRequired": true,
			})
			return
		}
	}
	if !s.auth.checkPassword(strings.TrimSpace(input.Username), input.Password) {
		failures := s.captcha.RecordFailure(ip)
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"code":            "invalid_credentials",
			"message":         "登录失败，请检查用户名和密码",
			"captchaRequired": failures >= captchaFailureThreshold,
		})
		return
	}
	s.loginLimiter.Reset(ip)
	s.captcha.ResetFailures(ip)
	s.auth.setSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "username": s.auth.username})
}

func (s *Server) logout(w http.ResponseWriter, _ *http.Request) {
	s.auth.clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func userFacingErrorMessage(context errorContext, err error) string {
	if err == nil {
		return ""
	}
	raw := strings.TrimSpace(err.Error())
	lower := strings.ToLower(raw)

	if context == errorContextRemoteStorage {
		switch {
		case strings.Contains(lower, "connection refused"):
			return "无法连接到对象存储 Endpoint，请确认地址、端口和网络访问策略是否正确"
		case strings.Contains(lower, "i/o timeout"), strings.Contains(lower, "deadline exceeded"), strings.Contains(lower, "timeout"):
			return "连接对象存储超时，请确认 Endpoint、网络连通性、防火墙规则和对象存储服务状态"
		case strings.Contains(lower, "no such host"), strings.Contains(lower, "lookup "):
			return "无法解析对象存储 Endpoint，请确认域名或 IP 地址是否正确"
		case strings.Contains(lower, "signaturedoesnotmatch"), strings.Contains(lower, "invalidaccesskeyid"), strings.Contains(lower, "accessdenied"), strings.Contains(lower, "access denied"):
			return "对象存储认证失败，请确认 Access Key、Secret Key、Region 和 Bucket 权限是否正确"
		case strings.Contains(lower, "nosuchbucket"), strings.Contains(lower, "bucket"):
			return "对象存储 Bucket 不存在或当前账号无权访问，请确认 Bucket 名称和授权策略"
		case strings.Contains(lower, "endpoint must include"), strings.Contains(lower, "invalid endpoint"):
			return "对象存储 Endpoint 格式不正确，请填写完整地址，例如 https://s3.example.com"
		}
	}

	switch {
	case strings.Contains(lower, "connection refused"):
		return "无法连接到数据库主机，请确认主机地址、端口和网络访问策略是否正确"
	case strings.Contains(lower, "i/o timeout"), strings.Contains(lower, "deadline exceeded"), strings.Contains(lower, "timeout"):
		return "连接数据库超时，请确认网络连通性、防火墙规则和数据库服务状态"
	case strings.Contains(lower, "no such host"), strings.Contains(lower, "lookup "):
		return "无法解析数据库主机地址，请确认填写的主机名或 IP 是否正确"
	case strings.Contains(lower, "authentication failed"), strings.Contains(lower, "unable to authenticate"), strings.Contains(lower, "access denied"), strings.Contains(lower, "password authentication failed"), strings.Contains(lower, "using password: yes"):
		return "数据库认证失败，请确认用户名、密码和授权范围是否正确"
	case context == errorContextBackupRuntime && strings.Contains(lower, "no space left on device"):
		return "备份临时目录空间不足，请清理磁盘或扩容后重试"
	case context == errorContextBackupRuntime && (strings.Contains(lower, "signal: killed") || strings.Contains(lower, "out of memory") || strings.Contains(lower, "oomkilled")):
		return "备份进程被系统终止，通常是容器内存不足，请检查 Pod OOM 记录和内存限制"
	case context == errorContextBackupRuntime && (strings.Contains(lower, "socket was unexpectedly closed") || strings.Contains(lower, "connection closed") || strings.Contains(lower, "connection reset") || strings.Contains(lower, "unexpected eof")):
		return "备份期间数据库连接意外中断，请检查数据库节点切换、网络抖动和服务端日志"
	case strings.Contains(lower, "not authorized"), strings.Contains(lower, "unauthorized"):
		return "当前数据库账号权限不足，请确认具备所需操作权限"
	case context == errorContextBackupRuntime && (strings.Contains(lower, "tls handshake") || strings.Contains(lower, "ssl handshake") || strings.Contains(lower, "certificate signed by unknown authority") || strings.Contains(lower, "certificate is not valid")):
		return "数据库 TLS 握手或证书校验失败，请检查 TLS 模式、CA 证书和连接地址"
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
	case strings.Contains(lower, "mysqldump not found"), strings.Contains(lower, "mysql not found"), strings.Contains(lower, "pg_dump not found"), strings.Contains(lower, "pg_restore not found"), strings.Contains(lower, "psql not found"), strings.Contains(lower, "mongodump not found"), strings.Contains(lower, "mongorestore not found"), strings.Contains(lower, "cypher-shell not found"):
		if context == errorContextMigrationRuntime {
			return "迁移依赖的数据库客户端工具未安装或不在 PATH 中，请先检查运行环境"
		}
		return "数据库客户端工具未安装或不在 PATH 中，请先检查运行环境"
	case strings.Contains(lower, "mysqldump:"):
		return "源端导出失败，请确认源数据库可访问、账号具备导出权限，且客户端工具版本兼容"
	case strings.Contains(lower, "mongodump:"):
		return "MongoDB 导出失败，请确认源数据库可访问、账号具备备份权限，并检查认证库和客户端工具版本"
	case strings.Contains(lower, "cypher-shell export:"):
		return "Neo4j 导出失败，请确认 cypher-shell 可用、APOC 导出过程已启用，并且账号具备读取权限"
	case strings.Contains(lower, "cypher-shell restore:"):
		return "Neo4j 导入失败，请确认目标库可访问、账号具备写入权限，并检查目标端是否存在同名约束或数据冲突"
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
	case errorContextRemoteStorage:
		return "远端存储操作失败，请确认 Endpoint、访问凭证、Bucket 权限和网络状态"
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

func validMongoDatabaseName(name string) bool {
	if name == "" || len(name) > 63 {
		return false
	}
	for _, value := range name {
		if value < 32 || value == 127 {
			return false
		}
		switch value {
		case '/', '\\', '.', '"', '$', ' ':
			return false
		}
	}
	return true
}

func validDatabaseNameForType(databaseType, name string) bool {
	if databaseType == "MongoDB" {
		return validMongoDatabaseName(name)
	}
	if databaseType == "Neo4j" {
		return validDatabaseName(name)
	}
	return validDatabaseName(name)
}

func invalidDatabaseNameMessage(databaseType string) string {
	if databaseType == "MongoDB" {
		return "MongoDB 数据库名格式不正确，请避免使用空格以及 / \\ . \" $ 等字符，长度不要超过 63 个字符"
	}
	if databaseType == "Neo4j" {
		return "Neo4j 数据库名格式不正确，请使用字母或下划线开头，只包含字母、数字、下划线或短横线"
	}
	return "数据库名格式不正确，请使用字母或下划线开头，只包含字母、数字、下划线或短横线"
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
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
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

func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
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
