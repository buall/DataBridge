import type {
  ConnectionInput,
  AuditLogEntry,
  DatabaseConnection,
  DatabaseInfo,
  MigrationConfig,
  MigrationJob,
  SeedDataResult,
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
};
