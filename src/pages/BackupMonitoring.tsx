import { AlertTriangle, Archive, Check, ChevronDown, ChevronRight, CircleX, Clock3, FileArchive, Gauge, Pencil, Play, Square } from "lucide-react";
import { type FormEvent, useEffect, useState } from "react";
import { api } from "../api";
import { migrationStatusLabel, migrationStatusTone } from "../status";
import type { BackupConfig, BackupJob } from "../types";
import { Badge, Button, Card, SelectField } from "../components/UI";
import { TimeSelect } from "./BackupWizard";

const stages = [
  ["pending", "任务排队"],
  ["export", "数据库导出"],
  ["upload", "写入存储"],
  ["completed", "备份完成"],
];

export function BackupMonitoring({ jobId, onComplete }: { jobId: string; onComplete: (job: BackupJob) => void }) {
  const [job, setJob] = useState<BackupJob | null>(null);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState(false);
  const [collapsedRuns, setCollapsedRuns] = useState<Record<string, boolean>>({});
  const [triggering, setTriggering] = useState(false);

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

  async function saveSchedule(config: BackupConfig) {
    const next = await api.updateBackup(jobId, config);
    setJob(next);
    onComplete(next);
    setEditing(false);
  }

  async function triggerNow() {
    setTriggering(true);
    setError("");
    try {
      const next = await api.triggerBackup(jobId);
      setJob(next);
      pollBackup();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "触发备份失败");
    } finally {
      setTriggering(false);
    }
  }

  function pollBackup() {
    window.setTimeout(async () => {
      try {
        const refreshed = await api.backup(jobId);
        setJob(refreshed);
        if (refreshed.status === "running" || refreshed.status === "pending") {
          pollBackup();
        } else {
          onComplete(refreshed);
        }
      } catch (reason) {
        setError(reason instanceof Error ? reason.message : "无法获取备份任务状态");
      }
    }, 900);
  }

  if (!job) return <div className="empty-state"><div>DB</div><h2>任务准备中</h2><p>{error || "正在准备备份任务..."}</p></div>;

  const scheduled = job.status === "scheduled";
  const failed = job.status === "failed" || job.status === "cancelled";
  const elapsed = Math.max(1, (Date.now() - new Date(job.startedAt).getTime()) / 1000);
  const currentStage = stageIndex(job.stage);
  const eventRuns = groupBackupEvents(job);

  return (
    <div className="page-stack">
      {error && <div className="error-banner">{error}</div>}
      {failed && <div className="error-banner">{job.error || "备份任务已取消"}</div>}
      <div className="runtime-heading"><div><div><Badge tone={migrationStatusTone(job.status)}>{migrationStatusLabel(job.status)}</Badge><h2>{job.name}</h2></div><p>{job.sourceName} / {job.sourceDatabase} · 任务 ID {job.id}</p></div><div>{scheduled && <Button variant="secondary" disabled={triggering} onClick={triggerNow}><Play size={15} />{triggering ? "触发中..." : "立即触发"}</Button>}{scheduled && <Button variant="secondary" onClick={() => setEditing(true)}><Pencil size={15} />编辑计划</Button>}{!scheduled && !failed && job.status !== "completed" && <Button variant="danger" onClick={cancel}><Square size={15} />停止任务</Button>}</div></div>
      <Card className="overall-progress"><div><span>总体进度 · {stageLabel(job.stage)}</span><strong>{job.progress}%</strong></div><div className="large-progress"><i style={{ width: `${job.progress}%` }} /></div><footer><span>目标：{job.destination}</span><span>{job.artifactName ? `文件：${job.artifactName}` : "等待生成备份文件"}</span></footer></Card>
      <div className="runtime-metrics"><RuntimeMetric icon={<Gauge />} label={scheduled ? "计划频率" : "当前阶段"} value={scheduled ? scheduleLabel(job) : stageLabel(job.stage)} detail={scheduled ? "定时自动执行" : "导出与上传分阶段执行"} /><RuntimeMetric icon={<FileArchive />} label="备份文件" value={job.artifactName || "待生成"} detail={formatBytes(job.artifactSize ?? 0)} long /><RuntimeMetric icon={<Clock3 />} label={scheduled ? "下次执行" : "运行时长"} value={scheduled ? formatDateTime(job.nextRunAt) : formatDuration(elapsed * 1000)} detail={scheduled ? "由后端调度器触发" : "从开始到现在"} /><RuntimeMetric icon={<AlertTriangle />} label="错误事件" value={String(job.events.filter((item) => item.level === "ERROR").length)} detail={failed ? "任务已停止" : scheduled ? "计划可执行" : "持续监控"} warning={failed} /></div>
      <div className="monitor-grid">
        <Card className="phase-card"><h3>执行阶段</h3>{stages.map(([id,label],index) => <div className={`phase ${index < currentStage || job.status === "completed" ? "done" : index === currentStage && !failed ? "active" : ""}`} key={id}><span>{index < currentStage || job.status === "completed" ? <Check size={13} /> : failed && index === currentStage ? <CircleX size={13}/> : index + 1}</span><strong>{label}</strong><small>{index < currentStage ? "完成" : index === currentStage ? failed ? "失败" : "进行中" : "等待中"}</small></div>)}</Card>
        <Card className="chart-card migration-detail"><div className="section-title compact"><div><h3>备份产物</h3><p>文件生成后会显示名称和大小。</p></div><Badge tone="gray">{job.destinationType}</Badge></div><div className="backup-artifact"><Archive size={34} /><strong>{job.artifactName ? formatArtifactName(job.artifactName) : "等待备份文件"}</strong><span>{formatBytes(job.artifactSize ?? 0)}</span><small>{job.destination}</small></div></Card>
      </div>
      <Card className="event-card"><div className="section-title compact"><div><h3>{scheduled ? "定时任务执行记录" : "实时事件"}</h3><p>{scheduled ? "按每次触发的具体任务查看事件。" : "查看备份过程中的关键日志。"}</p></div></div>{eventRuns.map((run) => {
        const collapsed = collapsedRuns[run.id] ?? false;
        return <div className="backup-run" key={run.id}><button type="button" className="backup-run-head" onClick={() => setCollapsedRuns((current) => ({ ...current, [run.id]: !collapsed }))}>{collapsed ? <ChevronRight size={15} /> : <ChevronDown size={15} />}<div><strong>{run.title}</strong><small>{run.subtitle}</small></div><Badge tone={run.failed ? "red" : run.completed ? "green" : "blue"}>{run.failed ? "失败" : run.completed ? "完成" : "执行中"}</Badge></button>{!collapsed && run.events.map((event,index)=><div className="event-row" key={`${run.id}-${event.time}-${index}`}><time>{new Date(event.time).toLocaleTimeString("zh-CN",{hour12:false})}</time><Archive size={15}/><span>{event.message}</span><Badge tone={event.level === "ERROR" ? "red" : event.level === "WARN" ? "orange" : "green"}>{event.level}</Badge></div>)}</div>;
      })}</Card>
      {editing && <ScheduleEditor job={job} onClose={() => setEditing(false)} onSave={saveSchedule} />}
    </div>
  );
}

