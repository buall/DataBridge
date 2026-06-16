import { useCallback, useEffect, useMemo, useState } from "react";
import { api } from "./api";
import { Layout } from "./components/Layout";
import { Connections } from "./pages/Connections";
import { Login } from "./pages/Login";
import { Logs } from "./pages/Logs";
import { MigrationList } from "./pages/MigrationList";
import { MigrationWizard } from "./pages/MigrationWizard";
import { Monitoring } from "./pages/Monitoring";
import { Overview } from "./pages/Overview";
import { Report } from "./pages/Report";
import type { AuditLogEntry } from "./types";
import type { AppView, DatabaseConnection, MigrationConfig, MigrationJob, MigrationStage } from "./types";

type AppRoute = {
  view: AppView;
  stage: MigrationStage;
  jobId: string | null;
  sourceId: string | null;
};

const routes: Record<AppView, string> = {
  overview: "/",
  connections: "/connections",
  migrations: "/migrations",
  logs: "/logs",
};

const viewMeta: Record<AppView, [string, string]> = {
  overview: ["工作台总览", "数据迁移运行态势与近期任务"],
  connections: ["数据连接", "统一管理源端与目标端实例"],
  migrations: ["迁移任务", "查看任务列表、运行状态和历史结果"],
  logs: ["运行日志", "查看 Agent、任务与系统审计事件"],
};

function routeFromPath(input: string): AppRoute {
  const url = new URL(input, window.location.origin);
  const path = url.pathname.replace(/\/+$/, "") || "/";
  const sourceId = url.searchParams.get("sourceId");
  if (path === "/connections") return { view: "connections", stage: "list", jobId: null, sourceId: null };
  if (path === "/logs") return { view: "logs", stage: "list", jobId: null, sourceId: null };
  if (path === "/migrations/new") return { view: "migrations", stage: "wizard", jobId: null, sourceId };
  if (path.startsWith("/migrations/")) {
    const jobId = decodeURIComponent(path.slice("/migrations/".length));
    return { view: "migrations", stage: "list", jobId: jobId || null, sourceId: null };
  }
  if (path === "/migrations") return { view: "migrations", stage: "list", jobId: null, sourceId: null };
  return { view: "overview", stage: "list", jobId: null, sourceId: null };
}

function pathForJob(job: MigrationJob) {
  return `/migrations/${encodeURIComponent(job.id)}`;
}

