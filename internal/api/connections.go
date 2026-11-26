package api

import (
	"context"
	"databridge/internal/etcd"
	"databridge/internal/model"
	"databridge/internal/mongo"
	"databridge/internal/mysql"
	"databridge/internal/neo4j"
	"databridge/internal/postgres"
	"databridge/internal/store"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

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
	const maxWorkers = 6
	sem := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup
	for index := range items {
		if items[index].Version != "" {
			continue
		}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			versionCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			version, err := serverVersion(versionCtx, items[idx])
			cancel()
			if err != nil {
				log.Printf("connection[%s] version discovery failed: %v", items[idx].ID, err)
				return
			}
			items[idx].Version = version
			if err := s.store.UpdateConnectionVersion(items[idx].ID, version); err != nil {
				log.Printf("connection[%s] version persistence failed: %v", items[idx].ID, err)
			}
		}(index)
	}
	wg.Wait()
	return items
}

func (s *Server) withConnectionRegions(ctx context.Context, items []model.Connection) []model.Connection {
	const maxWorkers = 6
	sem := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup
	for index := range items {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			items[idx].Region = s.connectionRegion(ctx, items[idx].Host)
		}(index)
	}
	wg.Wait()
	return items
}

func (s *Server) testConnection(w http.ResponseWriter, r *http.Request) {
	var input model.ConnectionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Type = normalizeDatabaseType(input.Type)
	if err := normalizeMongoConnectionEndpoints(input.Type, &input.Endpoints); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_connection", err.Error())
		return
	}
	if !validateConnection(w, &input) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	latency, version, err := testConnection(ctx, input)
	if err != nil {
		writeError(w, http.StatusBadGateway, "connection_failed", userFacingErrorMessage(errorContextConnectionTest, err))
		return
	}
	response := map[string]any{"ok": true, "latency": latency.Milliseconds(), "version": version}
	if input.Type == "MongoDB" {
		response["mongoBackupReadiness"] = mongoBackupReadiness(ctx, connectionFromInput(input))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) createConnection(w http.ResponseWriter, r *http.Request) {
	var input model.ConnectionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Type = normalizeDatabaseType(input.Type)
	if err := normalizeMongoConnectionEndpoints(input.Type, &input.Endpoints); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_connection", err.Error())
		return
	}
	if !validateConnection(w, &input) {
		return
	}
	shouldValidate := input.Validate == nil || *input.Validate
	var latency time.Duration
	var version string
	if shouldValidate {
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		var err error
		latency, version, err = testConnection(ctx, input)
		if err != nil {
			writeError(w, http.StatusBadGateway, "connection_failed", userFacingErrorMessage(errorContextConnectionTest, err))
			return
		}
		if input.Type == "etcd" {
			input.Endpoints, err = etcd.DiscoverEndpoints(ctx, connectionFromInput(input))
			if err != nil {
				writeError(w, http.StatusBadGateway, "connection_failed", err.Error())
				return
			}
		}
	} else {
		input.Status = "untested"
	}
	connection, err := s.store.AddConnection(input, latency, version)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextConnectionTest, err))
		return
	}
	if connection.Type == "MongoDB" {
		readiness := mongoBackupReadiness(r.Context(), connection)
		if err := s.store.UpdateMongoBackupReadiness(connection.ID, readiness); err != nil {
			log.Printf("connection[%s] MongoDB backup readiness persistence failed: %v", connection.ID, err)
		}
		connection.MongoBackupReadiness = &readiness
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
	if !decodeJSON(w, r, &input) || !validateConnectionUpdate(w, current.Type, &input) {
		return
	}
	if input.Endpoints != nil {
		if err := normalizeMongoConnectionEndpoints(current.Type, &input.Endpoints); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_connection", err.Error())
			return
		}
	}
	password := current.Password
	if strings.TrimSpace(input.Password) != "" {
		password = input.Password
	}
	tlsConfig := current.TLSConfig
	if !input.TLSConfig.Empty() {
		tlsConfig = input.TLSConfig
	}
	if !validateConnectionTLSConfig(w, current.Type, input.SSLMode, tlsConfig) {
		return
	}
	endpoints := current.Endpoints
	if input.Endpoints != nil {
		endpoints = input.Endpoints
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	latency, version, err := testConnection(ctx, model.ConnectionInput{
		Type:      current.Type,
		Name:      input.Name,
		Note:      input.Note,
		Host:      input.Host,
		Port:      input.Port,
		Username:  input.Username,
		Password:  password,
		SSLMode:   input.SSLMode,
		TLSConfig: tlsConfig,
		Endpoints: endpoints,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "connection_failed", userFacingErrorMessage(errorContextConnectionTest, err))
		return
	}
	if current.Type == "etcd" {
		candidate := current
		candidate.Host, candidate.Port, candidate.Username, candidate.Password = input.Host, input.Port, input.Username, password
		candidate.SSLMode, candidate.TLSConfig = input.SSLMode, tlsConfig
		input.Endpoints, err = etcd.DiscoverEndpoints(ctx, candidate)
		if err != nil {
			writeError(w, http.StatusBadGateway, "connection_failed", err.Error())
			return
		}
	}
	connection, err := s.store.UpdateConnection(current.ID, input, latency, version)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextConnectionTest, err))
		return
	}
	if connection.Type == "MongoDB" {
		readiness := mongoBackupReadiness(r.Context(), connection)
		if err := s.store.UpdateMongoBackupReadiness(connection.ID, readiness); err != nil {
			log.Printf("connection[%s] MongoDB backup readiness persistence failed: %v", connection.ID, err)
		}
		connection.MongoBackupReadiness = &readiness
	}
	connection.Region = s.connectionRegion(r.Context(), connection.Host)
	writeJSON(w, http.StatusOK, connection)
}

