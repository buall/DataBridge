package model

import (
	"encoding/json"
	"time"
)

type ConnectionInput struct {
	Type      string              `json:"type"`
	Name      string              `json:"name"`
	Note      string              `json:"note,omitempty"`
	Host      string              `json:"host"`
	Port      int                 `json:"port"`
	Username  string              `json:"username"`
	Password  string              `json:"password"`
	SSLMode   string              `json:"sslMode"`
	TLSConfig ConnectionTLSConfig `json:"tlsConfig,omitempty"`
}

type UpdateConnectionInput struct {
	Type      string              `json:"type,omitempty"`
	Name      string              `json:"name"`
	Note      string              `json:"note,omitempty"`
	Host      string              `json:"host"`
	Port      int                 `json:"port"`
	Username  string              `json:"username"`
	Password  string              `json:"password,omitempty"`
	SSLMode   string              `json:"sslMode"`
	TLSConfig ConnectionTLSConfig `json:"tlsConfig,omitempty"`
}

type DuplicateConnectionInput struct {
	Name      string              `json:"name"`
	Note      string              `json:"note,omitempty"`
	Host      string              `json:"host"`
	Port      int                 `json:"port"`
	Username  string              `json:"username"`
	SSLMode   string              `json:"sslMode"`
	Password  string              `json:"password,omitempty"`
	TLSConfig ConnectionTLSConfig `json:"tlsConfig,omitempty"`
}

type ConnectionTLSConfig struct {
	CACert     string `json:"caCert,omitempty"`
	ClientCert string `json:"clientCert,omitempty"`
	ClientKey  string `json:"clientKey,omitempty"`
}

func (c ConnectionTLSConfig) Empty() bool {
	return c.CACert == "" && c.ClientCert == "" && c.ClientKey == ""
}

type Connection struct {
	ID        string              `json:"id"`
	Name      string              `json:"name"`
	Note      string              `json:"note,omitempty"`
	Type      string              `json:"type"`
	Host      string              `json:"host"`
	Port      int                 `json:"port"`
	Region    string              `json:"region,omitempty"`
	Username  string              `json:"username"`
	SSLMode   string              `json:"sslMode"`
	Status    string              `json:"status"`
	LatencyMS int64               `json:"latency"`
	Version   string              `json:"version,omitempty"`
	CreatedAt time.Time           `json:"createdAt"`
	Password  string              `json:"-"`
	TLSConfig ConnectionTLSConfig `json:"-"`
}

type StorageInput struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	Provider  string `json:"provider"`
	Endpoint  string `json:"endpoint,omitempty"`
	Bucket    string `json:"bucket,omitempty"`
	Region    string `json:"region,omitempty"`
	AccessKey string `json:"accessKey,omitempty"`
	SecretKey string `json:"secretKey,omitempty"`
	Path      string `json:"path,omitempty"`
	AuthMode  string `json:"authMode,omitempty"`
	Note      string `json:"note,omitempty"`
	Verified  bool   `json:"verified,omitempty"`
}

type UpdateStorageInput struct {
	Type      string `json:"type,omitempty"`
	Name      string `json:"name"`
	Provider  string `json:"provider"`
	Endpoint  string `json:"endpoint,omitempty"`
	Bucket    string `json:"bucket,omitempty"`
	Region    string `json:"region,omitempty"`
	AccessKey string `json:"accessKey,omitempty"`
	SecretKey string `json:"secretKey,omitempty"`
	Path      string `json:"path,omitempty"`
	AuthMode  string `json:"authMode,omitempty"`
	Note      string `json:"note,omitempty"`
	Verified  bool   `json:"verified,omitempty"`
}

