import type {
  ConnectionInput,
  DatabaseConnection,
  DatabaseInfo,
  MigrationConfig,
  MigrationJob,
  TableInfo,
} from "./types";

const API_BASE = import.meta.env.VITE_API_BASE ?? "/api";

export type AuthSession = {
  authenticated: boolean;
  username: string;
  configured: boolean;
};

function normalizeMigration(job: MigrationJob): MigrationJob {
  return {
    ...job,
    results: job.results ?? [],
    events: job.events ?? [],
  };
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${API_BASE}${path}`, {
    ...init,
    credentials: "include",
    headers: { "Content-Type": "application/json", ...init?.headers },
  });
  const body = await response.json().catch(() => null);
  if (!response.ok) {
    const error = new Error(body?.message ?? `请求失败 (${response.status})`);
    Object.assign(error, { status: response.status, code: body?.code });
    throw error;
  }
  return body as T;
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
  refreshConnection: (connectionId: string) =>
    request<DatabaseConnection>(`/connections/${connectionId}/refresh`, { method: "POST" }),
  testConnection: (input: ConnectionInput) =>
    request<{ ok: boolean; latency: number }>("/connections/test", { method: "POST", body: JSON.stringify(input) }),
  databases: (connectionId: string) =>
    request<DatabaseInfo[]>(`/connections/${connectionId}/databases`),
  createDatabase: (connectionId: string, name: string) =>
    request<DatabaseInfo[]>(`/connections/${connectionId}/databases`, { method: "POST", body: JSON.stringify({ name }) }),
  tables: (connectionId: string, database: string) =>
    request<TableInfo[]>(`/connections/${connectionId}/databases/${encodeURIComponent(database)}/tables`),
  createMigration: async (config: MigrationConfig) =>
    normalizeMigration(await request<MigrationJob>("/migrations", { method: "POST", body: JSON.stringify(config) })),
  migration: async (id: string) => normalizeMigration(await request<MigrationJob>(`/migrations/${id}`)),
  cancelMigration: (id: string) =>
    request<{ ok: boolean }>(`/migrations/${id}/cancel`, { method: "POST" }),
  migrations: async () => (await request<MigrationJob[]>("/migrations")).map(normalizeMigration),
};