func (s *Server) updateConnectionFavorite(w http.ResponseWriter, r *http.Request) {
	var input model.FavoriteInput
	if !decodeJSON(w, r, &input) {
		return
	}
	connection, err := s.store.SetConnectionFavorite(r.PathValue("id"), input.Favorite)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "connection not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "storage_error", err.Error())
		return
	}
	connection.Region = s.connectionRegion(r.Context(), connection.Host)
	writeJSON(w, http.StatusOK, connection)
}

func (s *Server) duplicateConnection(w http.ResponseWriter, r *http.Request) {
	current, err := s.store.Connection(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "connection not found")
		return
	}
	var input model.DuplicateConnectionInput
	if !decodeJSON(w, r, &input) || !validateDuplicateConnection(w, current.Type, &input) {
		return
	}
	password := current.Password
	if strings.TrimSpace(input.Password) != "" {
		password = input.Password
	}
	tlsConfig := current.TLSConfig
	if !input.TLSConfig.Empty() {
		tlsConfig = input.TLSConfig
	}
	if !validateConnectionTLSConfig(w, current.Type, input.SSLMode, tlsConfig) {
		return
	}
	duplicate := model.ConnectionInput{
		Type:        current.Type,
		Name:        input.Name,
		Note:        input.Note,
		Project:     input.Project,
		Environment: input.Environment,
		Tags:        append([]string(nil), input.Tags...),
		Host:        input.Host,
		Port:        input.Port,
		Username:    input.Username,
		Password:    password,
		SSLMode:     input.SSLMode,
		TLSConfig:   tlsConfig,
		Endpoints:   append([]string(nil), current.Endpoints...),
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	latency, version, err := testConnection(ctx, duplicate)
	if err != nil {
		writeError(w, http.StatusBadGateway, "connection_failed", userFacingErrorMessage(errorContextConnectionTest, err))
		return
	}
	if duplicate.Type == "etcd" {
		duplicate.Endpoints, err = etcd.DiscoverEndpoints(ctx, connectionFromInput(duplicate))
		if err != nil {
			writeError(w, http.StatusBadGateway, "connection_failed", err.Error())
			return
		}
	}
	connection, err := s.store.AddConnection(duplicate, latency, version)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextConnectionTest, err))
		return
	}
	connection.Region = s.connectionRegion(r.Context(), connection.Host)
	writeJSON(w, http.StatusCreated, connection)
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
		Type:      connection.Type,
		Name:      connection.Name,
		Host:      connection.Host,
		Port:      connection.Port,
		Username:  connection.Username,
		Password:  connection.Password,
		SSLMode:   connection.SSLMode,
		TLSConfig: connection.TLSConfig,
		Endpoints: connection.Endpoints,
	})
	if connection.Type == "etcd" {
		latency, version, err = etcd.TestConnection(ctx, connection)
	}
	if err != nil {
		_ = s.store.UpdateConnectionHealth(connection.ID, "warning", 0, connection.Version)
		writeError(w, http.StatusBadGateway, "connection_failed", userFacingErrorMessage(errorContextConnectionTest, err))
		return
	}
	if err := s.store.UpdateConnectionHealth(connection.ID, "connected", latency, version); err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextConnectionTest, err))
		return
	}
	if connection.Type == "etcd" {
		if endpoints, discoverErr := etcd.DiscoverEndpoints(ctx, connection); discoverErr == nil {
			connection.Endpoints = endpoints
			_ = s.store.UpdateConnectionEndpoints(connection.ID, endpoints)
		}
	}
	if connection.Type == "MongoDB" {
		readiness := mongoBackupReadiness(r.Context(), connection)
		if err := s.store.UpdateMongoBackupReadiness(connection.ID, readiness); err != nil {
			log.Printf("connection[%s] MongoDB backup readiness persistence failed: %v", connection.ID, err)
		}
		connection.MongoBackupReadiness = &readiness
	}
	connection.Status = "connected"
	connection.LatencyMS = latency.Milliseconds()
	connection.Version = version
	connection.Region = s.connectionRegion(r.Context(), connection.Host)
	writeJSON(w, http.StatusOK, connection)
}

