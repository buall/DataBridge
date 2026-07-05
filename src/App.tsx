import { startTransition, useCallback, useEffect, useMemo, useState } from "react";
import { api } from "./api";
import { BackupList } from "./pages/BackupList";
import { BackupArtifacts } from "./pages/BackupArtifacts";
import { BackupMonitoring } from "./pages/BackupMonitoring";
import { BackupRestore } from "./pages/BackupRestore";
import { BackupWizard } from "./pages/BackupWizard";
import { Layout } from "./components/Layout";
import { Connections } from "./pages/Connections";
import { Login } from "./pages/Login";
import { Logs } from "./pages/Logs";
import { MigrationList } from "./pages/MigrationList";
import { MigrationWizard } from "./pages/MigrationWizard";
import { Monitoring } from "./pages/Monitoring";
import { Overview } from "./pages/Overview";
import { Report } from "./pages/Report";
import { Settings } from "./pages/Settings";
import { Storages } from "./pages/Storages";
import type { AuditLogEntry, BatchImportResult } from "./types";
import type { AppView, BackupConfig, BackupJob, DatabaseConnection, MigrationConfig, MigrationJob, MigrationStage, RemoteStorage } from "./types";

type AppRoute = {
  view: AppView;
  stage: MigrationStage;
  jobId: string | null;
  sourceId: string | null;
};

const routes: Record<AppView, string> = {
  overview: "/",
  connections: "/connections",
  storages: "/storages",
  migrations: "/migrations",
  backups: "/backups",
  logs: "/logs",
  settings: "/settings/recipients",
};

const viewMeta: Record<AppView, [string, string]> = {
  overview: ["工作台总览", "数据迁移运行态势与近期任务"],
  connections: ["数据连接", "统一管理源端与目标端实例"],
  storages: ["远端存储", "管理备份同步目标和云端授权"],
  migrations: ["迁移任务", "查看任务列表、运行状态和历史结果"],
  backups: ["备份任务", "查看备份列表、运行状态和备份产物"],
  logs: ["运行日志", "查看 Agent、任务与系统审计事件"],
  settings: ["系统设置", "配置全局通知和服务参数"],
};