function RuntimeMetric({ icon, label, value, detail, warning = false, long = false }: { icon: React.ReactNode; label: string; value: string; detail: string; warning?: boolean; long?: boolean }) {
  return <Card className={`runtime-metric ${long ? "long-text" : ""}`}><span className={warning ? "warning" : ""}>{icon}</span><div><small>{label}</small><strong>{value}</strong><em className={warning ? "warning" : ""}>{detail}</em></div></Card>;
}

function groupBackupEvents(job: BackupJob) {
  const events = [...job.events].sort((left, right) => new Date(left.time).getTime() - new Date(right.time).getTime());
  const runs: Array<{ id: string; title: string; subtitle: string; events: BackupJob["events"]; failed: boolean; completed: boolean }> = [];
  const scheduled = job.scheduleMode === "scheduled";
  let current: (typeof runs)[number] | null = null;
  const planEvents: BackupJob["events"] = [];

  if (!scheduled) {
    return [{
      id: "single-run",
      title: "本次备份",
      subtitle: events[0] ? new Date(events[0].time).toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false }) : "等待事件",
      events: events.reverse(),
      failed: events.some((event) => event.level === "ERROR"),
      completed: events.some((event) => event.message.includes("备份任务完成")),
    }];
  }

  events.forEach((event) => {
    const isScheduledRunStart = event.message.includes("定时备份已触发");
    const isManualRunStart = event.message.includes("手动触发定时备份");
    if (isScheduledRunStart || isManualRunStart) {
      current = {
        id: `${event.time}-${runs.length}`,
        title: `${isManualRunStart ? "手动执行" : "自动执行"} · 第 ${runs.length + 1} 次`,
        subtitle: new Date(event.time).toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false }),
        events: [],
        failed: false,
        completed: false,
      };
      runs.push(current);
    }
    if (!current) {
      planEvents.push(event);
      return;
    }
    current.events.push(event);
    if (event.level === "ERROR") current.failed = true;
    if (event.message.includes("备份任务完成")) current.completed = true;
  });
  if (!runs.length && planEvents.length) {
    runs.push({
      id: "plan-events",
      title: "计划事件",
      subtitle: "尚未触发执行",
      events: planEvents,
      failed: planEvents.some((event) => event.level === "ERROR"),
      completed: false,
    });
  }
  return runs.reverse();
}

