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

	"databridge/internal/model"
	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	db      *sql.DB
	aead    cipher.AEAD
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

type AuditLog struct {
	ID             string
	Time           time.Time
	Level          string
	Action         string
	ResourceType   string
	ResourceID     string
	ResourceName   string
	ConnectionName string
	Database       string
	Status         string
	Message        string
	Detail         string
}

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

func (s *Store) AddConnection(input model.ConnectionInput, latency time.Duration, version string) (model.Connection, error) {
	connection := model.Connection{
		ID: newID(), Name: input.Name, Note: strings.TrimSpace(input.Note), Type: input.Type, Host: input.Host,
		Port: input.Port, Username: input.Username, Password: input.Password,
		SSLMode: input.SSLMode, Status: "connected",
		LatencyMS: latency.Milliseconds(), Version: version, CreatedAt: time.Now().UTC(),
	}
	encrypted, err := s.encrypt(input.Password)
	if err != nil {
		return model.Connection{}, err
	}
	_, err = s.db.Exec(`
		INSERT INTO connections
		(id, name, note, type, host, port, username, password_encrypted, ssl_mode, status, latency_ms, server_version, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		connection.ID, connection.Name, connection.Note, connection.Type, connection.Host, connection.Port,
		connection.Username, encrypted, connection.SSLMode, connection.Status,
		connection.LatencyMS, connection.Version, connection.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return model.Connection{}, fmt.Errorf("save connection: %w", err)
	}
	return connection, nil
}

func (s *Store) Connections() ([]model.Connection, error) {
	rows, err := s.db.Query(`
		SELECT id, name, note, type, host, port, username, password_encrypted,
		       ssl_mode, status, latency_ms, server_version, created_at
		FROM connections ORDER BY created_at`)
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
		       ssl_mode, status, latency_ms, server_version, created_at
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

func (s *Store) UpdateConnection(id string, input model.UpdateConnectionInput, latency time.Duration, version string) (model.Connection, error) {
	current, err := s.Connection(id)
	if err != nil {
		return model.Connection{}, err
	}
	password := current.Password
	if strings.TrimSpace(input.Password) != "" {
		password = input.Password
	}
	encrypted, err := s.encrypt(password)
	if err != nil {
		return model.Connection{}, err
	}
	note := strings.TrimSpace(input.Note)
	status := "connected"
	if _, err := s.db.Exec(`
		UPDATE connections
		SET name = ?, note = ?, host = ?, port = ?, username = ?, password_encrypted = ?, ssl_mode = ?, status = ?, latency_ms = ?, server_version = ?, type = ?
		WHERE id = ?`,
		input.Name, note, input.Host, input.Port, input.Username, encrypted, input.SSLMode, status, latency.Milliseconds(), version, current.Type, id); err != nil {
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
	rows, err := s.db.Query(`
		SELECT id, time, level, action, resource_type, resource_id, resource_name, connection_name, database_name, status, message, detail
		FROM audit_logs
		ORDER BY time DESC`)
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
	rows, err := s.db.Query(`SELECT data FROM migrations ORDER BY updated_at DESC`)
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
			status TEXT NOT NULL,
			latency_ms INTEGER NOT NULL,
			server_version TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS migrations (
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
	`); err != nil {
		return fmt.Errorf("initialize sqlite: %w", err)
	}
	if err := s.ensureConnectionVersionColumn(); err != nil {
		return err
	}
	if err := s.ensureConnectionNoteColumn(); err != nil {
		return err
	}
	return s.recoverInterruptedMigrations()
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

type scanner interface {
	Scan(dest ...any) error
}

func (s *Store) scanConnection(row scanner) (model.Connection, error) {
	var item model.Connection
	var encrypted []byte
	var createdAt string
	if err := row.Scan(&item.ID, &item.Name, &item.Note, &item.Type, &item.Host, &item.Port,
		&item.Username, &encrypted, &item.SSLMode, &item.Status, &item.LatencyMS,
		&item.Version, &createdAt); err != nil {
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
	item.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return model.Connection{}, fmt.Errorf("parse connection creation time: %w", err)
	}
	return item, nil
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
