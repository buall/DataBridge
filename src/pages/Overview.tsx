import {
  Activity,
  Archive,
  ArrowRight,
  CalendarClock,
  CheckCircle2,
  CircleDashed,
  Database,
  DatabaseBackup,
  FileCheck2,
  Gauge,
  Layers3,
  ShieldCheck,
  TriangleAlert,
} from "lucide-react";
import type { ReactNode } from "react";
import { migrationStageLabel } from "../labels";
import { migrationStatusLabel, migrationStatusTone } from "../status";
import type { BackupJob, DatabaseConnection, MigrationJob } from "../types";
import { Badge, Card, DatabaseLogo, DatabaseTypeIcon, RegionFlag } from "../components/UI";

export function Overview({
  connections,
  backups,
  migrations,
  onConnections,
  onBackups,
  onOpenBackup,
  onMigrations,
  onOpenMigration,
}: {
  connections: DatabaseConnection[];
  backups: BackupJob[];
  migrations: MigrationJob[];
  onConnections: () => void;
  onBackups: () => void;
  onOpenBackup: (job: BackupJob) => void;
  onMigrations: () => void;
  onOpenMigration: (job: MigrationJob) => void;
}) {
  const running = migrations.filter((item) => item.status === "running" || item.status === "pending");
  const completed = migrations.filter((item) => item.status === "completed");
  const failed = migrations.filter((item) => item.status === "failed" || item.status === "cancelled");
  const results = completed.flatMap((item) => item.results);
  const validResults = results.filter((item) => item.valid).length;
  const consistency = results.length ? `${((validResults / results.length) * 100).toFixed(2)}%` : "—";
  const connected = connections.filter((item) => item.status === "connected");
  const needsCheck = connections.length - connected.length;
  const scheduledBackups = backups.filter((item) => item.status === "scheduled" && !item.schedulePaused);
  const pausedBackups = backups.filter((item) => item.scheduleMode === "scheduled" && item.schedulePaused);
  const runningBackups = backups.filter((item) => item.status === "running" || item.status === "pending");
  const completedBackups = backups.filter((item) => item.status === "completed");
  const failedBackups = backups.filter((item) => item.status === "failed" || item.status === "cancelled");
  const backupBytes = completedBackups.reduce((sum, item) => sum + (item.artifactSize ?? 0), 0);
  const latestBackup = [...backups].sort((a, b) => backupTime(b).getTime() - backupTime(a).getTime())[0];
  const latestTask = [...migrations].sort((a, b) => new Date(b.startedAt).getTime() - new Date(a.startedAt).getTime())[0];
  const readiness = buildReadiness(connections.length, connected.length, scheduledBackups.length, completedBackups.length);
  const healthTone = failedBackups.length || needsCheck ? "orange" : runningBackups.length ? "blue" : "green";
  const healthCopy = failedBackups.length
    ? `${failedBackups.length} 个备份任务需要复查`
    : needsCheck
      ? `${needsCheck} 个连接等待验证`
      : runningBackups.length
        ? `${runningBackups.length} 个备份正在执行`
        : scheduledBackups.length
          ? `${scheduledBackups.length} 个备份计划已生效`
          : pausedBackups.length
            ? `${pausedBackups.length} 个备份计划已暂停`
            : "等待配置数据保护";
  const nextAction = failedBackups.length
    ? "先检查失败的备份任务，确保已有可用的恢复副本。"
    : needsCheck
      ? "先验证待检查连接，确认网络、账号和权限都可用。"
      : !scheduledBackups.length && pausedBackups.length
        ? "恢复已暂停的定时备份，重新启用自动数据保护。"
      : !scheduledBackups.length
        ? "为重要数据库创建定时备份，先确定恢复副本和保留周期。"
        : !completedBackups.length
          ? "等待或触发首次备份，确认能够生成可恢复的备份产物。"
          : "连接和备份均已就绪，可按需要查看迁移或校验活动。";

  return (
    <div className="page-stack overview-page">
      <section className="overview-hero">
        <div className="overview-hero-copy">
          <span className={`overview-status-pill ${healthTone}`}><Gauge size={14} />{healthCopy}</span>
          <h2>工作台概览</h2>
          <p>先确认连接可用、备份受保护，再查看迁移和校验活动。</p>
          <div className="overview-hero-summary">
            <span><Database size={15} />{connections.length ? `${connected.length}/${connections.length} 个连接可用` : "尚未配置连接"}</span>
            <span><Archive size={15} />{scheduledBackups.length ? `${scheduledBackups.length} 个定时备份` : pausedBackups.length ? `${pausedBackups.length} 个计划已暂停` : "尚未设置备份计划"}</span>
            <span><DatabaseBackup size={15} />{completedBackups.length ? `${completedBackups.length} 份备份可用` : "暂无可恢复副本"}</span>
          </div>
          <div className="overview-next-step"><strong>建议下一步</strong><span>{nextAction}</span></div>
        </div>
        <div className="overview-hero-panel" aria-label="数据保护摘要">
          <div>
            <span>已保护数据</span>
            <strong>{formatBytes(backupBytes)}</strong>
            <small>{completedBackups.length ? `${completedBackups.length} 个任务已生成产物` : "等待首次备份完成"}</small>
          </div>
          <div className="overview-hero-progress"><i style={{ width: `${connections.length ? Math.round((connected.length / connections.length) * 100) : 0}%` }} /></div>
          <div className="overview-hero-meta">
            <span><CheckCircle2 size={14} />{connected.length} 个连接可用</span>
            <span><FileCheck2 size={14} />{completedBackups.length} 份可恢复副本</span>
          </div>
        </div>
      </section>

      <div className="metric-grid">
        <Metric icon={<Database />} label="连接实例" value={String(connections.length)} detail={connections.length ? `${connected.length} 个可用` : "还没有连接"} tone="blue" onClick={onConnections} />
        <Metric icon={<Archive />} label="定时备份" value={String(scheduledBackups.length)} detail={scheduledBackups.length ? "按计划自动执行" : pausedBackups.length ? `${pausedBackups.length} 个计划已暂停` : "尚未配置计划"} tone="green" onClick={onBackups} />
        <Metric icon={<DatabaseBackup />} label="可恢复副本" value={String(completedBackups.length)} detail={completedBackups.length ? formatBytes(backupBytes) : "等待首次备份"} tone="cyan" onClick={onBackups} />
        <Metric icon={<ShieldCheck />} label="数据校验" value={consistency} detail={results.length ? `${validResults} / ${results.length} 张表通过` : "暂无校验结果"} tone="purple" onClick={onMigrations} />
      </div>

      <div className="overview-insight-grid">
        <Card className="overview-readiness-card">
          <div className="section-title compact"><div><h3>数据保护准备度</h3><p>先完成连接验证与备份覆盖，再开展迁移等变更操作。</p></div><Badge tone={readiness.done === readiness.steps.length ? "green" : "blue"}>{readiness.done} / {readiness.steps.length}</Badge></div>
          <div className="readiness-list">
            {readiness.steps.map((step) => (
              <button className={`readiness-item ${step.done ? "done" : ""}`} key={step.title} onClick={step.action === "connections" ? onConnections : onBackups}>
                <span>{step.done ? <CheckCircle2 size={16} /> : <CircleDashed size={16} />}</span>
                <div><strong>{step.title}</strong><small>{step.detail}</small></div>
                <ArrowRight size={14} />
              </button>
            ))}
          </div>
        </Card>

        <Card className="overview-health-card">
          <div className="section-title compact"><div><h3>运行健康</h3><p>连接验证、备份执行和最近活动的集中提醒。</p></div><Badge tone={failedBackups.length || needsCheck ? "orange" : "green"}>{failedBackups.length || needsCheck ? "需关注" : "正常"}</Badge></div>
          <div className="health-list">
            <HealthItem icon={<Database size={16} />} title={needsCheck ? "连接待验证" : "连接状态稳定"} detail={needsCheck ? `${needsCheck} 个连接需要重新验证` : `${connected.length} 个连接已通过验证`} tone={needsCheck ? "orange" : "green"} actionLabel="管理连接" onClick={onConnections} />
            <HealthItem icon={failedBackups.length ? <TriangleAlert size={16} /> : <Archive size={16} />} title={failedBackups.length ? "备份异常" : "备份计划"} detail={failedBackups.length ? `${failedBackups.length} 个备份失败或取消` : runningBackups.length ? `${runningBackups.length} 个备份正在执行` : scheduledBackups.length ? `${scheduledBackups.length} 个计划等待执行` : "还没有建立备份计划"} tone={failedBackups.length ? "orange" : "blue"} actionLabel="查看备份" onClick={onBackups} />
            <HealthItem icon={<Layers3 size={16} />} title="最近备份" detail={latestBackup ? `${latestBackup.name} · ${formatDate(backupTime(latestBackup).toISOString())}` : "创建备份后会显示最近动态"} tone="gray" actionLabel={latestBackup ? "打开" : "配置"} onClick={() => latestBackup ? onOpenBackup(latestBackup) : onBackups()} />
          </div>
        </Card>
      </div>

      <div className="section-title"><div><h3>数据库连接</h3><p>优先展示最近保存的实例和连接状态。</p></div><button className="text-button" onClick={onConnections}>管理连接 <ArrowRight size={14} /></button></div>
      {connections.length ? (
        <div className="connections-grid overview-connections-grid">
          {connections.slice(0, 3).map((connection) => (
            <Card className="connection-card overview-connection-card" key={connection.id}>
              <div className="connection-card-head">
                <DatabaseLogo type={connection.type} name={connection.name} />
                <div>
                  <strong data-tooltip-content={connection.name}>{connection.name}</strong>
                  <span className="connection-version-line"><DatabaseTypeIcon type={connection.type} />{connection.type}<em className="version-tag" data-tooltip-content={connection.version ? `v${connection.version}` : "待刷新"}>{compactVersionLabel(connection.version)}</em>{scheduledBackups.filter((job) => job.sourceId === connection.id).length > 0 && <span className="connection-schedule-pill"><CalendarClock size={12} />{scheduledBackups.filter((job) => job.sourceId === connection.id).length} 个定时任务</span>}</span>
                </div>
              </div>
              <dl>
                <div><dt>主机地址</dt><dd data-tooltip-content={`${connection.host}:${connection.port}`}>{connection.host}:{connection.port}</dd></div>
                <div><dt>连接用户</dt><dd>{connection.username || "未启用鉴权"}</dd></div>
                <div><dt>{connection.type === "etcd" ? "集群成员" : "节点地区"}</dt><dd className="region-value">{connection.type === "etcd" ? `${connection.endpoints?.length ?? 0} 个已发现端点` : <><RegionFlag region={connection.region} /><span>{connection.region || "未知"}</span></>}</dd></div>
                <div><dt>备注</dt><dd data-tooltip-content={connection.note || "未填写"}>{connection.note || "未填写"}</dd></div>
              </dl>
              <div className="connection-card-foot">
                <Badge tone={connection.status === "connected" ? "green" : "orange"}>{connection.status === "connected" ? "已连接" : "需验证"}</Badge>
                <span className={connection.status === "connected" ? "healthy" : "pending"}>{connection.status === "connected" ? <CheckCircle2 size={14} /> : <TriangleAlert size={14} />}{connection.latency ? `延迟 ${connection.latency}ms` : "等待连接测试"}</span>
              </div>
            </Card>
          ))}
        </div>
      ) : <EmptySection title="暂无数据库连接" description="先添加源端和目标端，再启动第一条迁移链路。" actionLabel="添加连接" onClick={onConnections} />}

      <Card className="task-table-card overview-task-card">
        <div className="section-title compact"><div><h3>最近备份</h3><p>优先确认是否有最新、可恢复的数据副本。</p></div><button className="text-button" onClick={onBackups}>全部备份 <ArrowRight size={14} /></button></div>
        {backups.length ? (
          <div className="overview-task-list">
            {[...runningBackups, ...backups.filter((job) => !runningBackups.includes(job))].slice(0, 5).map((job) => (
              <button className="overview-task-row" key={job.id} onClick={() => onOpenBackup(job)}>
                <span className={`overview-task-icon ${job.status}`}><Archive size={17} /></span>
                <div className="overview-task-main"><strong data-tooltip-content={job.name}>{job.name}</strong><small data-tooltip-content={`${job.sourceName} / ${job.sourceDatabase}`}>{job.sourceName} / {job.sourceDatabase}</small></div>
                <span className="overview-task-stage">{job.status === "scheduled" ? "定时计划" : job.destination}</span>
                <div className="table-progress overview-task-progress"><div><i style={{ width: `${job.status === "scheduled" ? 100 : job.progress}%` }} /></div><small>{job.schedulePaused ? "已暂停" : job.status === "scheduled" ? "已计划" : `${job.progress}%`}</small></div>
                <Badge tone={job.schedulePaused ? "gray" : migrationStatusTone(job.status)}>{job.schedulePaused ? "已暂停" : migrationStatusLabel(job.status)}</Badge>
              </button>
            ))}
          </div>
        ) : <EmptySection title="暂无备份任务" description="先为重要数据库创建定时备份，保留可恢复的数据副本。" actionLabel="配置备份" onClick={onBackups} />}
      </Card>

      <Card className="task-table-card overview-task-card">
        <div className="section-title compact"><div><h3>最近迁移活动</h3><p>迁移属于变更操作，在连接与备份确认后再持续关注。</p></div><button className="text-button" onClick={onMigrations}>全部任务 <ArrowRight size={14} /></button></div>
        {migrations.length ? (
          <div className="overview-task-list">
            {[...running, ...migrations.filter((task) => !running.includes(task))].slice(0, 5).map((task) => (
              <button className="overview-task-row" key={task.id} onClick={() => onOpenMigration(task)}>
                <span className={`overview-task-icon ${task.status}`}><Activity size={17} /></span>
                <div className="overview-task-main">
                  <strong data-tooltip-content={displayTaskName(task)}>{displayTaskName(task)}</strong>
                  <small data-tooltip-content={`${task.sourceDatabase} → ${task.targetDatabase}`}>{task.sourceDatabase} → {task.targetDatabase}</small>
                </div>
                <span className="overview-task-stage">{migrationStageLabel(task.stage)}</span>
                <div className="table-progress overview-task-progress"><div><i style={{ width: `${task.progress}%` }} /></div><small>{task.progress}%</small></div>
                <Badge tone={migrationStatusTone(task.status)}>{migrationStatusLabel(task.status)}</Badge>
              </button>
            ))}
          </div>
        ) : <EmptySection title="暂无迁移活动" description="连接和备份就绪后，可按需创建迁移任务。" />}
      </Card>
    </div>
  );
}

