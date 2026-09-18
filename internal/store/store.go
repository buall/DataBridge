package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"databridge/internal/dbtls"
	"databridge/internal/model"
	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

func marshalStrings(values []string) []byte {
	data, _ := json.Marshal(values)
	return data
}

type Store struct {
	db      *sql.DB
	aead    cipher.AEAD
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

type AuditLog struct {
	ID             string    `json:"id"`
	Time           time.Time `json:"time"`
	Level          string    `json:"level"`
	Action         string    `json:"action"`
	ResourceType   string    `json:"resourceType"`
	ResourceID     string    `json:"resourceID"`
	ResourceName   string    `json:"resourceName"`
	ConnectionName string    `json:"connectionName"`
	Database       string    `json:"database"`
	Status         string    `json:"status"`
	Message        string    `json:"message"`
	Detail         string    `json:"detail"`
}

const (
	smtpSettingsKey         = "smtp"
	notificationSettingsKey = "notification"
)

func New(databasePath, keyPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	key, err := loadOrCreateKey(keyPath)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create credential cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create credential encryption: %w", err)
	}
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db, aead: aead, cancels: make(map[string]context.CancelFunc)}
	if err := store.initialize(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) SMTPSettings() (model.SMTPSettings, error) {
	var data []byte
	if err := s.db.QueryRow(`SELECT data FROM app_settings WHERE key = ?`, smtpSettingsKey).Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.SMTPSettings{Port: 587}, nil
		}
		return model.SMTPSettings{}, fmt.Errorf("load smtp settings: %w", err)
	}
	var stored struct {
		Enabled           bool   `json:"enabled"`
		Host              string `json:"host"`
		Port              int    `json:"port"`
		Username          string `json:"username,omitempty"`
		PasswordEncrypted []byte `json:"passwordEncrypted,omitempty"`
		From              string `json:"from"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		return model.SMTPSettings{}, fmt.Errorf("decode smtp settings: %w", err)
	}
	settings := model.SMTPSettings{
		Enabled:  stored.Enabled,
		Host:     stored.Host,
		Port:     stored.Port,
		Username: stored.Username,
		From:     stored.From,
	}
	if settings.Port == 0 {
		settings.Port = 587
	}
	settings.PasswordSet = len(stored.PasswordEncrypted) > 0
	if len(stored.PasswordEncrypted) > 0 {
		password, err := s.decrypt(stored.PasswordEncrypted)
		if err != nil {
			return model.SMTPSettings{}, fmt.Errorf("decrypt smtp password: %w", err)
		}
		settings.Password = password
	}
	return settings, nil
}

func (s *Store) UpdateSMTPSettings(input model.SMTPSettings) (model.SMTPSettings, error) {
	current, err := s.SMTPSettings()
	if err != nil {
		return model.SMTPSettings{}, err
	}
	settings := model.SMTPSettings{
		Enabled:  input.Enabled,
		Host:     strings.TrimSpace(input.Host),
		Port:     input.Port,
		Username: strings.TrimSpace(input.Username),
		From:     strings.TrimSpace(input.From),
	}
	if settings.Port == 0 {
		settings.Port = 587
	}
	password := input.Password
	if password == "" && current.PasswordSet {
		password = current.Password
	}
	var passwordEncrypted []byte
	if password != "" {
		passwordEncrypted, err = s.encrypt(password)
		if err != nil {
			return model.SMTPSettings{}, fmt.Errorf("encrypt smtp password: %w", err)
		}
	}
	stored := struct {
		Enabled           bool   `json:"enabled"`
		Host              string `json:"host"`
		Port              int    `json:"port"`
		Username          string `json:"username,omitempty"`
		PasswordEncrypted []byte `json:"passwordEncrypted,omitempty"`
		From              string `json:"from"`
	}{
		Enabled: settings.Enabled, Host: settings.Host, Port: settings.Port,
		Username: settings.Username, PasswordEncrypted: passwordEncrypted, From: settings.From,
	}
	data, err := json.Marshal(stored)
	if err != nil {
		return model.SMTPSettings{}, fmt.Errorf("encode smtp settings: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`
		INSERT INTO app_settings (key, data, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at`,
		smtpSettingsKey, data, now); err != nil {
		return model.SMTPSettings{}, fmt.Errorf("save smtp settings: %w", err)
	}
	settings.Password = password
	settings.PasswordSet = password != ""
	return settings, nil
}

func (s *Store) NotificationSettings() (model.NotificationSettings, error) {
	var data []byte
	if err := s.db.QueryRow(`SELECT data FROM app_settings WHERE key = ?`, notificationSettingsKey).Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.NotificationSettings{}, nil
		}
		return model.NotificationSettings{}, fmt.Errorf("load notification settings: %w", err)
	}
	var settings model.NotificationSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return model.NotificationSettings{}, fmt.Errorf("decode notification settings: %w", err)
	}
	settings.MessageTitle = strings.TrimSpace(settings.MessageTitle)
	return settings, nil
}

func (s *Store) UpdateNotificationSettings(input model.NotificationSettings) (model.NotificationSettings, error) {
	settings := model.NotificationSettings{MessageTitle: strings.TrimSpace(input.MessageTitle)}
	data, err := json.Marshal(settings)
	if err != nil {
		return model.NotificationSettings{}, fmt.Errorf("encode notification settings: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`
		INSERT INTO app_settings (key, data, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at`,
		notificationSettingsKey, data, now); err != nil {
		return model.NotificationSettings{}, fmt.Errorf("save notification settings: %w", err)
	}
	return settings, nil
}

func (s *Store) NotificationRecipients() ([]model.NotificationRecipient, error) {
	rows, err := s.db.Query(`SELECT id, name, type, target_encrypted, message_title, enabled, created_at, updated_at FROM notification_recipients ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list notification recipients: %w", err)
	}
	defer rows.Close()
	items := make([]model.NotificationRecipient, 0)
	for rows.Next() {
		item, err := s.scanNotificationRecipient(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) NotificationRecipient(id string) (model.NotificationRecipient, error) {
	return s.scanNotificationRecipient(s.db.QueryRow(`SELECT id, name, type, target_encrypted, message_title, enabled, created_at, updated_at FROM notification_recipients WHERE id = ?`, id))
}

func (s *Store) AddNotificationRecipient(input model.NotificationRecipientInput) (model.NotificationRecipient, error) {
	now := time.Now().UTC()
	target, err := s.encrypt(strings.TrimSpace(input.Target))
	if err != nil {
		return model.NotificationRecipient{}, fmt.Errorf("encrypt notification recipient: %w", err)
	}
	item := model.NotificationRecipient{ID: newID(), Name: strings.TrimSpace(input.Name), Type: input.Type, Target: strings.TrimSpace(input.Target), MessageTitle: strings.TrimSpace(input.MessageTitle), Enabled: input.Enabled, CreatedAt: now, UpdatedAt: now}
	if _, err := s.db.Exec(`INSERT INTO notification_recipients (id, name, type, target_encrypted, message_title, enabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, item.ID, item.Name, item.Type, target, item.MessageTitle, item.Enabled, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		return model.NotificationRecipient{}, fmt.Errorf("add notification recipient: %w", err)
	}
	setNotificationRecipientTargetSummary(&item)
	return item, nil
}

func (s *Store) UpdateNotificationRecipient(id string, input model.NotificationRecipientInput) (model.NotificationRecipient, error) {
	current, err := s.NotificationRecipient(id)
	if err != nil {
		return model.NotificationRecipient{}, err
	}
	targetValue := strings.TrimSpace(input.Target)
	if targetValue == "" {
		targetValue = current.Target
	}
	target, err := s.encrypt(targetValue)
	if err != nil {
		return model.NotificationRecipient{}, fmt.Errorf("encrypt notification recipient: %w", err)
	}
	now := time.Now().UTC()
	result, err := s.db.Exec(`UPDATE notification_recipients SET name = ?, type = ?, target_encrypted = ?, message_title = ?, enabled = ?, updated_at = ? WHERE id = ?`, strings.TrimSpace(input.Name), input.Type, target, strings.TrimSpace(input.MessageTitle), input.Enabled, now.Format(time.RFC3339Nano), id)
	if err != nil {
		return model.NotificationRecipient{}, fmt.Errorf("update notification recipient: %w", err)
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		return model.NotificationRecipient{}, ErrNotFound
	}
	return s.NotificationRecipient(id)
}

func (s *Store) DeleteNotificationRecipient(id string) error {
	result, err := s.db.Exec(`DELETE FROM notification_recipients WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete notification recipient: %w", err)
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) scanNotificationRecipient(row scanner) (model.NotificationRecipient, error) {
	var item model.NotificationRecipient
	var targetEncrypted []byte
	var enabled bool
	var createdAt, updatedAt string
	if err := row.Scan(&item.ID, &item.Name, &item.Type, &targetEncrypted, &item.MessageTitle, &enabled, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.NotificationRecipient{}, ErrNotFound
		}
		return model.NotificationRecipient{}, fmt.Errorf("scan notification recipient: %w", err)
	}
	target, err := s.decrypt(targetEncrypted)
	if err != nil {
		return model.NotificationRecipient{}, fmt.Errorf("decrypt notification recipient: %w", err)
	}
	item.Target = target
	item.Enabled = enabled
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	setNotificationRecipientTargetSummary(&item)
	return item, nil
}

func setNotificationRecipientTargetSummary(item *model.NotificationRecipient) {
	item.TargetSet = strings.TrimSpace(item.Target) != ""
	if item.Type == "telegram" {
		var config struct {
			ChatID string `json:"chatId"`
		}
		if json.Unmarshal([]byte(item.Target), &config) == nil && strings.TrimSpace(config.ChatID) != "" {
			chatID := []rune(strings.TrimSpace(config.ChatID))
			if len(chatID) > 4 {
				item.TargetMasked = "Chat ID: ••••" + string(chatID[len(chatID)-4:])
			} else {
				item.TargetMasked = "Chat ID: ••••"
			}
			return
		}
	}
	if item.Type == "email" {
		parts := strings.SplitN(item.Target, "@", 2)
		if len(parts) == 2 && parts[0] != "" {
			item.TargetMasked = string([]rune(parts[0])[0]) + "***@" + parts[1]
			return
		}
	}
	if parsed := strings.SplitN(item.Target, "://", 2); len(parsed) == 2 {
		host := strings.SplitN(parsed[1], "/", 2)[0]
		item.TargetMasked = parsed[0] + "://" + host + "/••••••"
		return
	}
	if item.TargetSet {
		item.TargetMasked = "••••••"
	}
}

func (s *Store) AddConnection(input model.ConnectionInput, latency time.Duration, version string) (model.Connection, error) {
	status := strings.TrimSpace(input.Status)
	if status == "" {
		status = "connected"
	}
	connection := model.Connection{
		ID: newID(), Name: input.Name, Note: strings.TrimSpace(input.Note), Type: input.Type, Host: input.Host,
		Port: input.Port, Username: input.Username, Password: input.Password,
		SSLMode: input.SSLMode, TLSConfig: dbtls.Normalize(input.TLSConfig), Status: status,
		Endpoints: append([]string(nil), input.Endpoints...),
		LatencyMS: latency.Milliseconds(), Version: version, CreatedAt: time.Now().UTC(),
	}
	encrypted, err := s.encrypt(input.Password)
	if err != nil {
		return model.Connection{}, err
	}
	encryptedTLSConfig, err := s.encryptTLSConfig(connection.TLSConfig)
	if err != nil {
		return model.Connection{}, err
	}
	_, err = s.db.Exec(`
		INSERT INTO connections
		(id, name, note, type, host, port, username, password_encrypted, ssl_mode, tls_config_encrypted, endpoints_json, status, latency_ms, server_version, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		connection.ID, connection.Name, connection.Note, connection.Type, connection.Host, connection.Port,
		connection.Username, encrypted, connection.SSLMode, encryptedTLSConfig, marshalStrings(connection.Endpoints), connection.Status,
		connection.LatencyMS, connection.Version, connection.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return model.Connection{}, fmt.Errorf("save connection: %w", err)
	}
	return connection, nil
}

func (s *Store) Connections() ([]model.Connection, error) {
	rows, err := s.db.Query(`
		SELECT id, name, note, type, host, port, username, password_encrypted,
		       ssl_mode, tls_config_encrypted, endpoints_json, status, latency_ms, server_version, created_at, favorite
		FROM connections ORDER BY favorite DESC, created_at`)
	if err != nil {
		return nil, fmt.Errorf("list connections: %w", err)
	}
	defer rows.Close()
	var items []model.Connection
	for rows.Next() {
		item, scanErr := s.scanConnection(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if items == nil {
		items = []model.Connection{}
	}
	return items, rows.Err()
}

func (s *Store) Connection(id string) (model.Connection, error) {
	row := s.db.QueryRow(`
		SELECT id, name, note, type, host, port, username, password_encrypted,
		       ssl_mode, tls_config_encrypted, endpoints_json, status, latency_ms, server_version, created_at, favorite
		FROM connections WHERE id = ?`, id)
	return s.scanConnection(row)
}

func (s *Store) UpdateConnectionVersion(id, version string) error {
	if _, err := s.db.Exec(`UPDATE connections SET server_version = ? WHERE id = ?`, version, id); err != nil {
		return fmt.Errorf("update connection version: %w", err)
	}
	return nil
}

func (s *Store) UpdateConnectionHealth(id, status string, latency time.Duration, version string) error {
	if _, err := s.db.Exec(`UPDATE connections SET status = ?, latency_ms = ?, server_version = ? WHERE id = ?`,
		status, latency.Milliseconds(), version, id); err != nil {
		return fmt.Errorf("update connection health: %w", err)
	}
	return nil
}

func (s *Store) UpdateConnectionEndpoints(id string, endpoints []string) error {
	if _, err := s.db.Exec(`UPDATE connections SET endpoints_json = ? WHERE id = ?`, marshalStrings(endpoints), id); err != nil {
		return fmt.Errorf("update connection endpoints: %w", err)
	}
	return nil
}

func (s *Store) SetConnectionFavorite(id string, favorite bool) (model.Connection, error) {
	result, err := s.db.Exec(`UPDATE connections SET favorite = ? WHERE id = ?`, favorite, id)
	if err != nil {
		return model.Connection{}, fmt.Errorf("update connection favorite: %w", err)
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		return model.Connection{}, ErrNotFound
	}
	return s.Connection(id)
}

func (s *Store) UpdateConnection(id string, input model.UpdateConnectionInput, latency time.Duration, version string) (model.Connection, error) {
	current, err := s.Connection(id)
	if err != nil {
		return model.Connection{}, err
	}
	password := current.Password
	if strings.TrimSpace(input.Password) != "" {
		password = input.Password
	}
	tlsConfig := current.TLSConfig
	if !input.TLSConfig.Empty() {
		tlsConfig = dbtls.Normalize(input.TLSConfig)
	}
	endpoints := current.Endpoints
	if input.Endpoints != nil {
		endpoints = append([]string(nil), input.Endpoints...)
	}
	encrypted, err := s.encrypt(password)
	if err != nil {
		return model.Connection{}, err
	}
	encryptedTLSConfig, err := s.encryptTLSConfig(tlsConfig)
	if err != nil {
		return model.Connection{}, err
	}
	note := strings.TrimSpace(input.Note)
	status := "connected"
	if _, err := s.db.Exec(`
		UPDATE connections
		SET name = ?, note = ?, host = ?, port = ?, username = ?, password_encrypted = ?, ssl_mode = ?, tls_config_encrypted = ?, endpoints_json = ?, status = ?, latency_ms = ?, server_version = ?, type = ?
		WHERE id = ?`,
		input.Name, note, input.Host, input.Port, input.Username, encrypted, input.SSLMode, encryptedTLSConfig, marshalStrings(endpoints), status, latency.Milliseconds(), version, current.Type, id); err != nil {
		return model.Connection{}, fmt.Errorf("update connection: %w", err)
	}
	updated, err := s.Connection(id)
	if err != nil {
		return model.Connection{}, err
	}
	updated.Note = note
	updated.Status = status
	updated.LatencyMS = latency.Milliseconds()
	updated.Version = version
	return updated, nil
}

func (s *Store) DeleteConnection(id string) error {
	result, err := s.db.Exec(`DELETE FROM connections WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete connection: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete connection rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) AddStorage(input model.StorageInput) (model.RemoteStorage, error) {
	storage := model.RemoteStorage{
		ID: newID(), Type: normalizeStorageType(input.Type), Name: strings.TrimSpace(input.Name),
		Provider: strings.TrimSpace(input.Provider), Endpoint: strings.TrimSpace(input.Endpoint),
		Bucket: strings.TrimSpace(input.Bucket), Region: strings.TrimSpace(input.Region),
		AccessKey: strings.TrimSpace(input.AccessKey), SecretKey: input.SecretKey,
		Path: strings.TrimSpace(input.Path), AuthMode: strings.TrimSpace(input.AuthMode),
		Note: strings.TrimSpace(input.Note), Status: storageStatusForInput(input), CreatedAt: time.Now().UTC(),
	}
	encrypted, err := s.encrypt(storage.SecretKey)
	if err != nil {
		return model.RemoteStorage{}, err
	}
	if _, err := s.db.Exec(`
		INSERT INTO remote_storages
		(id, type, name, provider, endpoint, bucket, region, access_key, secret_key_encrypted, path, auth_mode, note, status, created_at, favorite)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		storage.ID, storage.Type, storage.Name, storage.Provider, storage.Endpoint, storage.Bucket,
		storage.Region, storage.AccessKey, encrypted, storage.Path, storage.AuthMode, storage.Note,
		storage.Status, storage.CreatedAt.Format(time.RFC3339Nano), storage.Favorite); err != nil {
		return model.RemoteStorage{}, fmt.Errorf("save remote storage: %w", err)
	}
	return storage, nil
}

func (s *Store) Storages() ([]model.RemoteStorage, error) {
	rows, err := s.db.Query(`
		SELECT id, type, name, provider, endpoint, bucket, region, access_key,
		       secret_key_encrypted, path, auth_mode, note, status, created_at, favorite
		FROM remote_storages ORDER BY favorite DESC, created_at`)
	if err != nil {
		return nil, fmt.Errorf("list remote storages: %w", err)
	}
	defer rows.Close()
	var items []model.RemoteStorage
	for rows.Next() {
		item, scanErr := s.scanStorage(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if items == nil {
		items = []model.RemoteStorage{}
	}
	return items, rows.Err()
}

func (s *Store) Storage(id string) (model.RemoteStorage, error) {
	row := s.db.QueryRow(`
		SELECT id, type, name, provider, endpoint, bucket, region, access_key,
		       secret_key_encrypted, path, auth_mode, note, status, created_at, favorite
		FROM remote_storages WHERE id = ?`, id)
	return s.scanStorage(row)
}

func (s *Store) UpdateStorage(id string, input model.UpdateStorageInput) (model.RemoteStorage, error) {
	current, err := s.Storage(id)
	if err != nil {
		return model.RemoteStorage{}, err
	}
	secret := current.SecretKey
	if input.SecretKey != "" {
		secret = input.SecretKey
	}
	encrypted, err := s.encrypt(secret)
	if err != nil {
		return model.RemoteStorage{}, err
	}
	status := current.Status
	if input.Type != "" && normalizeStorageType(input.Type) != current.Type {
		status = storageStatusForInput(model.StorageInput{Type: input.Type, Verified: input.Verified})
	} else if input.Verified {
		status = "connected"
	}
	if _, err := s.db.Exec(`
		UPDATE remote_storages
		SET type = ?, name = ?, provider = ?, endpoint = ?, bucket = ?, region = ?, access_key = ?,
		    secret_key_encrypted = ?, path = ?, auth_mode = ?, note = ?, status = ?
		WHERE id = ?`,
		normalizeStorageType(nonEmpty(input.Type, current.Type)), strings.TrimSpace(input.Name), strings.TrimSpace(input.Provider),
		strings.TrimSpace(input.Endpoint), strings.TrimSpace(input.Bucket), strings.TrimSpace(input.Region),
		strings.TrimSpace(input.AccessKey), encrypted, strings.TrimSpace(input.Path), strings.TrimSpace(input.AuthMode),
		strings.TrimSpace(input.Note), status, id); err != nil {
		return model.RemoteStorage{}, fmt.Errorf("update remote storage: %w", err)
	}
	return s.Storage(id)
}

func (s *Store) DeleteStorage(id string) error {
	result, err := s.db.Exec(`DELETE FROM remote_storages WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete remote storage: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete remote storage rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UpdateStorageStatus(id, status string) error {
	if _, err := s.db.Exec(`UPDATE remote_storages SET status = ? WHERE id = ?`, status, id); err != nil {
		return fmt.Errorf("update remote storage status: %w", err)
	}
	return nil
}

func (s *Store) SetStorageFavorite(id string, favorite bool) (model.RemoteStorage, error) {
	result, err := s.db.Exec(`UPDATE remote_storages SET favorite = ? WHERE id = ?`, favorite, id)
	if err != nil {
		return model.RemoteStorage{}, fmt.Errorf("update remote storage favorite: %w", err)
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		return model.RemoteStorage{}, ErrNotFound
	}
	return s.Storage(id)
}

func (s *Store) AddAuditLog(entry AuditLog) error {
	if entry.ID == "" {
		entry.ID = newID()
	}
	if entry.Time.IsZero() {
		entry.Time = time.Now().UTC()
	}
	if _, err := s.db.Exec(`
		INSERT INTO audit_logs
		(id, time, level, action, resource_type, resource_id, resource_name, connection_name, database_name, status, message, detail)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.ID,
		entry.Time.Format(time.RFC3339Nano),
		entry.Level,
		entry.Action,
		entry.ResourceType,
		entry.ResourceID,
		entry.ResourceName,
		entry.ConnectionName,
		entry.Database,
		entry.Status,
		entry.Message,
		entry.Detail,
	); err != nil {
		return fmt.Errorf("save audit log: %w", err)
	}
	return nil
}

func (s *Store) AuditLogs() ([]AuditLog, error) {
	return s.AuditLogsLimit(0)
}

func (s *Store) AuditLogsLimit(limit int) ([]AuditLog, error) {
	query := `
		SELECT id, time, level, action, resource_type, resource_id, resource_name, connection_name, database_name, status, message, detail
		FROM audit_logs
		ORDER BY time DESC`
	var args []any
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(`
		`+query, args...)
	if err != nil {
		return nil, fmt.Errorf("list audit logs: %w", err)
	}
	defer rows.Close()
	var result []AuditLog
	for rows.Next() {
		var item AuditLog
		var at string
		if err := rows.Scan(&item.ID, &at, &item.Level, &item.Action, &item.ResourceType, &item.ResourceID, &item.ResourceName, &item.ConnectionName, &item.Database, &item.Status, &item.Message, &item.Detail); err != nil {
			return nil, err
		}
		item.Time, err = time.Parse(time.RFC3339Nano, at)
		if err != nil {
			return nil, fmt.Errorf("parse audit log time: %w", err)
		}
		result = append(result, item)
	}
	if result == nil {
		result = []AuditLog{}
	}
	return result, rows.Err()
}

func (s *Store) AddMigration(value model.Migration, cancel context.CancelFunc) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode migration: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec(`INSERT INTO migrations (id, data, updated_at) VALUES (?, ?, ?)`,
		value.ID, data, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("save migration: %w", err)
	}
	s.cancels[value.ID] = cancel
	return nil
}

func (s *Store) Migration(id string) (model.Migration, error) {
	var data []byte
	if err := s.db.QueryRow(`SELECT data FROM migrations WHERE id = ?`, id).Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Migration{}, ErrNotFound
		}
		return model.Migration{}, fmt.Errorf("load migration: %w", err)
	}
	return decodeMigration(data)
}

func (s *Store) Migrations() ([]model.Migration, error) {
	return s.MigrationsLimit(0)
}

func (s *Store) MigrationsLimit(limit int) ([]model.Migration, error) {
	query := `SELECT data FROM migrations ORDER BY updated_at DESC`
	var args []any
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	defer rows.Close()
	var items []model.Migration
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		item, err := decodeMigration(data)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if items == nil {
		items = []model.Migration{}
	}
	return items, rows.Err()
}

func (s *Store) UpdateMigration(id string, update func(*model.Migration)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var data []byte
	if err := s.db.QueryRow(`SELECT data FROM migrations WHERE id = ?`, id).Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("load migration for update: %w", err)
	}
	value, err := decodeMigration(data)
	if err != nil {
		return err
	}
	update(&value)
	data, err = json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode migration update: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE migrations SET data = ?, updated_at = ? WHERE id = ?`,
		data, time.Now().UTC().Format(time.RFC3339Nano), id); err != nil {
		return fmt.Errorf("update migration: %w", err)
	}
	return nil
}

func (s *Store) SetMigrationFavorite(id string, favorite bool) (model.Migration, error) {
	if err := s.UpdateMigration(id, func(value *model.Migration) { value.Favorite = favorite }); err != nil {
		return model.Migration{}, err
	}
	return s.Migration(id)
}

func (s *Store) CancelMigration(id string) error {
	if _, err := s.Migration(id); err != nil {
		return err
	}
	s.mu.Lock()
	cancel := s.cancels[id]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (s *Store) ClearMigrationCancel(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cancels, id)
}

func (s *Store) DeleteMigration(id string) error {
	if _, err := s.Migration(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cancels, id)
	result, err := s.db.Exec(`DELETE FROM migrations WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete migration: %w", err)
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) AddBackup(value model.BackupJob, cancel context.CancelFunc) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode backup: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec(`INSERT INTO backups (id, data, updated_at) VALUES (?, ?, ?)`,
		value.ID, data, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("save backup: %w", err)
	}
	s.cancels[value.ID] = cancel
	return nil
}

func (s *Store) Backup(id string) (model.BackupJob, error) {
	var data []byte
	if err := s.db.QueryRow(`SELECT data FROM backups WHERE id = ?`, id).Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.BackupJob{}, ErrNotFound
		}
		return model.BackupJob{}, fmt.Errorf("load backup: %w", err)
	}
	return decodeBackup(data)
}

func (s *Store) Backups() ([]model.BackupJob, error) {
	rows, err := s.db.Query(`SELECT data FROM backups ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list backups: %w", err)
	}
	defer rows.Close()
	var items []model.BackupJob
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		item, err := decodeBackup(data)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if items == nil {
		items = []model.BackupJob{}
	}
	return items, rows.Err()
}

func (s *Store) UpdateBackup(id string, update func(*model.BackupJob)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var data []byte
	if err := s.db.QueryRow(`SELECT data FROM backups WHERE id = ?`, id).Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("load backup for update: %w", err)
	}
	value, err := decodeBackup(data)
	if err != nil {
		return err
	}
	update(&value)
	data, err = json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode backup update: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE backups SET data = ?, updated_at = ? WHERE id = ?`,
		data, time.Now().UTC().Format(time.RFC3339Nano), id); err != nil {
		return fmt.Errorf("update backup: %w", err)
	}
	return nil
}

func (s *Store) SetBackupFavorite(id string, favorite bool) (model.BackupJob, error) {
	if err := s.UpdateBackup(id, func(value *model.BackupJob) { value.Favorite = favorite }); err != nil {
		return model.BackupJob{}, err
	}
	return s.Backup(id)
}

func (s *Store) CancelBackup(id string) error {
	if _, err := s.Backup(id); err != nil {
		return err
	}
	s.mu.Lock()
	cancel := s.cancels[id]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (s *Store) SetBackupCancel(id string, cancel context.CancelFunc) error {
	if _, err := s.Backup(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancels[id] = cancel
	return nil
}

func (s *Store) ClearBackupCancel(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cancels, id)
}

func (s *Store) DeleteBackup(id string) error {
	if _, err := s.Backup(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cancels, id)
	result, err := s.db.Exec(`DELETE FROM backups WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete backup: %w", err)
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		return ErrNotFound
	}
	return nil
}

func NewID() string { return newID() }

func (s *Store) initialize() error {
	if _, err := s.db.Exec(`
		PRAGMA journal_mode = WAL;
		PRAGMA foreign_keys = ON;
		CREATE TABLE IF NOT EXISTS connections (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			note TEXT NOT NULL DEFAULT '',
			type TEXT NOT NULL,
			host TEXT NOT NULL,
			port INTEGER NOT NULL,
			username TEXT NOT NULL,
			password_encrypted BLOB NOT NULL,
			ssl_mode TEXT NOT NULL,
			tls_config_encrypted BLOB,
			endpoints_json BLOB NOT NULL DEFAULT '[]',
			status TEXT NOT NULL,
			latency_ms INTEGER NOT NULL,
			server_version TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			favorite INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS migrations (
			id TEXT PRIMARY KEY,
			data BLOB NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS remote_storages (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			name TEXT NOT NULL,
			provider TEXT NOT NULL,
			endpoint TEXT NOT NULL DEFAULT '',
			bucket TEXT NOT NULL DEFAULT '',
			region TEXT NOT NULL DEFAULT '',
			access_key TEXT NOT NULL DEFAULT '',
			secret_key_encrypted BLOB NOT NULL,
			path TEXT NOT NULL DEFAULT '',
			auth_mode TEXT NOT NULL DEFAULT '',
			note TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			created_at TEXT NOT NULL,
			favorite INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS backups (
			id TEXT PRIMARY KEY,
			data BLOB NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS audit_logs (
			id TEXT PRIMARY KEY,
			time TEXT NOT NULL,
			level TEXT NOT NULL,
			action TEXT NOT NULL,
			resource_type TEXT NOT NULL,
			resource_id TEXT NOT NULL,
			resource_name TEXT NOT NULL,
			connection_name TEXT NOT NULL DEFAULT '',
			database_name TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			message TEXT NOT NULL,
			detail TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE IF NOT EXISTS app_settings (
			key TEXT PRIMARY KEY,
			data BLOB NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS notification_recipients (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			type TEXT NOT NULL,
			target_encrypted BLOB NOT NULL,
			message_title TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
	`); err != nil {
		return fmt.Errorf("initialize sqlite: %w", err)
	}
	if err := s.ensureConnectionVersionColumn(); err != nil {
		return err
	}
	if err := s.ensureConnectionNoteColumn(); err != nil {
		return err
	}
	if err := s.ensureConnectionTLSConfigColumn(); err != nil {
		return err
	}
	if err := s.ensureConnectionEndpointsColumn(); err != nil {
		return err
	}
	if err := s.ensureConnectionFavoriteColumn(); err != nil {
		return err
	}
	if err := s.ensureStorageFavoriteColumn(); err != nil {
		return err
	}
	if err := s.ensureNotificationRecipientMessageTitleColumn(); err != nil {
		return err
	}
	if err := s.recoverInterruptedMigrations(); err != nil {
		return err
	}
	return s.recoverInterruptedBackups()
}

func (s *Store) ensureConnectionVersionColumn() error {
	if _, err := s.db.Exec(`ALTER TABLE connections ADD COLUMN server_version TEXT NOT NULL DEFAULT ''`); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return nil
		}
		return fmt.Errorf("migrate connections.server_version: %w", err)
	}
	return nil
}

func (s *Store) ensureConnectionNoteColumn() error {
	if _, err := s.db.Exec(`ALTER TABLE connections ADD COLUMN note TEXT NOT NULL DEFAULT ''`); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return nil
		}
		return fmt.Errorf("migrate connections.note: %w", err)
	}
	return nil
}