function routeFromPath(input: string): AppRoute {
  const url = new URL(input, window.location.origin);
  const path = url.pathname.replace(/\/+$/, "") || "/";
  const sourceId = url.searchParams.get("sourceId");
  if (path === "/connections") return { view: "connections", stage: "list", jobId: null, sourceId: null };
  if (path === "/storages") return { view: "storages", stage: "list", jobId: null, sourceId: null };
  if (path === "/logs") return { view: "logs", stage: "list", jobId: null, sourceId: null };
  if (path === "/settings" || path === "/settings/recipients") return { view: "settings", stage: "list", jobId: null, sourceId: null };
  if (path === "/backups/new") return { view: "backups", stage: "wizard", jobId: null, sourceId };
  if (path.startsWith("/backups/")) {
    const rest = path.slice("/backups/".length);
    if (rest.endsWith("/restore")) {
      const jobId = decodeURIComponent(rest.slice(0, -"/restore".length));
      return { view: "backups", stage: "restore", jobId: jobId || null, sourceId: null };
    }
    if (rest.endsWith("/artifacts")) {
      const jobId = decodeURIComponent(rest.slice(0, -"/artifacts".length));
      return { view: "backups", stage: "list", jobId: jobId || null, sourceId: "artifacts" };
    }
    const jobId = decodeURIComponent(rest);
    return { view: "backups", stage: "list", jobId: jobId || null, sourceId: null };
  }
  if (path === "/backups") return { view: "backups", stage: "list", jobId: null, sourceId: null };
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

function pathForBackup(job: BackupJob) {
  return `/backups/${encodeURIComponent(job.id)}`;
}

function pathForBackupRestore(job: BackupJob) {
  return `/backups/${encodeURIComponent(job.id)}/restore`;
}

function pathForBackupArtifacts(job: BackupJob) {
  return `/backups/${encodeURIComponent(job.id)}/artifacts`;
}

export default function App() {
  const [route, setRoute] = useState<AppRoute>(() => routeFromPath(window.location.pathname));
  const [navigationView, setNavigationView] = useState<AppView>(() => routeFromPath(window.location.pathname).view);
  const [connections, setConnections] = useState<DatabaseConnection[]>([]);
  const [storages, setStorages] = useState<RemoteStorage[]>([]);
  const [migrations, setMigrations] = useState<MigrationJob[]>([]);
  const [backups, setBackups] = useState<BackupJob[]>([]);
  const [auditLogs, setAuditLogs] = useState<AuditLogEntry[]>([]);
  const [authChecked, setAuthChecked] = useState(false);
  const [authenticated, setAuthenticated] = useState(false);
  const [authConfigured, setAuthConfigured] = useState(true);
  const [loadError, setLoadError] = useState("");
  const activeJob = useMemo(() => route.jobId ? migrations.find((job) => job.id === route.jobId) ?? null : null, [migrations, route.jobId]);
  const activeBackup = useMemo(() => route.jobId ? backups.find((job) => job.id === route.jobId) ?? null : null, [backups, route.jobId]);
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
    Promise.all([api.connections(), api.storages(), api.migrations(), api.backups()])
      .then(([connectionItems, storageItems, migrationItems, backupItems]) => {
        setConnections(connectionItems);
        setStorages(storageItems);
        setMigrations(migrationItems);
        setBackups(backupItems);
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
    if (!authenticated || route.view !== "logs") return;
    api.logs()
      .then((data) => {
        setAuditLogs(data.auditLogs);
        setMigrations((current) => current.map((job) => {
          const logJob = data.migrations.find((item) => item.id === job.id);
          return logJob ? { ...job, events: logJob.events } : job;
        }));
      })
      .catch((error) => setLoadError(error.message));
  }, [authenticated, route.view]);

  useEffect(() => {
    if (!authenticated || route.view !== "migrations" || route.stage !== "report" || !route.jobId) return;
    api.migration(route.jobId)
      .then((detail) => setMigrations((current) => [detail, ...current.filter((item) => item.id !== detail.id)]))
      .catch((error) => setLoadError(error.message));
  }, [authenticated, route.view, route.stage, route.jobId]);

  useEffect(() => {
    const handlePopState = () => {
      const next = routeFromPath(window.location.pathname);
      setNavigationView(next.view);
      startTransition(() => setRoute(next));
    };
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
    setNavigationView("migrations");
    startTransition(() => {
      setRoute((current) => current.jobId === job.id ? {
        view: "migrations",
        stage: job.status === "running" || job.status === "pending" ? "running" : "report",
        jobId: job.id,
        sourceId: null,
      } : current);
    });
  }, [migrations, route]);

  useEffect(() => {
    if (route.view !== "backups" || route.stage !== "list" || !route.jobId || !backups.length) return;
    const job = backups.find((item) => item.id === route.jobId);
    if (!job) return;
    setNavigationView("backups");
    startTransition(() => {
      setRoute((current) => current.jobId === job.id ? {
        view: "backups",
        stage: job.status === "running" || job.status === "pending" ? "running" : "report",
        jobId: job.id,
        sourceId: null,
      } : current);
    });
  }, [backups, route]);

  const applyRoute = useCallback((next: AppRoute) => {
    setNavigationView(next.view);
    startTransition(() => setRoute(next));
  }, []);

  function pushRoute(path: string) {
    if (window.location.pathname !== path) {
      window.history.pushState(null, "", path);
    }
    applyRoute(routeFromPath(path));
  }

  function navigate(next: AppView) {
    if (route.view === next && route.stage === "list" && !route.jobId) return;
    pushRoute(routes[next]);
  }
  function createMigration(sourceId?: string) {
    const path = sourceId ? `/migrations/new?sourceId=${encodeURIComponent(sourceId)}` : "/migrations/new";
    pushRoute(path);
  }
  function createBackup() {
    pushRoute("/backups/new");
  }
  function openMigration(job: MigrationJob) {
    const path = pathForJob(job);
    if (window.location.pathname !== path) {
      window.history.pushState(null, "", path);
    }
    applyRoute({
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
    applyRoute({ view: "migrations", stage: "running", jobId: job.id, sourceId: null });
  }
  function openBackup(job: BackupJob) {
    const path = pathForBackup(job);
    if (window.location.pathname !== path) {
      window.history.pushState(null, "", path);
    }
    applyRoute({
      view: "backups",
      stage: job.status === "running" || job.status === "pending" ? "running" : "report",
      jobId: job.id,
      sourceId: null,
    });
  }
  async function startBackup(config: BackupConfig) {
    const job = await api.createBackup(config);
    setBackups((current) => [job, ...current]);
    window.history.pushState(null, "", pathForBackup(job));
    applyRoute({ view: "backups", stage: job.status === "scheduled" ? "report" : "running", jobId: job.id, sourceId: null });
  }
  function completeBatchImport(result: BatchImportResult) {
    setConnections((current) => [...current, ...result.connections.filter((connection) => !current.some((item) => item.id === connection.id))]);
    setBackups((current) => [...result.backups, ...current.filter((item) => !result.backups.some((backup) => backup.id === item.id))]);
  }
  const completeMigration = useCallback((job: MigrationJob) => {
    setMigrations((current) => [job, ...current.filter((item) => item.id !== job.id)]);
    window.history.replaceState(null, "", pathForJob(job));
    applyRoute({ view: "migrations", stage: "report", jobId: job.id, sourceId: null });
  }, [applyRoute]);
  const completeBackup = useCallback((job: BackupJob) => {
    setBackups((current) => [job, ...current.filter((item) => item.id !== job.id)]);
    window.history.replaceState(null, "", pathForBackup(job));
    applyRoute({ view: "backups", stage: "report", jobId: job.id, sourceId: null });
  }, [applyRoute]);
  const openRestoredBackup = useCallback((job: BackupJob) => {
    setBackups((current) => [job, ...current.filter((item) => item.id !== job.id)]);
    window.history.pushState(null, "", pathForBackup(job));
    applyRoute({ view: "backups", stage: "running", jobId: job.id, sourceId: null });
  }, [applyRoute]);
  function openBackupRestore(job: BackupJob) {
    const path = pathForBackupRestore(job);
    if (window.location.pathname !== path) {
      window.history.pushState(null, "", path);
    }
    applyRoute({ view: "backups", stage: "restore", jobId: job.id, sourceId: null });
  }
  function openBackupArtifacts(job: BackupJob) {
    const path = pathForBackupArtifacts(job);
    if (window.location.pathname !== path) {
      window.history.pushState(null, "", path);
    }
    applyRoute({ view: "backups", stage: "report", jobId: job.id, sourceId: "artifacts" });
  }
  async function deleteMigration(job: MigrationJob) {
    await api.deleteMigration(job.id);
    setMigrations((current) => current.filter((item) => item.id !== job.id));
    if (route.view === "migrations" && route.jobId === job.id) {
      pushRoute(routes.migrations);
    }
  }
  async function deleteBackup(job: BackupJob) {
    await api.deleteBackup(job.id);
    setBackups((current) => current.filter((item) => item.id !== job.id));
    if (route.view === "backups" && route.jobId === job.id) {
      pushRoute(routes.backups);
    }
  }
  async function login(username: string, password: string) {
    const session = await api.login(username, password);
    setAuthenticated(session.authenticated);
    setAuthConfigured(session.configured);
    setLoadError("");
  }
  async function logout() {
    await api.logout().catch(() => undefined);
    setConnections([]);
    setStorages([]);
    setMigrations([]);
    setBackups([]);
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
  else if (view === "connections") content = <Connections connections={connections} backups={backups} onAdd={(connection: DatabaseConnection) => setConnections((current) => [...current, connection])} onUpdate={(connection: DatabaseConnection) => setConnections((current) => current.map((item) => item.id === connection.id ? connection : item))} onDelete={(connectionId: string) => setConnections((current) => current.filter((item) => item.id !== connectionId))} onCreateMigration={createMigration} onBatchImported={completeBatchImport} />;
  else if (view === "storages") content = <Storages storages={storages} onAdd={(storage) => setStorages((current) => [...current, storage])} onUpdate={(storage) => setStorages((current) => current.map((item) => item.id === storage.id ? storage : item))} onDelete={(storageId) => setStorages((current) => current.filter((item) => item.id !== storageId))} />;
  else if (view === "migrations") content = migrationStage === "running" && activeJob ? <Monitoring jobId={activeJob.id} onComplete={completeMigration} /> : migrationStage === "report" && activeJob ? <Report job={activeJob} /> : migrationStage === "wizard" ? <MigrationWizard connections={connections} initialSourceId={route.sourceId ?? undefined} onStart={startMigration} /> : route.jobId && migrations.length ? <MigrationList title="未找到迁移任务" description={`任务 ${route.jobId} 不存在或已被删除。`} migrations={migrations} onCreate={() => createMigration()} onOpen={openMigration} onDelete={deleteMigration} /> : <MigrationList migrations={migrations} onCreate={() => createMigration()} onOpen={openMigration} onDelete={deleteMigration} />;
  else if (view === "backups") content = migrationStage === "restore" && activeBackup ? <BackupRestore job={activeBackup} connections={connections} onBack={() => openBackup(activeBackup)} onRestoreCreated={openRestoredBackup} /> : migrationStage === "report" && activeBackup && route.sourceId === "artifacts" ? <BackupArtifacts job={activeBackup} onBack={() => openBackup(activeBackup)} /> : (migrationStage === "running" || migrationStage === "report") && activeBackup ? <BackupMonitoring jobId={activeBackup.id} connections={connections} storages={storages} onComplete={completeBackup} onOpenRestore={openBackupRestore} onOpenArtifacts={openBackupArtifacts} /> : migrationStage === "wizard" ? <BackupWizard connections={connections} storages={storages} onStart={startBackup} /> : <BackupList backups={backups} onCreate={createBackup} onOpen={openBackup} onDelete={deleteBackup} />;
  else if (view === "settings") content = <Settings />;
  else content = <Logs migrations={migrations} auditLogs={auditLogs} />;

  const meta = view === "migrations" && migrationStage === "running" && activeJob
    ? [activeJob.name, `任务 ID ${activeJob.id}`]
    : view === "migrations" && migrationStage === "wizard"
      ? ["创建迁移任务", "配置源端、目标端与迁移对象"]
    : view === "migrations" && migrationStage === "report" && activeJob
      ? ["迁移校验报告", `${activeJob.name} · ${new Date(activeJob.startedAt).toLocaleDateString("zh-CN")}`]
    : view === "backups" && migrationStage === "running" && activeBackup
      ? [activeBackup.name, `任务 ID ${activeBackup.id}`]
    : view === "backups" && migrationStage === "wizard"
      ? ["创建备份任务", "配置源数据库与备份目标"]
    : view === "backups" && migrationStage === "restore" && activeBackup
      ? ["数据还原", `${activeBackup.name} · 最新备份产物`]
    : view === "backups" && migrationStage === "report" && activeBackup && route.sourceId === "artifacts"
      ? ["备份产物", `${activeBackup.name} · 指定版本管理`]
    : view === "backups" && migrationStage === "report" && activeBackup
      ? ["备份任务结果", `${activeBackup.name} · ${new Date(activeBackup.startedAt).toLocaleDateString("zh-CN")}`]
    : viewMeta[view];
  const title = navigationView === view ? meta[0] : viewMeta[navigationView][0];

  return <Layout activeView={navigationView} title={title} paths={routes} onNavigate={navigate} onLogout={logout}>{loadError && <div className="error-banner">{loadError}</div>}{content}</Layout>;
}