function Metric({ icon, label, value, detail, tone, onClick }: { icon: ReactNode; label: string; value: string; detail: string; tone: string; onClick: () => void }) {
  return <button className={`card metric-card metric-action ${tone}`} onClick={onClick}><span className="metric-icon">{icon}</span><div><span>{label}</span><strong>{value}</strong><small>{detail}</small></div></button>;
}

function HealthItem({ icon, title, detail, tone, actionLabel, onClick }: { icon: ReactNode; title: string; detail: string; tone: string; actionLabel: string; onClick: () => void }) {
  return (
    <button className="health-item" onClick={onClick}>
      <span className={tone}>{icon}</span>
      <div><strong>{title}</strong><small data-tooltip-content={detail}>{detail}</small></div>
      <em>{actionLabel}</em>
    </button>
  );
}

function EmptySection({ title, description, actionLabel, onClick }: { title: string; description: string; actionLabel?: string; onClick?: () => void }) {
  return (
    <div className="inline-empty overview-empty">
      <Database size={22} />
      <div><strong>{title}</strong><span>{description}</span></div>
      {actionLabel && onClick ? <button className="text-button" onClick={onClick}>{actionLabel} <ArrowRight size={14} /></button> : null}
    </div>
  );
}

function buildReadiness(connectionCount: number, connectedCount: number, scheduledCount: number, completedCount: number) {
  const steps = [
    { title: "保存数据库连接", detail: connectionCount ? `${connectionCount} 个连接已保存` : "先添加需要保护的数据库", done: connectionCount > 0, action: "connections" },
    { title: "完成连接验证", detail: connectedCount ? `${connectedCount} 个连接已通过验证` : "验证网络、账号和权限", done: connectedCount > 0, action: "connections" },
    { title: "建立定时备份", detail: scheduledCount ? `${scheduledCount} 个计划已启用` : "设置备份目标与执行周期", done: scheduledCount > 0, action: "backups" },
    { title: "确认恢复副本", detail: completedCount ? `${completedCount} 份备份已完成` : "完成首次备份并检查产物", done: completedCount > 0, action: "backups" },
  ];
  return { steps, done: steps.filter((step) => step.done).length };
}

function displayTaskName(job: MigrationJob) {
  const databaseRoute = `${job.sourceDatabase} → ${job.targetDatabase}`;
  return job.name && job.name !== databaseRoute ? job.name : `${job.sourceName} 到 ${job.targetName}迁移`;
}

function formatNumber(value: number) {
  return new Intl.NumberFormat("zh-CN", { notation: value > 999999 ? "compact" : "standard", maximumFractionDigits: 1 }).format(value);
}

function formatDate(value: string) {
  return new Date(value).toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false });
}

function backupTime(job: BackupJob) {
  return new Date(job.finishedAt || job.lastRunAt || job.startedAt);
}

function formatBytes(value: number) {
  if (!value) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  const amount = value / 1024 ** index;
  return `${amount >= 10 || index === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[index]}`;
}

function compactVersionLabel(version?: string) {
  if (!version) return "待刷新";
  const firstLine = version.split("\n")[0].trim();
  return `v${firstLine.length > 18 ? `${firstLine.slice(0, 17)}…` : firstLine}`;
}