export default function App() {
  const [route, setRoute] = useState<AppRoute>(() => routeFromPath(window.location.pathname));
  const [connections, setConnections] = useState<DatabaseConnection[]>([]);
  const [migrations, setMigrations] = useState<MigrationJob[]>([]);
  const [auditLogs, setAuditLogs] = useState<AuditLogEntry[]>([]);
  const [authChecked, setAuthChecked] = useState(false);
  const [authenticated, setAuthenticated] = useState(false);
  const [authConfigured, setAuthConfigured] = useState(true);
  const [loadError, setLoadError] = useState("");
  const activeJob = useMemo(() => route.jobId ? migrations.find((job) => job.id === route.jobId) ?? null : null, [migrations, route.jobId]);
  const { view, stage: migrationStage } = route;

  useEffect(() => {
    api.session()
      .then((session) => {
        setAuthenticated(session.authenticated);
        setAuthConfigured(session.configured);
      })
      .catch(() => setAuthenticated(false))
      .finally(() => setAuthChecked(true));
  }, []);

  useEffect(() => {
    if (!authenticated) return;
    Promise.all([api.connections(), api.migrationsWithAudit()])
      .then(([connectionItems, migrationData]) => {
        setConnections(connectionItems);
        setMigrations(migrationData.migrations);
        setAuditLogs(migrationData.auditLogs);
      })
      .catch((error) => {
        if (error && typeof error === "object" && "status" in error && error.status === 401) {
          setAuthenticated(false);
          return;
        }
        setLoadError(error.message);
      });
  }, [authenticated]);

  useEffect(() => {
    const handlePopState = () => setRoute(routeFromPath(window.location.pathname));
    window.addEventListener("popstate", handlePopState);
    return () => window.removeEventListener("popstate", handlePopState);
  }, []);

  useEffect(() => {
    window.scrollTo({ top: 0, behavior: "auto" });
  }, [route]);

  useEffect(() => {
    if (route.view !== "migrations" || route.stage !== "list" || !route.jobId || !migrations.length) return;
    const job = migrations.find((item) => item.id === route.jobId);
    if (!job) return;
    setRoute((current) => current.jobId === job.id ? {
      view: "migrations",
      stage: job.status === "running" || job.status === "pending" ? "running" : "report",
      jobId: job.id,
      sourceId: null,
    } : current);
  }, [migrations, route]);

  function pushRoute(path: string) {
    if (window.location.pathname !== path) {
      window.history.pushState(null, "", path);
    }
    setRoute(routeFromPath(path));
  }

  function navigate(next: AppView) {
    pushRoute(routes[next]);
  }
  function createMigration(sourceId?: string) {
    const path = sourceId ? `/migrations/new?sourceId=${encodeURIComponent(sourceId)}` : "/migrations/new";
    pushRoute(path);
  }
  function openMigration(job: MigrationJob) {
    const path = pathForJob(job);
    if (window.location.pathname !== path) {
      window.history.pushState(null, "", path);
    }
    setRoute({
      view: "migrations",
      stage: job.status === "running" || job.status === "pending" ? "running" : "report",
      jobId: job.id,
      sourceId: null,
    });
  }
  async function startMigration(config: MigrationConfig) {
    const job = await api.createMigration(config);
    setMigrations((current) => [job, ...current]);
    window.history.pushState(null, "", pathForJob(job));
    setRoute({ view: "migrations", stage: "running", jobId: job.id, sourceId: null });
  }
  const completeMigration = useCallback((job: MigrationJob) => {
    setMigrations((current) => [job, ...current.filter((item) => item.id !== job.id)]);
    window.history.replaceState(null, "", pathForJob(job));
    setRoute({ view: "migrations", stage: "report", jobId: job.id, sourceId: null });
  }, []);
  async function login(username: string, password: string) {
    const session = await api.login(username, password);
    setAuthenticated(session.authenticated);
    setAuthConfigured(session.configured);
    setLoadError("");
  }
  async function logout() {
    await api.logout().catch(() => undefined);
    setConnections([]);
    setMigrations([]);
    setAuthenticated(false);
  }

  if (!authChecked) {
    return <main className="login-page"><div className="loading-copy">正在检查登录状态...</div></main>;
  }
  if (!authenticated) {
    return <Login configured={authConfigured} onLogin={login} />;
  }

  let content: React.ReactNode;
  if (view === "overview") content = <Overview connections={connections} migrations={migrations} onCreateMigration={() => createMigration()} onConnections={() => navigate("connections")} onMigrations={() => navigate("migrations")} onOpenMigration={openMigration} />;
  else if (view === "connections") content = <Connections connections={connections} onAdd={(connection: DatabaseConnection) => setConnections((current) => [...current, connection])} onUpdate={(connection: DatabaseConnection) => setConnections((current) => current.map((item) => item.id === connection.id ? connection : item))} onDelete={(connectionId: string) => setConnections((current) => current.filter((item) => item.id !== connectionId))} onCreateMigration={createMigration} />;
  else if (view === "migrations") content = migrationStage === "running" && activeJob ? <Monitoring jobId={activeJob.id} onComplete={completeMigration} /> : migrationStage === "report" && activeJob ? <Report job={activeJob} /> : migrationStage === "wizard" ? <MigrationWizard connections={connections} initialSourceId={route.sourceId ?? undefined} onStart={startMigration} /> : route.jobId && migrations.length ? <MigrationList title="未找到迁移任务" description={`任务 ${route.jobId} 不存在或已被删除。`} migrations={migrations} onCreate={() => createMigration()} onOpen={openMigration} /> : <MigrationList migrations={migrations} onCreate={() => createMigration()} onOpen={openMigration} />;
  else content = <Logs migrations={migrations} auditLogs={auditLogs} />;

  const meta = view === "migrations" && migrationStage === "running" && activeJob
    ? [activeJob.name, `任务 ID ${activeJob.id}`]
    : view === "migrations" && migrationStage === "wizard"
      ? ["创建迁移任务", "配置源端、目标端与迁移对象"]
    : view === "migrations" && migrationStage === "report" && activeJob
      ? ["迁移校验报告", `${activeJob.name} · ${new Date(activeJob.startedAt).toLocaleDateString("zh-CN")}`]
    : viewMeta[view];

  return <Layout activeView={view} title={meta[0]} subtitle={meta[1]} paths={routes} onNavigate={navigate} onLogout={logout}>{loadError && <div className="error-banner">{loadError}</div>}{content}</Layout>;
}
