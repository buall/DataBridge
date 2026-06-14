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

func (s *Store) AddConnection(input model.ConnectionInput, latency time.Duration) (model.Connection, error) {
	connection := model.Connection{
		ID: newID(), Name: input.Name, Type: "PostgreSQL", Host: input.Host,
		Port: input.Port, Username: input.Username, Password: input.Password,
		SSLMode: input.SSLMode, Status: "connected",
		LatencyMS: latency.Milliseconds(), CreatedAt: time.Now().UTC(),
	}
	encrypted, err := s.encrypt(input.Password)
	if err != nil {
		return model.Connection{}, err
	}
	_, err = s.db.Exec(`
		INSERT INTO connections
		(id, name, type, host, port, username, password_encrypted, ssl_mode, status, latency_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		connection.ID, connection.Name, connection.Type, connection.Host, connection.Port,
		connection.Username, encrypted, connection.SSLMode, connection.Status,
		connection.LatencyMS, connection.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return model.Connection{}, fmt.Errorf("save connection: %w", err)
	}
	return connection, nil
}

func (s *Store) Connections() ([]model.Connection, error) {
	rows, err := s.db.Query(`
		SELECT id, name, type, host, port, username, password_encrypted,
		       ssl_mode, status, latency_ms, created_at
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
		SELECT id, name, type, host, port, username, password_encrypted,
		       ssl_mode, status, latency_ms, created_at
		FROM connections WHERE id = ?`, id)
	return s.scanConnection(row)
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
			type TEXT NOT NULL,
			host TEXT NOT NULL,
			port INTEGER NOT NULL,
			username TEXT NOT NULL,
			password_encrypted BLOB NOT NULL,
			ssl_mode TEXT NOT NULL,
			status TEXT NOT NULL,
			latency_ms INTEGER NOT NULL,
			created_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS migrations (
			id TEXT PRIMARY KEY,
			data BLOB NOT NULL,
			updated_at TEXT NOT NULL
		);
	`); err != nil {
		return fmt.Errorf("initialize sqlite: %w", err)
	}
	return s.recoverInterruptedMigrations()
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
	if err := row.Scan(&item.ID, &item.Name, &item.Type, &item.Host, &item.Port,
		&item.Username, &encrypted, &item.SSLMode, &item.Status, &item.LatencyMS,
		&createdAt); err != nil {
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