function stageIndex(stage: string) {
  const index = stages.findIndex(([id]) => id === stage);
  return index >= 0 ? index : 0;
}

function stageLabel(stage: string) {
  if (stage === "scheduled") return "等待调度";
  if (stage === "export") return "数据库导出";
  if (stage === "upload") return "写入存储";
  if (stage === "completed") return "备份完成";
  if (stage === "failed") return "执行失败";
  if (stage === "cancelled") return "已取消";
  return "任务排队";
}

const weekdays = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];

function ScheduleEditor({ job, onClose, onSave }: { job: BackupJob; onClose: () => void; onSave: (config: BackupConfig) => Promise<void> }) {
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [testingNotification, setTestingNotification] = useState(false);
  const [notificationTestResult, setNotificationTestResult] = useState("");
  const [form, setForm] = useState<BackupConfig>({
    name: job.name,
    sourceId: "",
    sourceDatabase: job.sourceDatabase,
    sourceDatabases: job.sourceDatabase.split(",").map((item) => item.trim()).filter(Boolean),
    destinationType: "storage",
    destination: "",
    scheduleMode: "scheduled",
    scheduleFrequency: job.scheduleFrequency ?? "daily",
    scheduleTime: job.scheduleTime ?? "02:00:00",
    scheduleWeekday: job.scheduleWeekday ?? 1,
    scheduleMonthDay: job.scheduleMonthDay ?? 1,
    notificationEnabled: job.notificationEnabled ?? false,
    notificationMethod: job.notificationMethod ?? "webhook",
    notificationTarget: job.notificationTarget ?? "",
  });

  async function submit(event: FormEvent) {
    event.preventDefault();
    setSaving(true);
    setError("");
    try {
      await onSave(form);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "保存计划失败");
      setSaving(false);
    }
  }

  async function testNotification() {
    setTestingNotification(true);
    setNotificationTestResult("");
    setError("");
    try {
      const result = await api.testBackupNotification(form.notificationMethod ?? "webhook", form.notificationTarget?.trim() ?? "");
      setNotificationTestResult(result.message);
    } catch (reason) {
      setNotificationTestResult(reason instanceof Error ? reason.message : "测试通知发送失败");
    } finally {
      setTestingNotification(false);
    }
  }

  return (
    <div className="modal-backdrop">
      <form className="modal schedule-editor-modal" onSubmit={submit}>
        <div className="modal-title"><div><h3>编辑备份计划</h3><p>{job.sourceName} / {job.sourceDatabase}</p></div></div>
        {error && <div className="error-banner">{error}</div>}
        <label className="field"><span>任务名称</span><input value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} /></label>
        <div className="schedule-grid compact">
          <SelectField label="频率" value={form.scheduleFrequency ?? "daily"} onChange={(scheduleFrequency) => setForm({ ...form, scheduleFrequency: scheduleFrequency as BackupConfig["scheduleFrequency"] })}><option value="daily">每天</option><option value="weekly">每周</option><option value="monthly">每月</option></SelectField>
          <TimeSelect value={form.scheduleTime ?? "02:00:00"} onChange={(scheduleTime) => setForm({ ...form, scheduleTime })} />
          {form.scheduleFrequency === "weekly" && <SelectField label="每周" value={form.scheduleWeekday ?? 1} onChange={(scheduleWeekday) => setForm({ ...form, scheduleWeekday: Number(scheduleWeekday) })}>{weekdays.map((item, index) => <option key={item} value={index}>{item}</option>)}</SelectField>}
          {form.scheduleFrequency === "monthly" && <SelectField label="每月" value={form.scheduleMonthDay ?? 1} onChange={(scheduleMonthDay) => setForm({ ...form, scheduleMonthDay: Number(scheduleMonthDay) })}>{Array.from({ length: 28 }, (_, index) => index + 1).map((day) => <option key={day} value={day}>{day} 日</option>)}</SelectField>}
        </div>
        <div className="notification-panel modal-notification-panel">
          <button type="button" className="toggle-row" onClick={() => setForm({ ...form, notificationEnabled: !form.notificationEnabled })}><span className={`switch ${form.notificationEnabled ? "on" : ""}`}><i /></span><div><strong>备份通知</strong><small>定时任务执行结束后发送成功或失败结果</small></div></button>
          {form.notificationEnabled && (
            <div className="notification-grid">
              <SelectField label="通知方式" value={form.notificationMethod ?? "webhook"} onChange={(notificationMethod) => { setNotificationTestResult(""); setForm({ ...form, notificationMethod: notificationMethod as BackupConfig["notificationMethod"], notificationTarget: "" }); }}><option value="webhook">Webhook</option><option value="email">Email</option></SelectField>
              <div className="notification-target-row"><label className="field"><span>{form.notificationMethod === "email" ? "收件邮箱" : "Webhook URL"}</span><input value={form.notificationTarget ?? ""} onChange={(event) => { setNotificationTestResult(""); setForm({ ...form, notificationTarget: event.target.value }); }} placeholder={form.notificationMethod === "email" ? "ops@databridge.local" : "https://example.com/webhook"} /></label><Button type="button" variant="secondary" disabled={testingNotification || !form.notificationTarget?.trim()} onClick={testNotification}>{testingNotification ? "测试中..." : "测试"}</Button></div>
            </div>
          )}
          {notificationTestResult && <p className="notification-test-result">{notificationTestResult}</p>}
        </div>
        <div className="modal-actions"><Button type="button" variant="secondary" onClick={onClose}>取消</Button><Button disabled={saving} type="submit">{saving ? "保存中..." : "保存计划"}</Button></div>
      </form>
    </div>
  );
}

function scheduleLabel(job: BackupJob) {
  if (job.scheduleFrequency === "weekly") return `每周${weekdays[job.scheduleWeekday ?? 1]} ${job.scheduleTime}`;
  if (job.scheduleFrequency === "monthly") return `每月 ${job.scheduleMonthDay ?? 1} 日 ${job.scheduleTime}`;
  return `每天 ${job.scheduleTime ?? "02:00:00"}`;
}

function formatDateTime(value?: string) {
  if (!value) return "待调度";
  return new Date(value).toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false });
}

function formatBytes(value: number) {
  if (!value) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  return `${(value / 1024 ** index).toFixed(index > 2 ? 1 : 0)} ${units[index]}`;
}

function formatArtifactName(value: string) {
  return value.split("/").map((part, index) => index === 0 ? part : <span key={`${part}-${index}`}>/{part}</span>);
}

function formatDuration(value: number) { const seconds = Math.round(value / 1000); return seconds < 60 ? `${seconds}s` : `${Math.floor(seconds / 60)}m ${seconds % 60}s`; }