func (s *Store) ensureConnectionTLSConfigColumn() error {
	if _, err := s.db.Exec(`ALTER TABLE connections ADD COLUMN tls_config_encrypted BLOB`); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return nil
		}
		return fmt.Errorf("migrate connections.tls_config_encrypted: %w", err)
	}
	return nil
}

func (s *Store) ensureConnectionEndpointsColumn() error {
	if _, err := s.db.Exec(`ALTER TABLE connections ADD COLUMN endpoints_json BLOB NOT NULL DEFAULT '[]'`); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return nil
		}
		return fmt.Errorf("migrate connections.endpoints_json: %w", err)
	}
	return nil
}

func (s *Store) ensureConnectionFavoriteColumn() error {
	if _, err := s.db.Exec(`ALTER TABLE connections ADD COLUMN favorite INTEGER NOT NULL DEFAULT 0`); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return nil
		}
		return fmt.Errorf("migrate connections.favorite: %w", err)
	}
	return nil
}

func (s *Store) ensureStorageFavoriteColumn() error {
	if _, err := s.db.Exec(`ALTER TABLE remote_storages ADD COLUMN favorite INTEGER NOT NULL DEFAULT 0`); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return nil
		}
		return fmt.Errorf("migrate remote_storages.favorite: %w", err)
	}
	return nil
}

