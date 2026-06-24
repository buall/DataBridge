import { AlertTriangle, Check, CircleX, Clock3, Database, Gauge, Rows3, Square } from "lucide-react";
import { useEffect, useState } from "react";
import { api } from "../api";
import { migrationStageLabel } from "../labels";
import { migrationStatusLabel, migrationStatusTone } from "../status";
import type { MigrationJob } from "../types";
import { Badge, Button, Card, ConfirmDialog } from "../components/UI";

const stages = [
  ["precheck", "连接检查"],
  ["target-prep", "目标处理"],
  ["schema", "结构准备"],
  ["dump", "源端导出"],
  ["data", "数据传输"],
  ["restore", "目标导入"],
  ["constraints", "约束索引"],
  ["validation", "结果校验"],
];

export function Monitoring({ jobId, onComplete }: { jobId: string; onComplete: (job: MigrationJob) => void }) {
  const [job, setJob] = useState<MigrationJob | null>(null);
  const [error, setError] = useState("");
  const [cancelConfirmOpen, setCancelConfirmOpen] = useState(false);
  const [cancelling, setCancelling] = useState(false);

  useEffect(() => {
    let stopped = false;
    async function refresh() {
      try {
        const next = await api.migration(jobId);
        if (stopped) return;
        setJob(next);
        setError("");
        if (next.status === "completed") {
          onComplete(next);
          return;
        }
        if (next.status === "running" || next.status === "pending") {
          window.setTimeout(refresh, 800);
        }
      } catch (reason) {
        if (!stopped) {
          setError(reason instanceof Error ? reason.message : "无法获取任务状态");
          window.setTimeout(refresh, 1600);
        }
      }
    }
    refresh();
    return () => { stopped = true; };
  }, [jobId, onComplete]);

  async function cancel() {
    setCancelling(true);
    try {
      await api.cancelMigration(jobId);
      setCancelConfirmOpen(false);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "停止迁移任务失败");
    } finally {
      setCancelling(false);
    }
  }

  if (!job) return <div className="empty-state"><div>DB</div><h2>任务准备中</h2><p>{error || "正在连接源端和目标端数据库..."}</p></div>;

  const failed = job.status === "failed" || job.status === "cancelled";
  const currentStage = stageIndex(job.stage);
  const elapsed = Math.max(1, (Date.now() - new Date(job.startedAt).getTime()) / 1000);
  const rowsPerSecond = Math.round(job.rowsCopied / elapsed);
  const isMongoMigration = job.results.some((result) => result.table.includes("collections)"));
  const nativeCommandStage = job.stage === "dump" || job.stage === "restore" || job.stage === "target-prep";
  const throughputDetail = isMongoMigration ? "Mongo 原生工具阶段按集合统计" : nativeCommandStage ? "原生命令阶段不按行实时统计" : "进程内实时估算";
  const rowsDetail = isMongoMigration ? `${job.rowsCopied} / ${job.rowsTotal} 个集合` : nativeCommandStage ? migrationStageLabel(job.stage) : `${job.tablesDone} 张表已完成`;
  return (
    <div className="page-stack">
      {error && <div className="error-banner">{error}</div>}
      {failed && <div className="error-banner">{job.error || "迁移任务已取消"}</div>}
      <div className="runtime-heading"><div><div><Badge tone={migrationStatusTone(job.status)}>{migrationStatusLabel(job.status)}</Badge><h2>{job.sourceName} → {job.targetName}</h2></div><p>{job.sourceDatabase} → {job.targetDatabase} · 任务 ID {job.id}</p></div><div>{!failed && <Button variant="danger" onClick={() => setCancelConfirmOpen(true)}><Square size={15} />停止任务</Button>}</div></div>
      <Card className="overall-progress"><div><span>总体进度 · {migrationStageLabel(job.stage)}</span><strong>{job.progress}%</strong></div><div className="large-progress"><i style={{ width: `${job.progress}%` }} /></div><footer><span>{isMongoMigration ? `已完成 ${formatNumber(job.rowsCopied)} / ${formatNumber(job.rowsTotal)} 个集合` : `已迁移 ${formatNumber(job.rowsCopied)} / ${formatNumber(job.rowsTotal)} 行`}</span><span>{job.currentTable ? `当前：${job.currentTable}` : isMongoMigration ? `${job.tablesDone} / ${job.tablesTotal} 个迁移项` : `${job.tablesDone} / ${job.tablesTotal} 张表`}</span></footer></Card>
      <div className="runtime-metrics"><RuntimeMetric icon={<Gauge />} label="实时吞吐" value={isMongoMigration ? `${formatNumber(rowsPerSecond)} 集合/s` : `${formatNumber(rowsPerSecond)} 行/s`} detail={throughputDetail} /><RuntimeMetric icon={<Rows3 />} label={isMongoMigration ? "已迁移集合" : "已迁移行数"} value={formatNumber(job.rowsCopied)} detail={rowsDetail} /><RuntimeMetric icon={<Clock3 />} label="运行时长" value={formatDuration(elapsed * 1000)} detail="从开始到现在" /><RuntimeMetric icon={<AlertTriangle />} label="错误事件" value={String(job.events.filter((item) => item.level === "ERROR").length)} detail={failed ? "任务已停止" : "持续监控"} warning={failed} /></div>
      <div className="monitor-grid">
        <Card className="chart-card migration-detail"><div className="section-title compact"><div><h3>{isMongoMigration ? "集合迁移状态" : "逐表迁移状态"}</h3><p>{isMongoMigration ? "按集合查看迁移与校验结果。" : "逐表查看迁移与校验结果。"}</p></div><Badge tone="green">实时</Badge></div><div className="migration-table-list">{job.results.length ? job.results.map((result) => <div className="migration-table-row" key={result.table}><Database size={16}/><strong>{result.table}</strong><span>{formatNumber(result.copiedRows)} {isMongoMigration ? "集合" : "行"}</span>{result.error ? <Badge tone="red">失败</Badge> : <Badge tone={result.valid ? "green" : "blue"}>{result.valid ? "已校验" : "已复制"}</Badge>}</div>) : <div className="loading-copy">{nativeCommandStage ? `${migrationStageLabel(job.stage)}中，等待${isMongoMigration ? "集合" : "表"}级校验结果...` : `等待首个${isMongoMigration ? "集合" : "表"}完成迁移...`}</div>}</div></Card>
        <Card className="phase-card"><h3>执行阶段</h3>{stages.map(([id,label],index) => <div className={`phase ${index < currentStage || job.status === "completed" ? "done" : index === currentStage && !failed ? "active" : ""}`} key={id}><span>{index < currentStage || job.status === "completed" ? <Check size={13} /> : failed && index === currentStage ? <CircleX size={13}/> : index + 1}</span><strong>{label}</strong><small>{index < currentStage ? "完成" : index === currentStage ? failed ? "失败" : "进行中" : "等待中"}</small></div>)}</Card>
      </div>
      <Card className="event-card"><div className="section-title compact"><div><h3>实时事件</h3><p>查看迁移过程中的关键日志。</p></div></div>{[...job.events].reverse().slice(0, 12).map((event,index)=><div className="event-row" key={`${event.time}-${index}`}><time>{new Date(event.time).toLocaleTimeString("zh-CN",{hour12:false})}</time><Database size={15}/><span>{event.message}</span><Badge tone={event.level === "ERROR" ? "red" : event.level === "WARN" ? "orange" : "green"}>{event.level}</Badge></div>)}</Card>
      {cancelConfirmOpen && <ConfirmDialog title="停止迁移任务" description="这是中危操作，任务会被取消，未完成的数据表不会继续迁移。" tone="warning" confirmText="确认停止" submitting={cancelling} onCancel={() => setCancelConfirmOpen(false)} onConfirm={cancel} details={[`任务：${job.sourceName} → ${job.targetName}`, `数据库：${job.sourceDatabase} → ${job.targetDatabase}`]} />}
    </div>
  );
}

function RuntimeMetric({ icon, label, value, detail, warning = false }: { icon: React.ReactNode; label: string; value: string; detail: string; warning?: boolean }) {
  return <Card className="runtime-metric"><span className={warning ? "warning" : ""}>{icon}</span><div><small>{label}</small><strong>{value}</strong><em className={warning ? "warning" : ""}>{detail}</em></div></Card>;
}

function stageIndex(stage: string) {
  const index = stages.findIndex(([id]) => id === stage);
  return index >= 0 ? index : 0;
}
function formatNumber(value: number) { return new Intl.NumberFormat("zh-CN").format(value); }
function formatDuration(value: number) { const seconds = Math.round(value / 1000); return seconds < 60 ? `${seconds}s` : `${Math.floor(seconds / 60)}m ${seconds % 60}s`; }
