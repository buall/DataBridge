export type DatabaseType = "PostgreSQL" | "MySQL" | "Redis";
export type ConnectionStatus = "connected" | "warning";
export type AppView = "overview" | "connections" | "migrations" | "reports" | "logs";
export type MigrationStage = "wizard" | "running" | "report";

export interface DatabaseConnection {
  id: string;
  name: string;
  type: DatabaseType;
  host: string;
  port: number;
  username: string;
  sslMode: string;
  status: ConnectionStatus;
  latency: number;
  createdAt: string;
}

export interface ConnectionInput {
  name: string;
  host: string;
  port: number;
  username: string;
  password: string;
  sslMode: string;
}

export interface DatabaseInfo {
  name: string;
  sizeBytes: number;
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
  sourceId: string;
  sourceDatabase: string;
  targetId: string;
  targetDatabase: string;
  tables: string[];
  conflictStrategy: "backup" | "skip" | "overwrite";
}

export interface MigrationEvent {
  time: string;
  level: string;
  message: string;
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