func (s *Store) ensureNotificationRecipientMessageTitleColumn() error {
	if _, err := s.db.Exec(`ALTER TABLE notification_recipients ADD COLUMN message_title TEXT NOT NULL DEFAULT ''`); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return nil
		}
		return fmt.Errorf("migrate notification_recipients.message_title: %w", err)
	}
	return nil
}

func (s *Store) recoverInterruptedMigrations() error {
	items, err := s.Migrations()
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Status != model.StatusPending && item.Status != model.StatusRunning {
			continue
		}
		finished := time.Now().UTC()
		if err := s.UpdateMigration(item.ID, func(value *model.Migration) {
			value.Status = model.StatusFailed
			value.Stage = "failed"
			value.Error = "API 服务重启，运行中的迁移任务已中断"
			value.FinishedAt = &finished
			value.Events = append(value.Events, model.Event{
				Time: finished, Level: "ERROR", Message: value.Error,
			})
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) recoverInterruptedBackups() error {
	items, err := s.Backups()
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Status != model.StatusPending && item.Status != model.StatusRunning {
			continue
		}
		finished := time.Now().UTC()
		if err := s.UpdateBackup(item.ID, func(value *model.BackupJob) {
			const interrupted = "API 服务重启，运行中的备份任务已中断"
			value.Error = interrupted
			value.FinishedAt = &finished
			for index := range value.ArtifactVersions {
				version := &value.ArtifactVersions[index]
				if version.Status != model.StatusPending && version.Status != model.StatusRunning {
					continue
				}
				version.Status = model.StatusFailed
				version.Stage = "failed"
				version.Error = interrupted
				version.FinishedAt = &finished
			}
			if value.ScheduleMode == "scheduled" {
				// Discard the stale due time. The API scheduler will calculate the
				// next future occurrence instead of immediately retrying a backup
				// that was interrupted (for example by an OOM restart).
				value.Status = model.StatusScheduled
				value.Stage = "scheduled"
				value.NextRunAt = nil
			} else {
				value.Status = model.StatusFailed
				value.Stage = "failed"
			}
			value.Events = append(value.Events, model.Event{
				Time: finished, Level: "ERROR", Message: interrupted,
			})
		}); err != nil {
			return err
		}
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func (s *Store) scanConnection(row scanner) (model.Connection, error) {
	var item model.Connection
	var encrypted []byte
	var encryptedTLSConfig []byte
	var endpointsJSON []byte
	var createdAt string
	if err := row.Scan(&item.ID, &item.Name, &item.Note, &item.Type, &item.Host, &item.Port,
		&item.Username, &encrypted, &item.SSLMode, &encryptedTLSConfig, &endpointsJSON, &item.Status, &item.LatencyMS,
		&item.Version, &createdAt, &item.Favorite); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Connection{}, ErrNotFound
		}
		return model.Connection{}, fmt.Errorf("scan connection: %w", err)
	}
	password, err := s.decrypt(encrypted)
	if err != nil {
		return model.Connection{}, fmt.Errorf("decrypt connection %s: %w", item.ID, err)
	}
	item.Password = password
	item.TLSConfig, err = s.decryptTLSConfig(encryptedTLSConfig)
	if err != nil {
		return model.Connection{}, fmt.Errorf("decrypt connection TLS config %s: %w", item.ID, err)
	}
	if len(endpointsJSON) > 0 {
		if err := json.Unmarshal(endpointsJSON, &item.Endpoints); err != nil {
			return model.Connection{}, fmt.Errorf("decode connection endpoints %s: %w", item.ID, err)
		}
	}
	item.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return model.Connection{}, fmt.Errorf("parse connection creation time: %w", err)
	}
	return item, nil
}

