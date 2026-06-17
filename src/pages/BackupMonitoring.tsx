import { AlertTriangle, Archive, Check, CircleX, Clock3, FileArchive, Gauge, Square } from "lucide-react";
import { useEffect, useState } from "react";
import { api } from "../api";
import { migrationStatusLabel, migrationStatusTone } from "../status";
import type { BackupJob } from "../types";
import { Badge, Button, Card } from "../components/UI";

const stages = [
  ["pending", "任务排队"],
  ["export", "数据库导出"],
  ["upload", "写入存储"],
  ["completed", "备份完成"],
];

export function BackupMonitoring({ jobId, onComplete }: { jobId: string; onComplete: (job: BackupJob) => void }) {
  const [job, setJob] = useState<BackupJob | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    let stopped = false;
    async function refresh() {
      try {
        const next = await api.backup(jobId);
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
          setError(reason instanceof Error ? reason.message : "无法获取备份任务状态");
          window.setTimeout(refresh, 1600);
        }
      }
    }
    refresh();
    return () => { stopped = true; };
  }, [jobId, onComplete]);

  async function cancel() {
    await api.cancelBackup(jobId);
  }

  if (!job) return <div className="empty-state"><div>DB</div><h2>任务准备中</h2><p>{error || "正在准备备份任务..."}</p></div>;

  const failed = job.status === "failed" || job.status === "cancelled";
  const elapsed = Math.max(1, (Date.now() - new Date(job.startedAt).getTime()) / 1000);
  const currentStage = stageIndex(job.stage);

  return (
    <div className="page-stack">
      {error && <div className="error-banner">{error}</div>}
      {failed && <div className="error-banner">{job.error || "备份任务已取消"}</div>}
      <div className="runtime-heading"><div><div><Badge tone={migrationStatusTone(job.status)}>{migrationStatusLabel(job.status)}</Badge><h2>{job.name}</h2></div><p>{job.sourceName} / {job.sourceDatabase} · 任务 ID {job.id}</p></div><div>{!failed && job.status !== "completed" && <Button variant="danger" onClick={cancel}><Square size={15} />停止任务</Button>}</div></div>
      <Card className="overall-progress"><div><span>总体进度 · {stageLabel(job.stage)}</span><strong>{job.progress}%</strong></div><div className="large-progress"><i style={{ width: `${job.progress}%` }} /></div><footer><span>目标：{job.destination}</span><span>{job.artifactName ? `文件：${job.artifactName}` : "等待生成备份文件"}</span></footer></Card>
      <div className="runtime-metrics"><RuntimeMetric icon={<Gauge />} label="当前阶段" value={stageLabel(job.stage)} detail="导出与上传分阶段执行" /><RuntimeMetric icon={<FileArchive />} label="备份文件" value={job.artifactName || "待生成"} detail={formatBytes(job.artifactSize ?? 0)} /><RuntimeMetric icon={<Clock3 />} label="运行时长" value={formatDuration(elapsed * 1000)} detail="从开始到现在" /><RuntimeMetric icon={<AlertTriangle />} label="错误事件" value={String(job.events.filter((item) => item.level === "ERROR").length)} detail={failed ? "任务已停止" : "持续监控"} warning={failed} /></div>
      <div className="monitor-grid">
        <Card className="phase-card"><h3>执行阶段</h3>{stages.map(([id,label],index) => <div className={`phase ${index < currentStage || job.status === "completed" ? "done" : index === currentStage && !failed ? "active" : ""}`} key={id}><span>{index < currentStage || job.status === "completed" ? <Check size={13} /> : failed && index === currentStage ? <CircleX size={13}/> : index + 1}</span><strong>{label}</strong><small>{index < currentStage ? "完成" : index === currentStage ? failed ? "失败" : "进行中" : "等待中"}</small></div>)}</Card>
        <Card className="chart-card migration-detail"><div className="section-title compact"><div><h3>备份产物</h3><p>文件生成后会显示名称和大小。</p></div><Badge tone="gray">{job.destinationType}</Badge></div><div className="backup-artifact"><Archive size={34} /><strong>{job.artifactName || "等待备份文件"}</strong><span>{formatBytes(job.artifactSize ?? 0)}</span><small>{job.destination}</small></div></Card>
      </div>
      <Card className="event-card"><div className="section-title compact"><div><h3>实时事件</h3><p>查看备份过程中的关键日志。</p></div></div>{[...job.events].reverse().slice(0, 12).map((event,index)=><div className="event-row" key={`${event.time}-${index}`}><time>{new Date(event.time).toLocaleTimeString("zh-CN",{hour12:false})}</time><Archive size={15}/><span>{event.message}</span><Badge tone={event.level === "ERROR" ? "red" : event.level === "WARN" ? "orange" : "green"}>{event.level}</Badge></div>)}</Card>
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

function stageLabel(stage: string) {
  if (stage === "export") return "数据库导出";
  if (stage === "upload") return "写入存储";
  if (stage === "completed") return "备份完成";
  if (stage === "failed") return "执行失败";
  if (stage === "cancelled") return "已取消";
  return "任务排队";
}

function formatBytes(value: number) {
  if (!value) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  return `${(value / 1024 ** index).toFixed(index > 2 ? 1 : 0)} ${units[index]}`;
}

function formatDuration(value: number) { const seconds = Math.round(value / 1000); return seconds < 60 ? `${seconds}s` : `${Math.floor(seconds / 60)}m ${seconds % 60}s`; }