func connectionFromInput(input model.ConnectionInput) model.Connection {
	return model.Connection{Type: input.Type, Host: input.Host, Port: input.Port, Username: input.Username, Password: input.Password, SSLMode: input.SSLMode, TLSConfig: input.TLSConfig, Endpoints: input.Endpoints}
}

func normalizeMongoConnectionEndpoints(databaseType string, endpoints *[]string) error {
	if databaseType != "MongoDB" {
		if endpoints != nil {
			*endpoints = nil
		}
		return nil
	}
	items, err := mongo.NormalizeReplicaSeeds(*endpoints)
	if err != nil {
		return err
	}
	*endpoints = items
	return nil
}

func mongoBackupReadiness(ctx context.Context, connection model.Connection) model.MongoBackupReadiness {
	readiness := model.MongoBackupReadiness{CheckedAt: time.Now().In(appLocation)}
	info, err := mongo.IncrementalBackupInfo(ctx, connection)
	if err != nil {
		readiness.Status = "unavailable"
		if strings.Contains(err.Error(), "不是 MongoDB 副本集") {
			readiness.Message = "当前连接不是副本集，不能使用 oplog 增量备份"
		} else {
			readiness.Message = "无法读取副本集 oplog：" + userFacingErrorMessage(errorContextConnectionTest, err)
		}
		return readiness
	}
	readiness.ReplicaSet = info.ReplicaSetName
	if info.Newest.Seconds >= info.Oldest.Seconds {
		readiness.OplogWindowHours = float64(info.Newest.Seconds-info.Oldest.Seconds) / 3600
	}
	if readiness.OplogWindowHours >= minimumMongoOplogWindowHours {
		readiness.Status = "ready"
		readiness.Message = fmt.Sprintf("oplog 窗口 %.1f 小时，满足至少 %d 小时的增量备份要求", readiness.OplogWindowHours, minimumMongoOplogWindowHours)
		return readiness
	}
	readiness.Status = "warning"
	readiness.Message = fmt.Sprintf("oplog 窗口仅 %.1f 小时，低于至少 %d 小时的增量备份要求，请先扩容 oplog", readiness.OplogWindowHours, minimumMongoOplogWindowHours)
	return readiness
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
	if len(s.geoCache) >= 1024 {
		clear(s.geoCache)
	}
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
	if !validDatabaseNameForType(connection.Type, name) {
		writeError(w, http.StatusBadRequest, "invalid_database", invalidDatabaseNameMessage(connection.Type))
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
	if request.Template != "users" && request.Template != "orders" && request.Template != "products" && request.Template != "payload" {
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

func normalizeConnectionEnvironment(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return "", true
	case "prod", "production", "生产", "生产环境":
		return "prod", true
	case "staging", "stage", "pre", "预发", "预发环境", "预发布":
		return "staging", true
	case "test", "testing", "qa", "测试", "测试环境":
		return "test", true
	case "dev", "development", "开发", "开发环境":
		return "dev", true
	default:
		return "", false
	}
}

func normalizeConnectionTags(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(items))
	result := make([]string, 0, len(items))
	for _, raw := range items {
		tag := strings.TrimSpace(raw)
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		result = append(result, tag)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func validateConnection(w http.ResponseWriter, input *model.ConnectionInput) bool {
	input.Type = normalizeDatabaseType(input.Type)
	if input.Type == "" {
		writeError(w, http.StatusBadRequest, "invalid_connection", "请选择数据库类型")
		return false
	}
	credentialsMissing := input.Type != "etcd" && (strings.TrimSpace(input.Username) == "" || input.Password == "")
	if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Host) == "" || credentialsMissing {
		writeError(w, http.StatusBadRequest, "invalid_connection", "连接名称和主机地址不能为空；该数据库类型还需要用户名和密码")
		return false
	}
	if input.Port == 0 {
		input.Port = defaultPort(input.Type)
	}
	if input.Port < 1 || input.Port > 65535 {
		writeError(w, http.StatusBadRequest, "invalid_connection", "端口号必须在 1 到 65535 之间")
		return false
	}
	env, ok := normalizeConnectionEnvironment(input.Environment)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_connection", "环境仅支持 prod、staging、test 或 dev")
		return false
	}
	input.Environment = env
	input.Project = strings.TrimSpace(input.Project)
	input.Tags = normalizeConnectionTags(input.Tags)
	if !validateConnectionTLSConfig(w, input.Type, input.SSLMode, input.TLSConfig) {
		return false
	}
	return true
}

func validateConnectionUpdate(w http.ResponseWriter, databaseType string, input *model.UpdateConnectionInput) bool {
	if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Host) == "" || (databaseType != "etcd" && strings.TrimSpace(input.Username) == "") {
		writeError(w, http.StatusBadRequest, "invalid_connection", "连接名称和主机地址不能为空")
		return false
	}
	if input.Port == 0 {
		input.Port = 1
	}
	if input.Port < 1 || input.Port > 65535 {
		writeError(w, http.StatusBadRequest, "invalid_connection", "端口号必须在 1 到 65535 之间")
		return false
	}
	env, ok := normalizeConnectionEnvironment(input.Environment)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_connection", "环境仅支持 prod、staging、test 或 dev")
		return false
	}
	input.Environment = env
	input.Project = strings.TrimSpace(input.Project)
	input.Tags = normalizeConnectionTags(input.Tags)
	return true
}

