import {
  Activity,
  AlertTriangle,
  Archive,
  ArrowRight,
  CalendarClock,
  Check,
  ChevronRight,
  Cloud,
  Database,
  DatabaseBackup,
  Plus,
  ShieldCheck,
} from "lucide-react";
import { BridgeFlow } from "../components/BridgeFlow";
import { Badge, Button, Card, DatabaseLogo, LatencyIndicator, RegionFlag } from "../components/UI";
import { migrationStageLabel } from "../labels";
import { migrationStatusLabel, migrationStatusTone } from "../status";
import type { BackupJob, DatabaseConnection, DatabaseType, MigrationJob, RemoteStorage } from "../types";

export function Overview({
  connections,
  storages = [],
  backups,
  migrations,
  onConnections,
  onStorages,
  onBackups,
  onOpenBackup,
  onMigrations,
  onOpenMigration,
  onCreateBackup,
  onCreateMigration,
}: {
  connections: DatabaseConnection[];
  storages?: RemoteStorage[];
  backups: BackupJob[];
  migrations: MigrationJob[];
  onConnections: () => void;
  onStorages?: () => void;
  onBackups: () => void;
  onOpenBackup: (job: BackupJob) => void;
  onMigrations: () => void;
  onOpenMigration: (job: MigrationJob) => void;
  onCreateBackup?: () => void;
  onCreateMigration?: () => void;
}) {
  const running = migrations.filter((item) => item.status === "running" || item.status === "pending");
  const activeRun = running[0];
  const completed = migrations.filter((item) => item.status === "completed");
  const results = completed.flatMap((item) => item.results);
  const validResults = results.filter((item) => item.valid).length;
  const consistency = results.length ? `${Math.round((validResults / results.length) * 100)}%` : "—";
  const totalMigratedRows = migrations.reduce((sum, item) => sum + (item.rowsCopied ?? 0), 0);

  const connected = connections.filter((item) => item.status === "connected");
  const needsCheck = connections.length - connected.length;
  const latencies = connected.map((item) => item.latency).filter((v) => typeof v === "number" && v > 0);
  const avgLatency = latencies.length ? Math.round(latencies.reduce((sum, v) => sum + v, 0) / latencies.length) : 0;
  const maxLatency = latencies.length ? Math.max(...latencies) : 0;

  const engineCounts = connections.reduce<Array<[DatabaseType, number]>>((acc, item) => {
    const existing = acc.find(([type]) => type === item.type);
    if (existing) existing[1] += 1;
    else acc.push([item.type, 1]);
    return acc;
  }, []);

  const scheduledBackups = backups.filter((item) => item.status === "scheduled" && !item.schedulePaused);
  const pausedBackups = backups.filter((item) => item.scheduleMode === "scheduled" && item.schedulePaused);
  const runningBackups = backups.filter((item) => item.status === "running" || item.status === "pending");
  const failedBackups = backups.filter(
    (item) =>
      item.status === "failed" ||
      item.status === "cancelled" ||
      ((item.scheduleMode === "scheduled" || item.status === "scheduled") &&
        item.status !== "running" &&
        item.status !== "pending" &&
        item.artifactVersions?.[0]?.status === "failed"),
  );
  const backupBytes = backups.reduce((sum, item) => sum + (item.artifactSize ?? 0), 0);
  const recoverableCopies = backups.reduce(
    (sum, item) =>
      sum +
      (item.artifactVersions?.filter((v) => v.status === "completed").length || (item.status === "completed" ? 1 : 0)),
    0,
  );
  const latestBackup = [...backups].sort((a, b) => backupTime(b).getTime() - backupTime(a).getTime())[0];

  const readyStorages = storages.filter((item) => item.status === "connected").length;
  const objectStorages = storages.filter((item) => item.type === "object").length;
  const otherStorages = storages.length - objectStorages;

  const readiness = buildReadiness(connections.length, connected.length, scheduledBackups.length, recoverableCopies);
  const readinessPct = Math.round((readiness.done / readiness.steps.length) * 100);

  const summaryBits: string[] = [];
  summaryBits.push(connections.length ? `${connected.length}/${connections.length} 连接可用` : "尚未配置实例连接");
  if (avgLatency > 0) summaryBits.push(`平均延迟 ${avgLatency} ms`);
  if (failedBackups.length) summaryBits.push(`${failedBackups.length} 个备份异常待复查`);
  else if (scheduledBackups.length) summaryBits.push(`${scheduledBackups.length} 个定时计划运行中`);
  else if (pausedBackups.length) summaryBits.push(`${pausedBackups.length} 个定时计划已暂停`);

  const nextAction = failedBackups.length
    ? "先排查失败的备份任务，确认已有可用的恢复副本。"
    : needsCheck
      ? "先验证待检查连接，确认网络、账号与权限都可用。"
      : !scheduledBackups.length && pausedBackups.length
        ? "恢复已暂停的定时备份，重新启用自动数据保护。"
        : !scheduledBackups.length
          ? "为重要数据库建立定时备份计划，确保具备可恢复副本。"
          : !recoverableCopies
            ? "等待或手动触发一次备份，验证可恢复产物已正常落盘。"
            : "连接与备份保护已就绪，可按需创建迁移任务或复核校验报告。";

  const activeRunType: DatabaseType =
    connections.find((c) => c.name === activeRun?.sourceName)?.type ?? "PostgreSQL";

  return (
    <div className="page-stack">
      <div className="h-row">
        <div>
          <h1 className="page-h">工作台总览</h1>
          <div className="sec-sub">{summaryBits.join(" · ")}</div>
        </div>
        <span className="sp" />
        <Button variant="secondary" onClick={onConnections}>
          <Plus size={15} />
          管理连接
        </Button>
        <Button variant="secondary" onClick={onCreateBackup ?? onBackups}>
          <ShieldCheck size={15} />
          新建备份
        </Button>
        <Button variant="primary" onClick={onCreateMigration ?? onMigrations}>
          <Activity size={15} />
          新建迁移
        </Button>
      </div>

      {activeRun ? (
        <Card>
          <div className="h-row" style={{ padding: "16px 22px 0" }}>
            <Badge tone="indigo">
              <span className="pd" />
              正在执行
            </Badge>
            <strong style={{ fontSize: 15 }}>{displayTaskName(activeRun)}</strong>
            <span className="tag mono-t">{activeRun.id}</span>
            <span className="sp" />
            <Button variant="secondary" className="sm" onClick={() => onOpenMigration(activeRun)}>
              打开实时执行 <ArrowRight size={13} />
            </Button>
          </div>
          <BridgeFlow
            sourceName={activeRun.sourceName}
            sourceDatabase={activeRun.sourceDatabase}
            targetName={activeRun.targetName}
            targetDatabase={activeRun.targetDatabase}
            type={activeRunType}
            status={activeRun.status}
            stage={activeRun.stage}
            progress={activeRun.progress}
            rowsCopied={activeRun.rowsCopied}
            rowsTotal={activeRun.rowsTotal}
            tablesDone={activeRun.tablesDone}
            tablesTotal={activeRun.tablesTotal}
            currentTable={activeRun.currentTable}
            isMongo={activeRun.results.some((r) => r.table.includes("collections)"))}
            isNeo4j={activeRun.results.some((r) => r.table.endsWith(" graph"))}
          />
        </Card>
      ) : null}

      {failedBackups.length > 0 || needsCheck > 0 ? (
        <div className="ov-alerts">
          {failedBackups.length > 0 ? (
            <div className="attn">
              <AlertTriangle size={17} style={{ color: "var(--danger)", flex: "none", marginTop: 2 }} />
              <div style={{ flex: 1, minWidth: 0 }}>
                <div className="a-t">备份异常 · {failedBackups[0].name}</div>
                <div className="a-s">
                  {failedBackups.length} 个备份任务最近执行失败或已取消，建议检查错误详情并重新执行备份。
                </div>
              </div>
              <Button variant="secondary" className="sm" onClick={() => onOpenBackup(failedBackups[0])}>
                排查异常
              </Button>
            </div>
          ) : null}
          {needsCheck > 0 ? (
            <div className="attn warn-a">
              <AlertTriangle size={17} style={{ color: "var(--warn)", flex: "none", marginTop: 2 }} />
              <div style={{ flex: 1, minWidth: 0 }}>
                <div className="a-t">有 {needsCheck} 个连接待验证或不可达</div>
                <div className="a-s">建议进入连接列表刷新测速，确认网络连通与账号权限可用。</div>
              </div>
              <Button variant="secondary" className="sm" onClick={onConnections}>
                检查连接
              </Button>
            </div>
          ) : null}
        </div>
      ) : null}

      <div className="ov-kpis">
        <button type="button" className="ov-kpi" onClick={onConnections}>
          <div className="ov-kpi-top">
            <span className="ov-kpi-lbl">
              <Database size={14} /> 数据库实例
            </span>
            <Badge tone={connections.length === 0 ? "gray" : needsCheck > 0 ? "orange" : "green"}>
              {connections.length === 0 ? "待添加" : `${connected.length}/${connections.length} 可用`}
            </Badge>
          </div>
          <div className="ov-kpi-val num">{connections.length}</div>
          <div className="ov-kpi-sub">
            {avgLatency > 0 ? (
              <>
                <span>平均延迟</span>
                <LatencyIndicator latency={avgLatency} />
                <span className="num">峰值 {maxLatency} ms</span>
              </>
            ) : (
              <span>点击添加并验证源端与目标端连接</span>
            )}
          </div>
        </button>

        <button type="button" className="ov-kpi" onClick={onBackups}>
          <div className="ov-kpi-top">
            <span className="ov-kpi-lbl">
              <DatabaseBackup size={14} /> 备份与副本
            </span>
            <Badge tone={failedBackups.length > 0 ? "red" : backups.length === 0 ? "gray" : "green"}>
              {failedBackups.length > 0 ? `${failedBackups.length} 异常` : backups.length === 0 ? "未配置" : "保护中"}
            </Badge>
          </div>
          <div className="ov-kpi-val num">{backups.length}</div>
          <div className="ov-kpi-sub">
            <span>
              定时 <b className="num">{scheduledBackups.length}</b>
            </span>
            <span>·</span>
            <span>
              副本 <b className="num">{recoverableCopies}</b> 份 ({formatBytes(backupBytes)})
            </span>
          </div>
        </button>

        <button type="button" className="ov-kpi" onClick={onMigrations}>
          <div className="ov-kpi-top">
            <span className="ov-kpi-lbl">
              <Activity size={14} /> 数据迁移
            </span>
            <Badge tone={running.length > 0 ? "indigo" : completed.length > 0 ? "green" : "gray"}>
              {running.length > 0 ? `${running.length} 运行中` : completed.length > 0 ? "已完成" : "暂无任务"}
            </Badge>
          </div>
          <div className="ov-kpi-val num">{migrations.length}</div>
          <div className="ov-kpi-sub">
            <span>
              校验一致性 <b className="num">{consistency}</b>
            </span>
            <span>·</span>
            <span>
              累计迁移 <b className="num">{formatNumber(totalMigratedRows)}</b> 行
            </span>
          </div>
        </button>

        <button type="button" className="ov-kpi" onClick={onStorages ?? onBackups}>
          <div className="ov-kpi-top">
            <span className="ov-kpi-lbl">
              <Cloud size={14} /> 远端存储
            </span>
            <Badge
              tone={
                storages.length === 0 ? "gray" : readyStorages === storages.length ? "green" : "orange"
              }
            >
              {storages.length === 0 ? "未接入" : `${readyStorages}/${storages.length} 就绪`}
            </Badge>
          </div>
          <div className="ov-kpi-val num">{storages.length}</div>
          <div className="ov-kpi-sub">
            {storages.length === 1 ? (
              <span>
                {storages[0].name} · {storages[0].provider}
              </span>
            ) : storages.length > 1 ? (
              <span>
                对象存储 <b className="num">{objectStorages}</b> · 网盘 / 本地 <b className="num">{otherStorages}</b>
              </span>
            ) : (
              <span>接入 S3 / WebDAV / 云盘作异地备份目标</span>
            )}
          </div>
        </button>
      </div>

      <div className="ov-row-2">
        <Card className="pad ov-card-stretch">
          <div className="h-row" style={{ marginBottom: 10 }}>
            <div>
              <h3 className="sec-title">数据库实例与链路延迟</h3>
              <div className="sec-sub">
                {engineCounts.length
                  ? engineCounts.map(([engine, count]) => `${engine} ${count}`).join(" · ")
                  : "暂无已配置的数据库实例"}
              </div>
            </div>
            <span className="sp" />
            <Button variant="ghost" className="sm" onClick={onConnections}>
              全部连接 <ChevronRight size={14} />
            </Button>
          </div>
          {connections.length ? (
            <div style={{ flex: 1 }}>
              {connections.slice(0, 5).map((connection) => (
                <div
                  key={connection.id}
                  className="kv ov-lat-row"
                  onClick={onConnections}
                >
                  <span
                    className="k"
                    data-tooltip-content={connection.name}
                    style={{ color: "var(--ink)", fontWeight: 550, minWidth: 0 }}
                  >
                    <DatabaseLogo type={connection.type} name={connection.name} size="sm" />
                    <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                      {connection.name}
                    </span>
                    {connection.region && connection.region !== "内网" ? (
                      <RegionFlag region={connection.region} />
                    ) : null}
                  </span>
                  <span
                    className="mono ov-lat-host"
                    data-tooltip-content={`${connection.host}:${connection.port}`}
                  >
                    {connection.host}:{connection.port}
                  </span>
                  <span className="ov-lat-col">
                    {connection.status === "connected" ? (
                      <span className="tag lat-tag">
                        <LatencyIndicator latency={connection.latency} />
                      </span>
                    ) : (
                      <Badge tone={connection.status === "untested" ? "orange" : "red"}>
                        {connection.status === "untested" ? "未验证" : "失败"}
                      </Badge>
                    )}
                  </span>
                </div>
              ))}
            </div>
          ) : (
            <div className="empty" style={{ padding: "22px 16px", marginTop: 8 }}>
              <div className="e-t">暂无数据库实例</div>
              <div className="e-s">先在「数据连接」中添加源端与目标端数据库。</div>
            </div>
          )}
          {connections.length > 5 ? (
            <div
              className="ov-card-foot"
              style={{
                borderTop: "1px solid var(--hair-2)",
                display: "flex",
                alignItems: "center",
                justifyContent: "space-between",
                fontSize: 12,
                color: "var(--muted)",
              }}
            >
              <span>当前展示前 5 个实例，共 {connections.length} 个已配置</span>
              <button
                type="button"
                onClick={onConnections}
                style={{
                  background: "none",
                  border: "none",
                  padding: 0,
                  minHeight: "auto",
                  fontSize: 12,
                  fontWeight: 600,
                  color: "var(--indigo-ink)",
                }}
              >
                查看其余 {connections.length - 5} 个 →
              </button>
            </div>
          ) : null}
        </Card>

        <Card className="pad ov-card-stretch">
          <div className="h-row">
            <div>
              <h3 className="sec-title">数据保护就绪检查</h3>
              <div className="sec-sub">按推荐路径完成实例连通、定时计划与副本验证</div>
            </div>
            <span className="sp" />
            <span className="tag num">
              {readiness.done} / {readiness.steps.length} 步
            </span>
          </div>
          <div className="ov-prog" aria-label={`就绪进度 ${readinessPct}%`}>
            <i style={{ width: `${readinessPct}%` }} />
          </div>
          <div className="ov-steps-grid">
            {readiness.steps.map((step, index) => (
              <button
                type="button"
                key={step.title}
                className={step.done ? "ov-step-tile done" : "ov-step-tile"}
                onClick={step.action === "connections" ? onConnections : onBackups}
              >
                <span
                  style={{
                    width: 22,
                    height: 22,
                    borderRadius: "50%",
                    flex: "none",
                    display: "grid",
                    placeItems: "center",
                    background: step.done ? "var(--ok-soft)" : "var(--surface)",
                    color: step.done ? "var(--ok)" : "var(--muted)",
                    border: step.done ? "none" : "1px solid var(--hair)",
                    fontSize: 11.5,
                    fontWeight: 650,
                  }}
                >
                  {step.done ? <Check size={12} /> : index + 1}
                </span>
                <span style={{ flex: 1, minWidth: 0 }}>
                  <span style={{ display: "block", fontSize: 13, fontWeight: 600 }}>{step.title}</span>
                  <span
                    style={{
                      display: "block",
                      fontSize: 11.5,
                      color: "var(--muted)",
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                    }}
                  >
                    {step.detail}
                  </span>
                </span>
              </button>
            ))}
          </div>
          <div className="ov-card-foot">
            <div
              style={{
                display: "flex",
                alignItems: "center",
                justifyContent: "space-between",
                gap: 10,
                background: "var(--indigo-faint)",
                borderRadius: 10,
                padding: "9px 12px",
                fontSize: 12.5,
                lineHeight: 1.45,
              }}
            >
              <span>
                <b style={{ color: "var(--indigo-ink)" }}>建议下一步</b>　{nextAction}
              </span>
            </div>
          </div>
        </Card>
      </div>

      <div className="ov-row-even">
        <Card className="pad ov-card-stretch">
          <div className="h-row" style={{ marginBottom: 10 }}>
            <div>
              <h3 className="sec-title">最近备份</h3>
              <div className="sec-sub">最近执行状态与备份产物容量</div>
            </div>
            <span className="sp" />
            <Button variant="ghost" className="sm" onClick={onBackups}>
              全部备份 <ChevronRight size={14} />
            </Button>
          </div>
          {backups.length ? (
            <div className="ledger" style={{ flex: 1 }}>
              {[...runningBackups, ...backups.filter((job) => !runningBackups.includes(job))].slice(0, 3).map((job) => {
                const type = backupDbType(job.sourceType);
                return (
                  <div key={job.id} className="lrow" onClick={() => onOpenBackup(job)}>
                    {type ? <DatabaseLogo type={type} name={job.sourceName} size="sm" /> : <Archive size={18} />}
                    <div className="lr-main">
                      <div className="lr-t">
                        <span>{job.name}</span>
                        <Badge tone={job.schedulePaused ? "gray" : migrationStatusTone(job.status)}>
                          {job.schedulePaused ? "已暂停" : migrationStatusLabel(job.status)}
                        </Badge>
                      </div>
                      <div className="lr-s">
                        <span>
                          {job.sourceName} / {job.sourceDatabase}
                        </span>
                        <span>
                          <CalendarClock size={12} style={{ verticalAlign: "-2px" }} />{" "}
                          {job.status === "scheduled" ? "定时计划" : job.destination}
                        </span>
                      </div>
                    </div>
                    <div className="lr-actions" style={{ textAlign: "right", flexDirection: "column", alignItems: "flex-end", gap: 2 }}>
                      <div className="num" style={{ fontWeight: 600 }}>
                        {job.artifactSize ? formatBytes(job.artifactSize) : `${job.progress}%`}
                      </div>
                      <div style={{ fontSize: 12, color: "var(--muted)" }}>
                        {latestBackup && job.id === latestBackup.id ? "最新" : formatDate(job.lastRunAt || job.startedAt)}
                      </div>
                    </div>
                  </div>
                );
              })}
            </div>
          ) : (
            <div
              style={{
                flex: 1,
                display: "flex",
                alignItems: "center",
                justifyContent: "space-between",
                gap: 12,
                padding: "14px 16px",
                borderRadius: 10,
                background: "var(--surface-2)",
                border: "1px dashed var(--hair)",
                flexWrap: "wrap",
              }}
            >
              <div>
                <div style={{ fontSize: 13.5, fontWeight: 600 }}>暂无备份任务</div>
                <div style={{ fontSize: 12, color: "var(--muted)", marginTop: 2 }}>
                  先为重要数据库创建定时备份，保留可恢复副本。
                </div>
              </div>
              <Button variant="secondary" className="sm" onClick={onCreateBackup ?? onBackups}>
                <ShieldCheck size={13} /> 新建备份
              </Button>
            </div>
          )}
        </Card>

        <Card className="pad ov-card-stretch">
          <div className="h-row" style={{ marginBottom: 10 }}>
            <div>
              <h3 className="sec-title">最近迁移</h3>
              <div className="sec-sub">跨实例数据迁移进度与一致性校验概览</div>
            </div>
            <span className="sp" />
            <Button variant="ghost" className="sm" onClick={onMigrations}>
              全部任务 <ChevronRight size={14} />
            </Button>
          </div>
          {migrations.length ? (
            <div className="ledger" style={{ flex: 1 }}>
              {[...running, ...migrations.filter((task) => !running.includes(task))].slice(0, 3).map((task) => {
                const type = connections.find((c) => c.name === task.sourceName)?.type ?? "PostgreSQL";
                return (
                  <div key={task.id} className="lrow" onClick={() => onOpenMigration(task)}>
                    <DatabaseLogo type={type} name={task.sourceName} size="sm" />
                    <div className="lr-main">
                      <div className="lr-t">
                        <span>{displayTaskName(task)}</span>
                        <Badge tone={migrationStatusTone(task.status)}>{migrationStatusLabel(task.status)}</Badge>
                      </div>
                      <div className="lr-s">
                        <span>
                          {task.sourceName}/{task.sourceDatabase} → {task.targetName}/{task.targetDatabase}
                        </span>
                        <span>{migrationStageLabel(task.stage)}</span>
                      </div>
                      {task.status === "running" ? (
                        <div className="progress" style={{ marginTop: 6 }}>
                          <i className="shimmer" style={{ width: `${task.progress}%` }} />
                        </div>
                      ) : null}
                    </div>
                    <div className="lr-actions" style={{ textAlign: "right", flexDirection: "column", alignItems: "flex-end", gap: 2 }}>
                      <div className="num" style={{ fontWeight: 600 }}>
                        {task.status === "running" ? `${task.progress}%` : formatNumber(task.rowsCopied)}
                      </div>
                      <div style={{ fontSize: 12, color: "var(--muted)" }}>{formatDate(task.startedAt)}</div>
                    </div>
                  </div>
                );
              })}
            </div>
          ) : (
            <div
              style={{
                flex: 1,
                display: "flex",
                alignItems: "center",
                justifyContent: "space-between",
                gap: 12,
                padding: "14px 16px",
                borderRadius: 10,
                background: "var(--surface-2)",
                border: "1px dashed var(--hair)",
                flexWrap: "wrap",
              }}
            >
              <div>
                <div style={{ fontSize: 13.5, fontWeight: 600 }}>暂无迁移活动</div>
                <div style={{ fontSize: 12, color: "var(--muted)", marginTop: 2 }}>
                  连接与备份就绪后，可选择同类型实例发起库表迁移与校验。
                </div>
              </div>
              <Button variant="secondary" className="sm" onClick={onCreateMigration ?? onMigrations}>
                <Activity size={13} /> 发起迁移
              </Button>
            </div>
          )}
        </Card>
      </div>
    </div>
  );
}

