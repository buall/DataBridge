import type {
  ConnectionInput,
  DatabaseConnection,
  DatabaseInfo,
  MigrationConfig,
  MigrationJob,
  TableInfo,
} from "./types";

const API_BASE = import.meta.env.VITE_API_BASE ?? "/api";

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
    headers: { "Content-Type": "application/json", ...init?.headers },
  });
  const body = await response.json().catch(() => null);
  if (!response.ok) {
    throw new Error(body?.message ?? `请求失败 (${response.status})`);
  }
  return body as T;
}

export const api = {
  connections: () => request<DatabaseConnection[]>("/connections"),
  createConnection: (input: ConnectionInput) =>
    request<DatabaseConnection>("/connections", { method: "POST", body: JSON.stringify(input) }),
  testConnection: (input: ConnectionInput) =>
    request<{ ok: boolean; latency: number }>("/connections/test", { method: "POST", body: JSON.stringify(input) }),
  databases: (connectionId: string) =>
    request<DatabaseInfo[]>(`/connections/${connectionId}/databases`),
  tables: (connectionId: string, database: string) =>
    request<TableInfo[]>(`/connections/${connectionId}/databases/${encodeURIComponent(database)}/tables`),
  createMigration: async (config: MigrationConfig) =>
    normalizeMigration(await request<MigrationJob>("/migrations", { method: "POST", body: JSON.stringify(config) })),
  migration: async (id: string) => normalizeMigration(await request<MigrationJob>(`/migrations/${id}`)),
  cancelMigration: (id: string) =>
    request<{ ok: boolean }>(`/migrations/${id}/cancel`, { method: "POST" }),
  migrations: async () => (await request<MigrationJob[]>("/migrations")).map(normalizeMigration),
};
