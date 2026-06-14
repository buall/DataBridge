package model

import (
	"encoding/json"
	"time"
)

type ConnectionInput struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	SSLMode  string `json:"sslMode"`
}

type Connection struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Host      string    `json:"host"`
	Port      int       `json:"port"`
	Username  string    `json:"username"`
	SSLMode   string    `json:"sslMode"`
	Status    string    `json:"status"`
	LatencyMS int64     `json:"latency"`
	CreatedAt time.Time `json:"createdAt"`
	Password  string    `json:"-"`
}

type DatabaseInfo struct {
	Name string `json:"name"`
	Size int64  `json:"sizeBytes"`
}

type TableInfo struct {
	ID        string `json:"id"`
	Schema    string `json:"schema"`
	Name      string `json:"name"`
	SizeBytes int64  `json:"sizeBytes"`
	Rows      int64  `json:"estimatedRows"`
}

type MigrationRequest struct {
	Name             string   `json:"name"`
	SourceID         string   `json:"sourceId"`
	SourceDatabase   string   `json:"sourceDatabase"`
	TargetID         string   `json:"targetId"`
	TargetDatabase   string   `json:"targetDatabase"`
	Tables           []string `json:"tables"`
	ConflictStrategy string   `json:"conflictStrategy"`
}

type MigrationStatus string

const (
	StatusPending   MigrationStatus = "pending"
	StatusRunning   MigrationStatus = "running"
	StatusCompleted MigrationStatus = "completed"
	StatusFailed    MigrationStatus = "failed"
	StatusCancelled MigrationStatus = "cancelled"
)

type TableResult struct {
	Table      string        `json:"table"`
	SourceRows int64         `json:"sourceRows"`
	TargetRows int64         `json:"targetRows"`
	CopiedRows int64         `json:"copiedRows"`
	Duration   time.Duration `json:"durationMs"`
	Valid      bool          `json:"valid"`
	Error      string        `json:"error,omitempty"`
}

func (r TableResult) MarshalJSON() ([]byte, error) {
	type wire struct {
		Table      string `json:"table"`
		SourceRows int64  `json:"sourceRows"`
		TargetRows int64  `json:"targetRows"`
		CopiedRows int64  `json:"copiedRows"`
		DurationMS int64  `json:"durationMs"`
		Valid      bool   `json:"valid"`
		Error      string `json:"error,omitempty"`
	}
	return marshal(wire{
		Table: r.Table, SourceRows: r.SourceRows, TargetRows: r.TargetRows,
		CopiedRows: r.CopiedRows, DurationMS: r.Duration.Milliseconds(),
		Valid: r.Valid, Error: r.Error,
	})
}

func (r *TableResult) UnmarshalJSON(data []byte) error {
	type wire struct {
		Table      string `json:"table"`
		SourceRows int64  `json:"sourceRows"`
		TargetRows int64  `json:"targetRows"`
		CopiedRows int64  `json:"copiedRows"`
		DurationMS int64  `json:"durationMs"`
		Valid      bool   `json:"valid"`
		Error      string `json:"error,omitempty"`
	}
	var value wire
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	r.Table = value.Table
	r.SourceRows = value.SourceRows
	r.TargetRows = value.TargetRows
	r.CopiedRows = value.CopiedRows
	r.Duration = time.Duration(value.DurationMS) * time.Millisecond
	r.Valid = value.Valid
	r.Error = value.Error
	return nil
}

type Event struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

type Migration struct {
	ID             string           `json:"id"`
	Name           string           `json:"name"`
	Status         MigrationStatus  `json:"status"`
	Stage          string           `json:"stage"`
	Progress       int              `json:"progress"`
	CurrentTable   string           `json:"currentTable,omitempty"`
	TablesTotal    int              `json:"tablesTotal"`
	TablesDone     int              `json:"tablesDone"`
	RowsTotal      int64            `json:"rowsTotal"`
	RowsCopied     int64            `json:"rowsCopied"`
	StartedAt      time.Time        `json:"startedAt"`
	FinishedAt     *time.Time       `json:"finishedAt,omitempty"`
	Error          string           `json:"error,omitempty"`
	SourceName     string           `json:"sourceName"`
	TargetName     string           `json:"targetName"`
	SourceDatabase string           `json:"sourceDatabase"`
	TargetDatabase string           `json:"targetDatabase"`
	Results        []TableResult    `json:"results"`
	Events         []Event          `json:"events"`
	Request        MigrationRequest `json:"-"`
}