type RemoteStorage struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Name      string    `json:"name"`
	Provider  string    `json:"provider"`
	Endpoint  string    `json:"endpoint,omitempty"`
	Bucket    string    `json:"bucket,omitempty"`
	Region    string    `json:"region,omitempty"`
	AccessKey string    `json:"accessKey,omitempty"`
	Path      string    `json:"path,omitempty"`
	AuthMode  string    `json:"authMode,omitempty"`
	Note      string    `json:"note,omitempty"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
	SecretKey string    `json:"-"`
}

type DatabaseInfo struct {
	Name          string `json:"name"`
	Size          int64  `json:"sizeBytes"`
	Nodes         int64  `json:"nodes,omitempty"`
	Relationships int64  `json:"relationships,omitempty"`
}

type CreateDatabaseRequest struct {
	Name string `json:"name"`
}

type SeedDataRequest struct {
	Database string `json:"database"`
	Rows     int    `json:"rows"`
	Mode     string `json:"mode,omitempty"`
	Template string `json:"template,omitempty"`
}

type SeedDataResult struct {
	Database string `json:"database"`
	Table    string `json:"table"`
	Rows     int    `json:"rows"`
	Mode     string `json:"mode"`
	Template string `json:"template"`
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
	Engine           string   `json:"engine"`
	SourceID         string   `json:"sourceId"`
	SourceDatabase   string   `json:"sourceDatabase"`
	TargetID         string   `json:"targetId"`
	TargetDatabase   string   `json:"targetDatabase"`
	Tables           []string `json:"tables"`
	ConflictStrategy string   `json:"conflictStrategy"`
}

type BackupRequest struct {
	Name                string             `json:"name"`
	SourceID            string             `json:"sourceId"`
	SourceDatabase      string             `json:"sourceDatabase"`
	SourceDatabases     []string           `json:"sourceDatabases,omitempty"`
	DestinationType     string             `json:"destinationType"`
	Destination         string             `json:"destination"`
	ScheduleMode        string             `json:"scheduleMode,omitempty"`
	ScheduleFrequency   string             `json:"scheduleFrequency,omitempty"`
	ScheduleTime        string             `json:"scheduleTime,omitempty"`
	ScheduleWeekday     int                `json:"scheduleWeekday,omitempty"`
	ScheduleMonthDay    int                `json:"scheduleMonthDay,omitempty"`
	RetentionDays       int                `json:"retentionDays,omitempty"`
	RetentionMonths     int                `json:"retentionMonths,omitempty"`
	Notification        BackupNotification `json:"notification,omitempty"`
	NotificationEnabled bool               `json:"notificationEnabled,omitempty"`
	NotificationMethod  string             `json:"notificationMethod,omitempty"`
	NotificationTarget  string             `json:"notificationTarget,omitempty"`
	Storage             *RemoteStorage     `json:"-"`
}

type RestoreBackupRequest struct {
	TargetID          string            `json:"targetId"`
	TargetDatabase    string            `json:"targetDatabase"`
	ArtifactVersionID string            `json:"artifactVersionId,omitempty"`
	SourceDatabases   []string          `json:"sourceDatabases,omitempty"`
	SelectedDatabases []string          `json:"selectedDatabases,omitempty"`
	TargetDatabases   map[string]string `json:"targetDatabases,omitempty"`
	Strategy          string            `json:"strategy,omitempty"`
}

type RestorePrecheckResult struct {
	OK       bool                  `json:"ok"`
	Checks   []RestorePrecheckItem `json:"checks"`
	Warnings []RestoreWarning      `json:"warnings,omitempty"`
}

type RestorePrecheckItem struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type RestoreWarning struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Message string `json:"message"`
}

type BackupNotification struct {
	Enabled bool   `json:"enabled,omitempty"`
	Method  string `json:"method,omitempty"`
	Target  string `json:"target,omitempty"`
}

type BackupNotificationTestRequest struct {
	Method string `json:"method"`
	Target string `json:"target"`
}

type BackupArtifact struct {
	Database string `json:"database"`
	Name     string `json:"name"`
	Size     int64  `json:"size,omitempty"`
	Path     string `json:"path,omitempty"`
}

type BackupArtifactVersion struct {
	ID         string           `json:"id"`
	StartedAt  time.Time        `json:"startedAt"`
	FinishedAt *time.Time       `json:"finishedAt,omitempty"`
	Status     MigrationStatus  `json:"status"`
	Stage      string           `json:"stage,omitempty"`
	Artifacts  []BackupArtifact `json:"artifacts,omitempty"`
	Error      string           `json:"error,omitempty"`
}

type SMTPSettings struct {
	Enabled     bool   `json:"enabled"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username,omitempty"`
	Password    string `json:"password,omitempty"`
	PasswordSet bool   `json:"passwordSet,omitempty"`
	From        string `json:"from"`
}

type MigrationStatus string

const (
	StatusScheduled MigrationStatus = "scheduled"
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

type BackupJob struct {
	ID                  string                  `json:"id"`
	Name                string                  `json:"name"`
	Status              MigrationStatus         `json:"status"`
	Stage               string                  `json:"stage"`
	Progress            int                     `json:"progress"`
	StartedAt           time.Time               `json:"startedAt"`
	FinishedAt          *time.Time              `json:"finishedAt,omitempty"`
	Error               string                  `json:"error,omitempty"`
	SourceName          string                  `json:"sourceName"`
	SourceType          string                  `json:"sourceType"`
	SourceDatabase      string                  `json:"sourceDatabase"`
	DestinationType     string                  `json:"destinationType"`
	Destination         string                  `json:"destination"`
	ScheduleMode        string                  `json:"scheduleMode,omitempty"`
	ScheduleFrequency   string                  `json:"scheduleFrequency,omitempty"`
	ScheduleTime        string                  `json:"scheduleTime,omitempty"`
	ScheduleWeekday     int                     `json:"scheduleWeekday,omitempty"`
	ScheduleMonthDay    int                     `json:"scheduleMonthDay,omitempty"`
	RetentionDays       int                     `json:"retentionDays,omitempty"`
	RetentionMonths     int                     `json:"retentionMonths,omitempty"`
	Notification        BackupNotification      `json:"notification,omitempty"`
	NotificationEnabled bool                    `json:"notificationEnabled,omitempty"`
	NotificationMethod  string                  `json:"notificationMethod,omitempty"`
	NotificationTarget  string                  `json:"notificationTarget,omitempty"`
	NextRunAt           *time.Time              `json:"nextRunAt,omitempty"`
	LastRunAt           *time.Time              `json:"lastRunAt,omitempty"`
	ArtifactName        string                  `json:"artifactName,omitempty"`
	ArtifactSize        int64                   `json:"artifactSize,omitempty"`
	ArtifactPath        string                  `json:"artifactPath,omitempty"`
	Artifacts           []BackupArtifact        `json:"artifacts,omitempty"`
	ArtifactVersions    []BackupArtifactVersion `json:"artifactVersions,omitempty"`
	Events              []Event                 `json:"events"`
	Request             BackupRequest           `json:"request,omitempty"`
}
