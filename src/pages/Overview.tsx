import { ArrowRight, Database, Plus, ShieldCheck, Timer, Zap } from "lucide-react";
import { migrationStageLabel } from "../labels";
import { migrationStatusLabel, migrationStatusTone } from "../status";
import type { DatabaseConnection, MigrationJob } from "../types";
import { Badge, Button, Card, DatabaseLogo, DatabaseTypeIcon, RegionFlag } from "../components/UI";

export function Overview({
  connections,
  migrations,
  onCreateMigration,
  onConnections,
  onMigrations,
  onOpenMigration,
}: {
  connections: DatabaseConnection[];
  migrations: MigrationJob[];
  onCreateMigration: () => void;
  onConnections: () => void;
  onMigrations: () => void;
  onOpenMigration: (job: MigrationJob) => void;
}) {
  const running = migrations.filter((item) => item.status === "running" || item.status === "pending");
  const completed = migrations.filter((item) => item.status === "completed");
  const rowsCopied = completed.reduce((sum, item) => sum + item.rowsCopied, 0);
  const results = completed.flatMap((item) => item.results);
  const validResults = results.filter((item) => item.valid).length;
  const consistency = results.length ? `${(validResults / results.length * 100).toFixed(2)}%` : "—";

  return (
    <div className="page-stack">
      <div className="page-heading">
        <div><h2>工作台概览</h2><p>连接、迁移和校验状态一目了然。</p></div>
        <Button onClick={onCreateMigration}><Plus size={17} />新建迁移</Button>
      </div>
      <div className="metric-grid">
        <Metric icon={<Database />} label="连接实例" value={String(connections.length)} detail={connections.length ? "已保存连接" : "还没有连接"} tone="blue" onClick={onConnections} />
        <Metric icon={<Zap />} label="运行中任务" value={String(running.length)} detail={running.length ? "正在执行" : "当前无任务"} tone="green" onClick={onMigrations} />
        <Metric icon={<Timer />} label="累计迁移行数" value={formatNumber(rowsCopied)} detail={`${completed.length} 个任务完成`} tone="cyan" onClick={onMigrations} />
        <Metric icon={<ShieldCheck />} label="行数一致性" value={consistency} detail={results.length ? `${validResults} / ${results.length} 张表通过` : "暂无结果"} tone="purple" onClick={onMigrations} />
      </div>

      <div className="section-title"><div><h3>数据库连接</h3><p>已保存并加密的数据库实例</p></div><button className="text-button" onClick={onConnections}>管理连接 <ArrowRight size={14} /></button></div>
      {connections.length ? (
        <div className="connection-preview-grid">
          {connections.slice(0, 3).map((connection) => (
            <Card className="connection-preview" key={connection.id}>
              <div className="connection-preview-head">
                <DatabaseLogo type={connection.type} name={connection.name} />
                <div>
                  <strong data-tooltip-content={connection.name}>{connection.name}</strong>
                  <span className="connection-version-line"><DatabaseTypeIcon type={connection.type} />{connection.type}<em className="version-tag">{connection.version ? `v${connection.version}` : "待刷新"}</em></span>
                  <span className="connection-note" data-tooltip-content={connection.note || ""}>{connection.note || "\u00A0"}</span>
                </div>
                <Badge tone={connection.status === "connected" ? "green" : "orange"}>
                  {connection.status === "connected" ? "已连接" : "需验证"}
                </Badge>
              </div>
              <div className="connection-meta">
                <div><span>HOST</span><strong>{connection.host}:{connection.port}</strong></div>
                <div><span>REGION</span><strong className="region-value"><RegionFlag region={connection.region} /><span>{connection.region || "未知"}</span></strong></div>
                <span className="latency">延迟 {connection.latency}ms</span>
              </div>
            </Card>
          ))}
        </div>
      ) : <EmptySection title="暂无数据库连接" description="先添加连接，再开始迁移。" />}

      <Card className="task-table-card">
        <div className="section-title compact"><div><h3>迁移任务</h3><p>任务记录会保留在本地。</p></div><Badge tone="gray">{migrations.length} 个任务</Badge></div>
        {migrations.length ? (
          <div className="task-table">
            <div className="table-row table-head"><span>任务名称</span><span>源端 → 目标端</span><span>阶段</span><span>进度</span><span>状态</span></div>
            {migrations.slice(0, 8).map((task) => (
              <button className="table-row task-row-button" key={task.id} onClick={() => onOpenMigration(task)}>
                <strong data-tooltip-content={task.name}>{task.name}</strong><span data-tooltip-content={`${task.sourceDatabase} → ${task.targetDatabase}`}>{task.sourceDatabase} → {task.targetDatabase}</span><span>{migrationStageLabel(task.stage)}</span>
                <div className="table-progress"><div><i style={{ width: `${task.progress}%` }} /></div><small>{task.progress}%</small></div>
                <Badge tone={migrationStatusTone(task.status)}>{migrationStatusLabel(task.status)}</Badge>
              </button>
            ))}
          </div>
        ) : <EmptySection title="暂无迁移任务" description="创建任务后，这里会显示进度和报告。" />}
      </Card>
    </div>
  );
}

function Metric({ icon, label, value, detail, tone, onClick }: { icon: React.ReactNode; label: string; value: string; detail: string; tone: string; onClick: () => void }) {
  return <button className={`card metric-card metric-action ${tone}`} onClick={onClick}><span className="metric-icon">{icon}</span><div><span>{label}</span><strong>{value}</strong><small>{detail}</small></div></button>;
}

function EmptySection({ title, description }: { title: string; description: string }) {
  return <div className="inline-empty"><Database size={22} /><div><strong>{title}</strong><span>{description}</span></div></div>;
}

function formatNumber(value: number) {
  return new Intl.NumberFormat("zh-CN", { notation: value > 999999 ? "compact" : "standard", maximumFractionDigits: 1 }).format(value);
}
