package api

import (
	"context"
	"crypto/rand"
	"databridge/internal/model"
	"databridge/internal/remotestorage"
	"databridge/internal/store"
	"encoding/base64"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

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

func (s *Server) listWebDAVDirectories(w http.ResponseWriter, r *http.Request) {
	var input model.StorageInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if normalizeStorageType(input.Type) != "webdav" {
		writeError(w, http.StatusBadRequest, "invalid_storage", "只有 WebDAV 存储支持获取目录列表")
		return
	}
	current := model.RemoteStorage{
		Type:      "webdav",
		Endpoint:  strings.TrimSpace(input.Endpoint),
		AccessKey: strings.TrimSpace(input.AccessKey),
		SecretKey: input.SecretKey,
	}
	dir := strings.TrimSpace(input.Path)
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	entries, err := remotestorage.ListWebDAVDirectory(ctx, current, dir)
	if err != nil {
		writeError(w, http.StatusBadGateway, "webdav_list_failed", userFacingErrorMessage(errorContextRemoteStorage, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": dir, "entries": entries})
}

func (s *Server) listStoredWebDAVDirectories(w http.ResponseWriter, r *http.Request) {
	current, err := s.store.Storage(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "storage not found")
		return
	}
	if current.Type != "webdav" {
		writeError(w, http.StatusBadRequest, "invalid_storage", "只有 WebDAV 存储支持获取目录列表")
		return
	}
	var input model.StorageInput
	if !decodeJSON(w, r, &input) {
		return
	}
	storage := current
	storage.Endpoint = nonEmpty(input.Endpoint, current.Endpoint)
	storage.AccessKey = nonEmpty(input.AccessKey, current.AccessKey)
	storage.SecretKey = nonEmpty(input.SecretKey, current.SecretKey)
	dir := strings.TrimSpace(input.Path)
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	entries, err := remotestorage.ListWebDAVDirectory(ctx, storage, dir)
	if err != nil {
		writeError(w, http.StatusBadGateway, "webdav_list_failed", userFacingErrorMessage(errorContextRemoteStorage, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": dir, "entries": entries})
}

func (s *Server) listWebDAVDirectory(w http.ResponseWriter, r *http.Request) {
	current, err := s.store.Storage(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "storage not found")
		return
	}
	if current.Type != "webdav" {
		writeError(w, http.StatusBadRequest, "invalid_storage", "只有 WebDAV 存储支持查看目录")
		return
	}
	var input struct {
		Path string `json:"path"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	dir := strings.TrimSpace(input.Path)
	if dir == "" {
		dir = current.Path
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	entries, err := remotestorage.ListWebDAVDirectory(ctx, current, dir)
	if err != nil {
		writeError(w, http.StatusBadGateway, "webdav_list_failed", userFacingErrorMessage(errorContextRemoteStorage, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": dir, "entries": entries})
}

func (s *Server) createWebDAVDirectory(w http.ResponseWriter, r *http.Request) {
	current, err := s.store.Storage(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "storage not found")
		return
	}
	if current.Type != "webdav" {
		writeError(w, http.StatusBadRequest, "invalid_storage", "只有 WebDAV 存储支持创建目录")
		return
	}
	var input struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	name := strings.Trim(strings.TrimSpace(input.Name), "/")
	if name == "" || strings.Contains(name, "..") {
		writeError(w, http.StatusBadRequest, "invalid_directory", "请输入有效目录名称")
		return
	}
	parent := strings.TrimSpace(input.Path)
	if parent == "" {
		parent = current.Path
	}
	dir := path.Join(parent, name)
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	if err := remotestorage.CreateWebDAVDirectory(ctx, current, dir); err != nil {
		writeError(w, http.StatusBadGateway, "webdav_create_failed", userFacingErrorMessage(errorContextRemoteStorage, err))
		return
	}
	entries, err := remotestorage.ListWebDAVDirectory(ctx, current, parent)
	if err != nil {
		entries = []remotestorage.WebDAVEntry{}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"path": parent, "created": dir, "entries": entries})
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

func (s *Server) updateStorageFavorite(w http.ResponseWriter, r *http.Request) {
	var input model.FavoriteInput
	if !decodeJSON(w, r, &input) {
		return
	}
	storage, err := s.store.SetStorageFavorite(r.PathValue("id"), input.Favorite)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "storage not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "storage_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, redactStorageSecret(storage))
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
	case "webdav":
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		if err := remotestorage.TestWebDAV(ctx, item); err != nil {
			status = "warning"
			message = userFacingErrorMessage(errorContextRemoteStorage, err)
			log.Printf("remote storage[%s] webdav test failed: %v", item.ID, err)
		} else {
			message = "WebDAV 存储测试通过"
		}
	case "google_drive", "one_drive":
		if item.Type == "one_drive" {
			ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
			defer cancel()
			if err := remotestorage.TestOneDrive(ctx, item); err != nil {
				status = "warning"
				message = userFacingErrorMessage(errorContextRemoteStorage, err)
				log.Printf("remote storage[%s] onedrive test failed: %v", item.ID, err)
			} else {
				message = "OneDrive 存储测试通过"
			}
		} else {
			status = "needs_auth"
			message = "该存储需要完成 OAuth 授权后才能测试"
		}
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

func (s *Server) authorizeOneDrive(w http.ResponseWriter, r *http.Request) {
	storage, err := s.store.Storage(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "storage not found")
		return
	}
	if storage.Type != "one_drive" {
		writeError(w, http.StatusBadRequest, "invalid_storage", "只有 OneDrive 存储支持 OAuth 授权")
		return
	}
	stateBytes := make([]byte, 24)
	if _, err := rand.Read(stateBytes); err != nil {
		writeError(w, http.StatusInternalServerError, "oauth_error", "生成 OAuth 状态失败")
		return
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes)
	redirectURI := s.oneDriveRedirectURI(r, storage.ID)
	authorizeURL, err := remotestorage.OneDriveAuthorizeURL(storage, redirectURI, state)
	if err != nil {
		writeError(w, http.StatusBadRequest, "oauth_error", err.Error())
		return
	}
	s.oauthMu.Lock()
	s.oauth[state] = oneDriveOAuthState{StorageID: storage.ID, ExpiresAt: time.Now().Add(10 * time.Minute)}
	s.oauthMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"url": authorizeURL, "redirectUri": redirectURI})
}

func (s *Server) oneDriveCallback(w http.ResponseWriter, r *http.Request) {
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	s.oauthMu.Lock()
	oauthState, ok := s.oauth[state]
	if ok {
		delete(s.oauth, state)
	}
	s.oauthMu.Unlock()
	if !ok || oauthState.ExpiresAt.Before(time.Now()) || oauthState.StorageID != r.PathValue("id") {
		http.Error(w, "OneDrive OAuth state is invalid or expired", http.StatusBadRequest)
		return
	}
	if oauthError := strings.TrimSpace(r.URL.Query().Get("error")); oauthError != "" {
		http.Error(w, "OneDrive OAuth failed: "+oauthError, http.StatusBadRequest)
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		http.Error(w, "OneDrive OAuth code is missing", http.StatusBadRequest)
		return
	}
	storage, err := s.store.Storage(oauthState.StorageID)
	if err != nil {
		http.Error(w, "OneDrive storage not found", http.StatusNotFound)
		return
	}
	credential, err := remotestorage.OneDriveExchangeCode(r.Context(), storage, code, s.oneDriveRedirectURI(r, storage.ID))
	if err != nil {
		http.Error(w, "OneDrive OAuth failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	secret, err := remotestorage.EncodeOneDriveCredential(credential)
	if err != nil {
		http.Error(w, "save OneDrive credential failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	updated, err := s.store.UpdateStorage(storage.ID, model.UpdateStorageInput{
		Type: storage.Type, Name: storage.Name, Provider: storage.Provider, Endpoint: storage.Endpoint,
		Bucket: storage.Bucket, Region: storage.Region, AccessKey: storage.AccessKey, SecretKey: secret,
		Path: storage.Path, AuthMode: "oauth", Note: storage.Note, Verified: true,
	})
	if err != nil {
		http.Error(w, "save OneDrive credential failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_ = updated
	http.Redirect(w, r, strings.TrimRight(s.publicBaseURL(r), "/")+"/storages?onedrive=authorized", http.StatusFound)
}

func (s *Server) oneDriveRedirectURI(r *http.Request, storageID string) string {
	return strings.TrimRight(s.publicBaseURL(r), "/") + "/api/storages/" + url.PathEscape(storageID) + "/onedrive/callback"
}

func (s *Server) publicBaseURL(r *http.Request) string {
	if configured := strings.TrimSpace(os.Getenv("DATABRIDGE_PUBLIC_URL")); configured != "" {
		return strings.TrimRight(configured, "/")
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		proto = "http"
	}
	host := r.Host
	if host == "" {
		host = "localhost:8081"
	}
	return proto + "://" + host
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
	if storageType == "webdav" && (strings.TrimSpace(input.Endpoint) == "" || strings.TrimSpace(input.AccessKey) == "" || input.SecretKey == "") {
		writeError(w, http.StatusBadRequest, "invalid_storage", "WebDAV 需要填写 Endpoint、用户名和密码")
		return false
	}
	if storageType == "one_drive" && (strings.TrimSpace(input.Endpoint) == "" || strings.TrimSpace(input.AccessKey) == "" || input.SecretKey == "") {
		writeError(w, http.StatusBadRequest, "invalid_storage", "OneDrive 需要填写 Graph 端点、应用程序 ID 和客户端密钥")
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
	if storageType == "webdav" && (strings.TrimSpace(input.Endpoint) == "" || strings.TrimSpace(input.AccessKey) == "") {
		writeError(w, http.StatusBadRequest, "invalid_storage", "WebDAV 需要填写 Endpoint 和用户名")
		return false
	}
	if storageType == "one_drive" && (strings.TrimSpace(input.Endpoint) == "" || strings.TrimSpace(input.AccessKey) == "") {
		writeError(w, http.StatusBadRequest, "invalid_storage", "OneDrive 需要填写 Graph 端点和应用程序 ID")
		return false
	}
	return true
}

func normalizeStorageType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "object", "object_storage", "object-storage", "s3":
		return "object"
	case "webdav", "web_dav", "web-dav":
		return "webdav"
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