func (s *Store) scanStorage(row scanner) (model.RemoteStorage, error) {
	var item model.RemoteStorage
	var encrypted []byte
	var createdAt string
	if err := row.Scan(&item.ID, &item.Type, &item.Name, &item.Provider, &item.Endpoint, &item.Bucket,
		&item.Region, &item.AccessKey, &encrypted, &item.Path, &item.AuthMode, &item.Note,
		&item.Status, &createdAt, &item.Favorite); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.RemoteStorage{}, ErrNotFound
		}
		return model.RemoteStorage{}, fmt.Errorf("scan remote storage: %w", err)
	}
	secret, err := s.decrypt(encrypted)
	if err != nil {
		return model.RemoteStorage{}, fmt.Errorf("decrypt remote storage %s: %w", item.ID, err)
	}
	item.SecretKey = secret
	item.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return model.RemoteStorage{}, fmt.Errorf("parse remote storage creation time: %w", err)
	}
	return item, nil
}

func normalizeStorageType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "object", "object_storage", "object-storage", "s3":
		return "object"
	case "webdav", "web_dav", "web-dav":
		return "webdav"
	case "google_drive", "google-drive", "gdrive":
		return "google_drive"
	case "onedrive", "one_drive", "one-drive":
		return "one_drive"
	case "local":
		return "local"
	default:
		return strings.TrimSpace(value)
	}
}

