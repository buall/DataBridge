import { useCallback, useEffect, useState } from "react";
import { api } from "./api";
import { Layout } from "./components/Layout";
import { Connections } from "./pages/Connections";
import { MigrationWizard } from "./pages/MigrationWizard";
import { Monitoring } from "./pages/Monitoring";
import { Overview } from "./pages/Overview";
import { Report } from "./pages/Report";
import type { AppView, DatabaseConnection, MigrationConfig, MigrationJob, MigrationStage } from "./types";

const viewMeta: Record<AppView, [string, string]> = {
  overview: ["工作台总览", "数据迁移运行态势与近期任务"],
  connections: ["数据连接", "统一管理源端与目标端实例"],
  migrations: ["创建迁移任务", "配置源端、目标端与迁移对象"],
  reports: ["迁移校验报告", "查看已完成任务的真实校验结果"],
  logs: ["运行日志", "查看 Agent、任务与系统审计事件"],
};

export default function App() {
  const [view, setView] = useState<AppView>("overview");
  const [connections, setConnections] = useState<DatabaseConnection[]>([]);
  const [migrationStage, setMigrationStage] = useState<MigrationStage>("wizard");
  const [activeJob, setActiveJob] = useState<MigrationJob | null>(null);
  const [migrations, setMigrations] = useState<MigrationJob[]>([]);
  const [loadError, setLoadError] = useState("");

  useEffect(() => {
    Promise.all([api.connections(), api.migrations()])
      .then(([connectionItems, migrationItems]) => {
        setConnections(connectionItems);
        setMigrations(migrationItems);
      })
      .catch((error) => setLoadError(error.message));
  }, []);

  useEffect(() => {
    window.scrollTo({ top: 0, behavior: "auto" });
  }, [view, migrationStage]);

  function navigate(next: AppView) {
    setView(next);
    if (next === "migrations" && migrationStage === "report") setMigrationStage("wizard");
  }
  function createMigration() {
    setMigrationStage("wizard");
    setView("migrations");
  }
  async function startMigration(config: MigrationConfig) {
    const job = await api.createMigration(config);
    setActiveJob(job);
    setMigrations((current) => [job, ...current]);
    setMigrationStage("running");
  }
  const completeMigration = useCallback((job: MigrationJob) => {
    setActiveJob(job);
    setMigrations((current) => [job, ...current.filter((item) => item.id !== job.id)]);
    setMigrationStage("report");
    setView("reports");
  }, []);

  let content: React.ReactNode;
  if (view === "overview") content = <Overview connections={connections} migrations={migrations} onCreateMigration={createMigration} onConnections={() => navigate("connections")} />;
  else if (view === "connections") content = <Connections connections={connections} onAdd={(connection: DatabaseConnection) => setConnections((current) => [...current, connection])} />;
  else if (view === "migrations") content = migrationStage === "running" && activeJob ? <Monitoring jobId={activeJob.id} onComplete={completeMigration} /> : <MigrationWizard connections={connections} onStart={startMigration} />;
  else if (view === "reports") content = activeJob ? <Report job={activeJob} /> : <NoReport />;
  else content = <EmptyLogs />;

  const meta = view === "migrations" && migrationStage === "running" && activeJob
    ? [activeJob.name, `任务 ID ${activeJob.id}`]
    : view === "reports" && activeJob
      ? ["迁移校验报告", `${activeJob.name} · ${new Date(activeJob.startedAt).toLocaleDateString("zh-CN")}`]
    : viewMeta[view];

  return <Layout activeView={view} title={meta[0]} subtitle={meta[1]} onNavigate={navigate}>{loadError && <div className="error-banner">{loadError}。请确认 Go API 已启动。</div>}{content}</Layout>;
}

function EmptyLogs() {
  return <div className="empty-state"><div>LOG</div><h2>运行日志</h2><p>任务启动后，Agent 与系统事件会汇总显示在这里。</p></div>;
}

function NoReport() {
  return <div className="empty-state"><div>OK</div><h2>暂无校验报告</h2><p>完成一次 PostgreSQL 迁移后，逐表校验结果会显示在这里。</p></div>;
}
