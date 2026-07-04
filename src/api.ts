import type {
  ConnectionInput,
  AuditLogEntry,
  BackupConfig,
  BackupJob,
  BatchImportPreview,
  BatchImportResult,
  DatabaseConnection,
  DatabaseInfo,
  DuplicateConnectionInput,
  MigrationConfig,
  MigrationJob,
  NotificationRecipient,
  NotificationRecipientInput,
  NotificationRecipientTestInput,
  RemoteStorage,
  RemoteStorageInput,
  RestoreBackupConfig,
  RestorePrecheckResult,
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

export type WebDAVEntry = {
  name: string;
  path: string;
  isDirectory: boolean;
  size?: number;
  modified?: string;
};

function normalizeBackup(job: BackupJob): BackupJob {
  const backupTargets = job.backupTargets?.length
    ? job.backupTargets
    : job.destination
      ? [{ storageId: job.primaryDestinationId || job.destination, role: "primary" as const, order: 0 }]
      : [];
  return {
    ...job,
    backupTargets,
    primaryDestinationId: job.primaryDestinationId || backupTargets.find((target) => target.role === "primary")?.storageId || backupTargets[0]?.storageId,
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

async function requestBlob(path: string): Promise<{ blob: Blob; fileName: string }> {
  try {
    const response = await fetch(`${API_BASE}${path}`, { credentials: "include" });
    if (!response.ok) {
      const body = await response.json().catch(() => null);
      throw new Error(body?.message ?? `请求失败（${response.status}）`);
    }
    return { blob: await response.blob(), fileName: downloadFileName(response.headers.get("Content-Disposition")) };
  } catch (error) {
    throw formatClientError(error);
  }
}

async function requestForm<T>(path: string, form: FormData): Promise<T> {
  try {
    const response = await fetch(`${API_BASE}${path}`, {
      method: "POST",
      credentials: "include",
      body: form,
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

function importForm(file: File) {
  const form = new FormData();
  form.append("file", file, file.name);
  return form;
}

function downloadFileName(disposition: string | null) {
  const matched = disposition?.match(/filename="([^"]+)"/i);
  return matched?.[1] ? decodeURIComponent(matched[1]) : "databridge-backup.tar.gz";
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
  duplicateConnection: (connectionId: string, input: DuplicateConnectionInput) =>
    request<DatabaseConnection>(`/connections/${connectionId}/duplicate`, { method: "POST", body: JSON.stringify(input) }),
  deleteConnection: (connectionId: string) =>
    request<{ ok: boolean }>(`/connections/${connectionId}`, { method: "DELETE" }),
  refreshConnection: (connectionId: string) =>
    request<DatabaseConnection>(`/connections/${connectionId}/refresh`, { method: "POST" }),
  testConnection: (input: ConnectionInput) =>
    request<{ ok: boolean; latency: number }>("/connections/test", { method: "POST", body: JSON.stringify(input) }),
  previewExcelImport: (file: File) =>
    requestForm<BatchImportPreview>("/imports/excel/preview", importForm(file)),
  executeExcelImport: (file: File) =>
    requestForm<BatchImportResult>("/imports/excel", importForm(file)),
  exportExcel: (scope: "connections" | "schedules") =>
    requestBlob(`/exports/excel?scope=${scope}`),
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
  webDAVDirectories: (input: RemoteStorageInput) =>
    request<{ path: string; entries: WebDAVEntry[] }>("/storages/webdav/directories", {
      method: "POST",
      body: JSON.stringify(input),
    }),
  storedWebDAVDirectories: (storageId: string, input: RemoteStorageInput) =>
    request<{ path: string; entries: WebDAVEntry[] }>(`/storages/${storageId}/webdav/directories`, {
      method: "POST",
      body: JSON.stringify(input),
    }),
  webDAVDirectory: (storageId: string, path: string) =>
    request<{ path: string; entries: WebDAVEntry[] }>(`/storages/${storageId}/webdav/list`, {
      method: "POST",
      body: JSON.stringify({ path }),
    }),
  createWebDAVDirectory: (storageId: string, path: string, name: string) =>
    request<{ path: string; created: string; entries: WebDAVEntry[] }>(`/storages/${storageId}/webdav/directories/create`, {
      method: "POST",
      body: JSON.stringify({ path, name }),
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
  deleteMigration: (id: string) =>
    request<{ ok: boolean }>(`/migrations/${id}`, { method: "DELETE" }),
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
  backup: async (id: string, compact = false) =>
    normalizeBackup(await request<BackupJob>(`/backups/${id}${compact ? "?compact=1" : ""}`)),
  triggerBackup: async (id: string) =>
    normalizeBackup(await request<BackupJob>(`/backups/${id}/trigger`, { method: "POST" })),
  restoreBackup: async (id: string, config: RestoreBackupConfig) =>
    normalizeBackup(await request<BackupJob>(`/backups/${id}/restore`, { method: "POST", body: JSON.stringify(config) })),
  restorePrecheck: (id: string, config: RestoreBackupConfig) =>
    request<RestorePrecheckResult>(`/backups/${id}/restore/precheck`, { method: "POST", body: JSON.stringify(config) }),
  cancelBackup: (id: string) =>
    request<{ ok: boolean }>(`/backups/${id}/cancel`, { method: "POST" }),
  downloadBackupArtifact: (id: string, database: string, artifactVersionId?: string) =>
    requestBlob(`/backups/${id}/artifacts/${encodeURIComponent(database)}/download${artifactVersionId ? `?artifactVersionId=${encodeURIComponent(artifactVersionId)}` : ""}`),
  deleteBackup: (id: string) =>
    request<{ ok: boolean }>(`/backups/${id}`, { method: "DELETE" }),
  smtpSettings: () => request<SMTPSettings>("/settings/smtp"),
  updateSMTPSettings: (input: SMTPSettings) =>
    request<SMTPSettings>("/settings/smtp", { method: "PUT", body: JSON.stringify(input) }),
  notificationRecipients: () => request<NotificationRecipient[]>("/settings/notification-recipients"),
  createNotificationRecipient: (input: NotificationRecipientInput) =>
    request<NotificationRecipient>("/settings/notification-recipients", { method: "POST", body: JSON.stringify(input) }),
  updateNotificationRecipient: (id: string, input: NotificationRecipientInput) =>
    request<NotificationRecipient>(`/settings/notification-recipients/${id}`, { method: "PUT", body: JSON.stringify(input) }),
  deleteNotificationRecipient: (id: string) =>
    request<{ ok: boolean }>(`/settings/notification-recipients/${id}`, { method: "DELETE" }),
  testNotificationRecipient: (id: string) =>
    request<{ message: string }>(`/settings/notification-recipients/${id}/test`, { method: "POST" }),
  testNotificationRecipientInput: (input: NotificationRecipientTestInput) =>
    request<{ message: string }>("/settings/notification-recipients/test", { method: "POST", body: JSON.stringify(input) }),
};