function buildReadiness(
  connectionCount: number,
  connectedCount: number,
  scheduledCount: number,
  recoverableCount: number,
) {
  const steps = [
    {
      title: "保存数据库连接",
      detail: connectionCount ? `${connectionCount} 个连接已保存` : "添加需要保护的数据库",
      done: connectionCount > 0,
      action: "connections",
    },
    {
      title: "完成连接验证",
      detail: connectedCount ? `${connectedCount} 个连接已通过验证` : "验证网络、账号与权限",
      done: connectionCount > 0 && connectedCount === connectionCount,
      action: "connections",
    },
    {
      title: "建立定时备份",
      detail: scheduledCount ? `${scheduledCount} 个计划运行中` : "设置备份目标与周期",
      done: scheduledCount > 0,
      action: "backups",
    },
    {
      title: "确认恢复副本",
      detail: recoverableCount ? `${recoverableCount} 份可用恢复副本` : "完成首次备份并核查产物",
      done: recoverableCount > 0,
      action: "backups",
    },
  ];
  return { steps, done: steps.filter((step) => step.done).length };
}

function backupDbType(type: string): DatabaseType | null {
  return ["PostgreSQL", "MySQL", "MongoDB", "Neo4j", "etcd", "Redis"].includes(type) ? (type as DatabaseType) : null;
}

function displayTaskName(job: MigrationJob) {
  const databaseRoute = `${job.sourceDatabase} → ${job.targetDatabase}`;
  return job.name && job.name !== databaseRoute ? job.name : `${job.sourceName} 到 ${job.targetName}迁移`;
}

function formatNumber(value: number) {
  return new Intl.NumberFormat("zh-CN", {
    notation: value > 999999 ? "compact" : "standard",
    maximumFractionDigits: 1,
  }).format(value);
}

function formatDate(value: string) {
  if (!value) return "—";
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  return d.toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  });
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
