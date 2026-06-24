export type DatabaseType = "PostgreSQL" | "MySQL" | "Redis";
export type ConnectionStatus = "connected" | "warning";
export type AppView = "overview" | "connections" | "storages" | "migrations" | "backups" | "logs" | "settings";
export type MigrationStage = "list" | "wizard" | "running" | "report" | "restore";

export interface DatabaseConnection {
  id: string;
  name: string;
  note?: string;
  type: DatabaseType;
  host: string;
  port: number;
  region?: string;
  username: string;
  sslMode: string;
  status: ConnectionStatus;
  latency: number;
  version?: string;
  createdAt: string;
}

export interface ConnectionInput {
  type: DatabaseType;
  name: string;
  note?: string;
  host: string;
  port: number;
  username: string;
  password: string;
  sslMode: string;
}

export type RemoteStorageType = "object" | "google_drive" | "one_drive" | "local";
export type RemoteStorageStatus = "connected" | "warning" | "needs_auth" | "untested";

export interface RemoteStorage {
  id: string;
  type: RemoteStorageType;
  name: string;
  provider: string;
  endpoint?: string;
  bucket?: string;
  region?: string;
  accessKey?: string;
  path?: string;
  authMode?: string;
  note?: string;
  status: RemoteStorageStatus;
  createdAt: string;
}

export interface RemoteStorageInput {
  type: RemoteStorageType;
  name: string;
  provider: string;
  endpoint?: string;
  bucket?: string;
  region?: string;
  accessKey?: string;
  secretKey?: string;
  path?: string;
  authMode?: string;
  note?: string;
  verified?: boolean;
}

export interface DatabaseInfo {
  name: string;
  sizeBytes: number;
}

export interface SeedDataResult {
  database: string;
  table: string;
  rows: number;
  mode: string;
  template: string;
}

export interface TableInfo {
  id: string;
  schema: string;
  name: string;
  sizeBytes: number;
  estimatedRows: number;
}

export interface DatabaseObject {
  id: string;
  name: string;
  size: string;
  objects: string;
  selected: boolean;
}

export interface MigrationConfig {
  name: string;
  engine: "native" | "copy";
  sourceId: string;
  sourceDatabase: string;
  targetId: string;
  targetDatabase: string;
  tables: string[];
  conflictStrategy: "backup" | "skip" | "overwrite";
}

export interface BackupConfig {
  name: string;
  sourceId: string;
  sourceDatabase: string;
  sourceDatabases: string[];
  destinationType: "storage" | "local" | "object";
  destination: string;
  scheduleMode: "once" | "scheduled";
  scheduleFrequency?: "daily" | "weekly" | "monthly";
  scheduleTime?: string;
  scheduleWeekday?: number;
  scheduleMonthDay?: number;
  retentionDays?: number;
  retentionMonths?: number;
  notificationEnabled?: boolean;
  notificationMethod?: "webhook" | "email";
  notificationTarget?: string;
}

export interface RestoreBackupConfig {
  targetId: string;
  targetDatabase: string;
  artifactVersionId?: string;
  selectedDatabases?: string[];
  targetDatabases?: Record<string, string>;
  strategy?: "append" | "recreate";
}

export interface RestorePrecheckResult {
  ok: boolean;
  checks: Array<{
    key: string;
    label: string;
    status: "passed" | "failed" | "warning";
    message: string;
  }>;
  warnings?: string[];
}

export interface SMTPSettings {
  enabled: boolean;
  host: string;
  port: number;
  username?: string;
  password?: string;
  passwordSet?: boolean;
  from: string;
}

export interface MigrationEvent {
  time: string;
  level: string;
  message: string;
}

export interface AuditLogEntry {
  id: string;
  time: string;
  level: string;
  action: string;
  resourceType: string;
  resourceID: string;
  resourceName: string;
  connectionName: string;
  database: string;
  status: string;
  message: string;
  detail: string;
}

export interface TableResult {
  table: string;
  sourceRows: number;
  targetRows: number;
  copiedRows: number;
  durationMs: number;
  valid: boolean;
  error?: string;
}

export interface MigrationJob {
  id: string;
  name: string;
  status: "pending" | "running" | "completed" | "failed" | "cancelled";
  stage: string;
  progress: number;
  currentTable?: string;
  tablesTotal: number;
  tablesDone: number;
  rowsTotal: number;
  rowsCopied: number;
  startedAt: string;
  finishedAt?: string;
  error?: string;
  sourceName: string;
  targetName: string;
  sourceDatabase: string;
  targetDatabase: string;
  results: TableResult[];
  events: MigrationEvent[];
}

export interface BackupJob {
  id: string;
  name: string;
  status: MigrationJob["status"] | "scheduled";
  stage: string;
  progress: number;
  startedAt: string;
  finishedAt?: string;
  error?: string;
  sourceName: string;
  sourceType: string;
  sourceDatabase: string;
  destinationType: string;
  destination: string;
  scheduleMode?: "once" | "scheduled";
  scheduleFrequency?: "daily" | "weekly" | "monthly";
  scheduleTime?: string;
  scheduleWeekday?: number;
  scheduleMonthDay?: number;
  retentionDays?: number;
  retentionMonths?: number;
  notificationEnabled?: boolean;
  notificationMethod?: "webhook" | "email";
  notificationTarget?: string;
  nextRunAt?: string;
  lastRunAt?: string;
  artifactName?: string;
  artifactSize?: number;
  artifactPath?: string;
  artifacts?: Array<{ database: string; name: string; size?: number; path?: string }>;
  artifactVersions?: Array<{ id: string; startedAt: string; finishedAt?: string; status: BackupJob["status"]; stage?: string; artifacts?: Array<{ database: string; name: string; size?: number; path?: string }>; error?: string }>;
  events: MigrationEvent[];
}
