import type {
  ConnectionInput,
  AuditLogEntry,
  BackupConfig,
  BackupJob,
  DatabaseConnection,
  DatabaseInfo,
  MigrationConfig,
  MigrationJob,
  RemoteStorage,
  RemoteStorageInput,
  SeedDataResult,
  SMTPSettings,
  TableInfo,
} from "./types";

const API_BASE = import.meta.env.VITE_API_BASE ?? "/api";

export type AuthSession = {
  authenticated: boolean;
  username: string;
  configured: boolean;
};

export type MigrationsResponse = {
  migrations: MigrationJob[];
  auditLogs?: AuditLogEntry[];
};

function normalizeBackup(job: BackupJob): BackupJob {
  return {
    ...job,
    events: job.events ?? [],
  };
}

function normalizeMigration(job: MigrationJob): MigrationJob {
  return {
    ...job,
    results: job.results ?? [],
    events: job.events ?? [],
  };
}

function formatClientError(error: unknown): Error {
  if (error instanceof Error) {
    const message = error.message.trim();
    const lower = message.toLowerCase();
    if (lower === "failed to fetch" || lower.includes("networkerror") || lower.includes("load failed")) {
      return Object.assign(new Error("无法连接到服务，请确认 DataBridge API 已启动且当前网络可访问"), error);
    }
    if (lower.includes("aborted")) {
      return Object.assign(new Error("请求已中断，请稍后重试"), error);
    }
    return error;
  }
  return new Error("请求失败，请稍后重试");
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  try {
    const response = await fetch(`${API_BASE}${path}`, {
      ...init,
      credentials: "include",
      headers: { "Content-Type": "application/json", ...init?.headers },
    });
    const body = await response.json().catch(() => null);
    if (!response.ok) {
      const error = new Error(body?.message ?? `请求失败（${response.status}）`);
      Object.assign(error, { status: response.status, code: body?.code });
      throw error;
    }
    return body as T;
  } catch (error) {
    throw formatClientError(error);
  }
}

export const api = {
  session: () => request<AuthSession>("/auth/session"),
  login: (username: string, password: string) =>
    request<AuthSession>("/auth/login", { method: "POST", body: JSON.stringify({ username, password }) }),
  logout: () => request<{ ok: boolean }>("/auth/logout", { method: "POST" }),
  connections: () => request<DatabaseConnection[]>("/connections"),
  createConnection: (input: ConnectionInput) =>
    request<DatabaseConnection>("/connections", { method: "POST", body: JSON.stringify(input) }),
  updateConnection: (connectionId: string, input: ConnectionInput & { password?: string }) =>
    request<DatabaseConnection>(`/connections/${connectionId}`, { method: "PUT", body: JSON.stringify(input) }),
  deleteConnection: (connectionId: string) =>
    request<{ ok: boolean }>(`/connections/${connectionId}`, { method: "DELETE" }),
  refreshConnection: (connectionId: string) =>
    request<DatabaseConnection>(`/connections/${connectionId}/refresh`, { method: "POST" }),
  testConnection: (input: ConnectionInput) =>
    request<{ ok: boolean; latency: number }>("/connections/test", { method: "POST", body: JSON.stringify(input) }),
  databases: (connectionId: string) =>
    request<DatabaseInfo[]>(`/connections/${connectionId}/databases`),
  createDatabase: (connectionId: string, name: string) =>
    request<DatabaseInfo[]>(`/connections/${connectionId}/databases`, { method: "POST", body: JSON.stringify({ name }) }),
  seedData: (connectionId: string, database: string, rows: number, mode: "append" | "replace", template: "users" | "orders" | "products") =>
    request<SeedDataResult>(`/connections/${connectionId}/seed`, { method: "POST", body: JSON.stringify({ database, rows, mode, template }) }),
  storages: () => request<RemoteStorage[]>("/storages"),
  createStorage: (input: RemoteStorageInput) =>
    request<RemoteStorage>("/storages", { method: "POST", body: JSON.stringify(input) }),
  objectBuckets: (input: RemoteStorageInput) =>
    request<{ buckets: string[] }>("/storages/object/buckets", { method: "POST", body: JSON.stringify(input) }),
  storedObjectBuckets: (storageId: string, input: RemoteStorageInput) =>
    request<{ buckets: string[] }>(`/storages/${storageId}/buckets`, { method: "POST", body: JSON.stringify(input) }),
  createStoredObjectBucket: (storageId: string, name: string) =>
    request<{ storage: RemoteStorage; buckets: string[] }>(`/storages/${storageId}/buckets/create`, {
      method: "POST",
      body: JSON.stringify({ name }),
    }),
  updateStorage: (storageId: string, input: RemoteStorageInput) =>
    request<RemoteStorage>(`/storages/${storageId}`, { method: "PUT", body: JSON.stringify(input) }),
  deleteStorage: (storageId: string) =>
    request<{ ok: boolean }>(`/storages/${storageId}`, { method: "DELETE" }),
  testStorage: (storageId: string) =>
    request<{ storage: RemoteStorage; message: string }>(`/storages/${storageId}/test`, { method: "POST" }),
  tables: (connectionId: string, database: string) =>
    request<TableInfo[]>(`/connections/${connectionId}/databases/${encodeURIComponent(database)}/tables`),
  createMigration: async (config: MigrationConfig) =>
    normalizeMigration(await request<MigrationJob>("/migrations", { method: "POST", body: JSON.stringify(config) })),
  migration: async (id: string) => normalizeMigration(await request<MigrationJob>(`/migrations/${id}`)),
  cancelMigration: (id: string) =>
    request<{ ok: boolean }>(`/migrations/${id}/cancel`, { method: "POST" }),
  migrationsWithAudit: async () => {
    const response = await request<MigrationJob[] | MigrationsResponse>("/migrations");
    const migrations = (Array.isArray(response) ? response : response.migrations).map(normalizeMigration);
    const auditLogs = Array.isArray(response) ? [] : (response.auditLogs ?? []);
    return { migrations, auditLogs };
  },
  migrations: async () => (await api.migrationsWithAudit()).migrations,
  backups: async () => (await request<BackupJob[]>("/backups")).map(normalizeBackup),
  createBackup: async (config: BackupConfig) =>
    normalizeBackup(await request<BackupJob>("/backups", { method: "POST", body: JSON.stringify(config) })),
  updateBackup: async (id: string, config: BackupConfig) =>
    normalizeBackup(await request<BackupJob>(`/backups/${id}`, { method: "PUT", body: JSON.stringify(config) })),
  testBackupNotification: (method: "webhook" | "email", target: string) =>
    request<{ message: string }>("/backups/notification-test", { method: "POST", body: JSON.stringify({ method, target }) }),
  backup: async (id: string) => normalizeBackup(await request<BackupJob>(`/backups/${id}`)),
  triggerBackup: async (id: string) =>
    normalizeBackup(await request<BackupJob>(`/backups/${id}/trigger`, { method: "POST" })),
  cancelBackup: (id: string) =>
    request<{ ok: boolean }>(`/backups/${id}/cancel`, { method: "POST" }),
  smtpSettings: () => request<SMTPSettings>("/settings/smtp"),
  updateSMTPSettings: (input: SMTPSettings) =>
    request<SMTPSettings>("/settings/smtp", { method: "PUT", body: JSON.stringify(input) }),
};