func storageStatusForInput(input model.StorageInput) string {
	switch normalizeStorageType(input.Type) {
	case "google_drive", "one_drive":
		return "needs_auth"
	case "object":
		if input.Verified {
			return "connected"
		}
		return "untested"
	default:
		return "untested"
	}
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func (s *Store) encrypt(value string) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("create credential nonce: %w", err)
	}
	return s.aead.Seal(nonce, nonce, []byte(value), nil), nil
}

func (s *Store) decrypt(value []byte) (string, error) {
	if len(value) < s.aead.NonceSize() {
		return "", errors.New("encrypted credential is truncated")
	}
	nonce, ciphertext := value[:s.aead.NonceSize()], value[s.aead.NonceSize():]
	plaintext, err := s.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func (s *Store) encryptTLSConfig(config model.ConnectionTLSConfig) ([]byte, error) {
	config = dbtls.Normalize(config)
	if config.Empty() {
		return nil, nil
	}
	data, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode connection TLS config: %w", err)
	}
	return s.encrypt(string(data))
}

func (s *Store) decryptTLSConfig(value []byte) (model.ConnectionTLSConfig, error) {
	if len(value) == 0 {
		return model.ConnectionTLSConfig{}, nil
	}
	data, err := s.decrypt(value)
	if err != nil {
		return model.ConnectionTLSConfig{}, err
	}
	var config model.ConnectionTLSConfig
	if err := json.Unmarshal([]byte(data), &config); err != nil {
		return model.ConnectionTLSConfig{}, fmt.Errorf("decode connection TLS config: %w", err)
	}
	return dbtls.Normalize(config), nil
}

func loadOrCreateKey(path string) ([]byte, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create key directory: %w", err)
	}
	key, err := os.ReadFile(path)
	if err == nil {
		if len(key) != 32 {
			return nil, fmt.Errorf("credential key %s must be exactly 32 bytes", path)
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read credential key: %w", err)
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate credential key: %w", err)
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, fmt.Errorf("write credential key: %w", err)
	}
	return key, nil
}

func newID() string {
	var data [8]byte
	_, _ = rand.Read(data[:])
	return hex.EncodeToString(data[:])
}

func decodeMigration(data []byte) (model.Migration, error) {
	var value model.Migration
	if err := json.Unmarshal(data, &value); err != nil {
		return model.Migration{}, fmt.Errorf("decode migration: %w", err)
	}
	if value.Results == nil {
		value.Results = []model.TableResult{}
	}
	if value.Events == nil {
		value.Events = []model.Event{}
	}
	return value, nil
}

func decodeBackup(data []byte) (model.BackupJob, error) {
	var value model.BackupJob
	if err := json.Unmarshal(data, &value); err != nil {
		return model.BackupJob{}, fmt.Errorf("decode backup: %w", err)
	}
	if value.Events == nil {
		value.Events = []model.Event{}
	}
	return value, nil
}