func validateDuplicateConnection(w http.ResponseWriter, databaseType string, input *model.DuplicateConnectionInput) bool {
	if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Host) == "" || (databaseType != "etcd" && strings.TrimSpace(input.Username) == "") {
		writeError(w, http.StatusBadRequest, "invalid_connection", "连接名称和主机地址不能为空")
		return false
	}
	if input.Port == 0 {
		input.Port = 1
	}
	if input.Port < 1 || input.Port > 65535 {
		writeError(w, http.StatusBadRequest, "invalid_connection", "端口号必须在 1 到 65535 之间")
		return false
	}
	env, ok := normalizeConnectionEnvironment(input.Environment)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_connection", "环境仅支持 prod、staging、test 或 dev")
		return false
	}
	input.Environment = env
	input.Project = strings.TrimSpace(input.Project)
	input.Tags = normalizeConnectionTags(input.Tags)
	return true
}

func validateConnectionTLSConfig(w http.ResponseWriter, databaseType, sslMode string, tlsConfig model.ConnectionTLSConfig) bool {
	if strings.TrimSpace(sslMode) != "verify-full" {
		return true
	}
	if strings.TrimSpace(tlsConfig.CACert) == "" {
		writeError(w, http.StatusBadRequest, "invalid_connection", databaseType+" 使用 verify-full 时需要填写 CA 证书 PEM")
		return false
	}
	if (strings.TrimSpace(tlsConfig.ClientCert) == "") != (strings.TrimSpace(tlsConfig.ClientKey) == "") {
		writeError(w, http.StatusBadRequest, "invalid_connection", "客户端证书和私钥需要同时填写")
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
	case "mongodb", "mongo":
		return "MongoDB"
	case "neo4j":
		return "Neo4j"
	case "etcd":
		return "etcd"
	default:
		return ""
	}
}

func defaultPort(databaseType string) int {
	if databaseType == "MySQL" {
		return 3306
	}
	if databaseType == "MongoDB" {
		return 27017
	}
	if databaseType == "Neo4j" {
		return 7687
	}
	if databaseType == "etcd" {
		return 2379
	}
	return 5432
}

func testConnection(ctx context.Context, input model.ConnectionInput) (time.Duration, string, error) {
	switch input.Type {
	case "PostgreSQL":
		return postgres.Test(ctx, input)
	case "MySQL":
		return mysql.Test(ctx, input)
	case "MongoDB":
		return mongo.Test(ctx, input)
	case "Neo4j":
		return neo4j.Test(ctx, input)
	case "etcd":
		return etcd.Test(ctx, input)
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
	case "MongoDB":
		return mongo.ServerVersion(ctx, connection)
	case "Neo4j":
		return neo4j.ServerVersion(ctx, connection)
	case "etcd":
		return etcd.ServerVersion(ctx, connection)
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
	case "MongoDB":
		return mongo.Databases(ctx, connection)
	case "Neo4j":
		return neo4j.Databases(ctx, connection)
	case "etcd":
		return []model.DatabaseInfo{{Name: "整个集群"}}, nil
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
	case "MongoDB":
		return mongo.CreateDatabase(ctx, connection, name)
	case "Neo4j":
		return neo4j.CreateDatabase(ctx, connection, name)
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
	case "MongoDB":
		items, err := mongo.CollectionNames(ctx, connection, database)
		if err != nil {
			return nil, err
		}
		result := make([]model.TableInfo, 0, len(items))
		for _, item := range items {
			result = append(result, model.TableInfo{
				ID:        item,
				Schema:    database,
				Name:      item,
				SizeBytes: 0,
				Rows:      0,
			})
		}
		return result, nil
	case "Neo4j":
		return neo4j.LabelsAndRelationshipTypes(ctx, connection, database)
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
	case "MongoDB":
		return mongo.SeedData(ctx, connection, database, rows, mode, template)
	case "Neo4j":
		return neo4j.SeedData(ctx, connection, database, rows, mode, template)
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
