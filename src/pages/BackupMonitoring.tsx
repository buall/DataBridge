import { AlertTriangle, Archive, Check, ChevronDown, ChevronRight, CircleX, Clock3, Clipboard, Copy, DatabaseBackup, Download, FileArchive, Gauge, HardDrive, Pencil, Play, Square, Trash2 } from "lucide-react";
import { type FormEvent, useEffect, useRef, useState } from "react";
import { api } from "../api";
import { migrationStatusLabel, migrationStatusTone } from "../status";
import type { BackupConfig, BackupJob, DatabaseConnection } from "../types";
import { Badge, Button, Card, ConfirmDialog, DatabaseTypeIcon, RegionFlag, SelectField } from "../components/UI";
import { TimeSelect } from "./BackupWizard";

type BackupArtifactItem = { database: string; name: string; size?: number; path?: string };
export type BackupArtifactVersionOption = { id: string; label: string; startedAt: string; finishedAt?: string; status: BackupJob["status"]; artifacts: BackupArtifactItem[]; current?: boolean };

const backupStages = [
  ["pending", "任务排队"],
  ["export", "数据库导出"],
  ["upload", "写入存储"],
  ["completed", "备份完成"],
];

const restoreStages = [
  ["pending", "任务排队"],
  ["download", "下载备份"],
  ["extract", "备份解压"],
  ["restore", "目标导入"],
  ["validation", "恢复校验"],
  ["completed", "恢复完成"],
];

export function BackupMonitoring({
  jobId,
  onComplete,
  onOpenRestore,
  onOpenArtifacts,
}: {
  jobId: string;
  onComplete: (job: BackupJob) => void;
  onOpenRestore: (job: BackupJob) => void;
  onOpenArtifacts: (job: BackupJob) => void;
}) {
  const [job, setJob] = useState<BackupJob | null>(null);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState(false);
  const [collapsedRuns, setCollapsedRuns] = useState<Record<string, boolean>>({});
  const [triggering, setTriggering] = useState(false);
  const [cancelConfirmOpen, setCancelConfirmOpen] = useState(false);
  const [cancelling, setCancelling] = useState(false);
  const [artifactError, setArtifactError] = useState("");

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
    setCancelling(true);
    try {
      await api.cancelBackup(jobId);
      setCancelConfirmOpen(false);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "停止备份任务失败");
    } finally {
      setCancelling(false);
    }
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
  const stageItems = job.destinationType === "restore" ? restoreStages : backupStages;
  const currentStage = stageIndex(job.stage, stageItems);
  const eventRuns = groupBackupEvents(job);
  const lastError = latestErrorEvent(job);

  return (
    <div className="page-stack">
      {error && <div className="error-banner">{error}</div>}
      {failed && <div className="error-banner detail-error"><strong>{job.error || "备份任务已取消"}</strong>{lastError && <span>{lastError}</span>}</div>}
      <div className="runtime-heading"><div><div><Badge tone={migrationStatusTone(job.status)}>{migrationStatusLabel(job.status)}</Badge><h2>{job.name}</h2></div><p>{job.sourceName} / {job.sourceDatabase} · 任务 ID {job.id}</p></div><div><Button variant="secondary" onClick={() => onOpenArtifacts(job)}><Archive size={15} />备份产物</Button>{canShowRestorePanel(job) && <Button variant="secondary" onClick={() => onOpenRestore(job)}><DatabaseBackup size={15} />还原数据</Button>}{scheduled && <Button variant="secondary" disabled={triggering} onClick={triggerNow}><Play size={15} />{triggering ? "触发中..." : "立即触发"}</Button>}{scheduled && <Button variant="secondary" onClick={() => setEditing(true)}><Pencil size={15} />编辑计划</Button>}{!scheduled && !failed && job.status !== "completed" && <Button variant="danger" onClick={() => setCancelConfirmOpen(true)}><Square size={15} />停止任务</Button>}</div></div>
      <Card className="overall-progress"><div><span>总体进度 · {stageLabel(job.stage)}</span><strong>{job.progress}%</strong></div><div className="large-progress"><i style={{ width: `${job.progress}%` }} /></div><footer><span>目标：{job.destination}</span><span>{job.artifactName ? `文件：${job.artifactName}` : "等待生成备份文件"}</span></footer></Card>
      <div className="runtime-metrics"><RuntimeMetric icon={<Gauge />} label={scheduled ? "计划频率" : "当前阶段"} value={scheduled ? scheduleLabel(job) : stageLabel(job.stage)} detail={scheduled ? "定时自动执行" : "导出与上传分阶段执行"} /><RuntimeMetric icon={<FileArchive />} label="备份文件" value={job.artifactName || "待生成"} detail={formatBytes(job.artifactSize ?? 0)} long /><RuntimeMetric icon={<Clock3 />} label={scheduled ? "下次执行" : "运行时长"} value={scheduled ? formatDateTime(job.nextRunAt) : formatDuration(elapsed * 1000)} detail={scheduled ? "由后端调度器触发" : "从开始到现在"} /><RuntimeMetric icon={<AlertTriangle />} label="错误事件" value={String(job.events.filter((item) => item.level === "ERROR").length)} detail={failed ? "任务已停止" : scheduled ? "计划可执行" : "持续监控"} warning={failed} /></div>
      <div className="monitor-grid single">
        <Card className="phase-card"><h3>执行阶段</h3>{stageItems.map(([id,label],index) => <div className={`phase ${index < currentStage || job.status === "completed" ? "done" : index === currentStage && !failed ? "active" : ""}`} key={id}><span>{index < currentStage || job.status === "completed" ? <Check size={13} /> : failed && index === currentStage ? <CircleX size={13}/> : index + 1}</span><strong>{label}</strong><small>{index < currentStage ? "完成" : index === currentStage ? failed ? "失败" : "进行中" : "等待中"}</small></div>)}</Card>
      </div>
      <Card className="event-card"><div className="section-title compact"><div><h3>{scheduled ? "定时任务执行记录" : "实时事件"}</h3><p>{scheduled ? "按每次触发的具体任务查看事件。" : "查看备份过程中的关键日志。"}</p></div></div>{eventRuns.map((run) => {
        const collapsed = collapsedRuns[run.id] ?? scheduled;
        return <div className="backup-run" key={run.id}><button type="button" className="backup-run-head" onClick={() => setCollapsedRuns((current) => ({ ...current, [run.id]: !collapsed }))}>{collapsed ? <ChevronRight size={15} /> : <ChevronDown size={15} />}<div><strong>{run.title}</strong><small>{run.subtitle}</small></div><Badge tone={run.failed ? "red" : run.completed ? "green" : "blue"}>{run.failed ? "失败" : run.completed ? "完成" : "执行中"}</Badge></button>{!collapsed && run.events.map((event,index)=><div className="event-row" key={`${run.id}-${event.time}-${index}`}><time>{new Date(event.time).toLocaleTimeString("zh-CN",{hour12:false})}</time><Archive size={15}/><span>{event.message}</span><Badge tone={event.level === "ERROR" ? "red" : event.level === "WARN" ? "orange" : "green"}>{event.level}</Badge></div>)}</div>;
      })}</Card>
      {editing && <ScheduleEditor job={job} onClose={() => setEditing(false)} onSave={saveSchedule} />}
      {cancelConfirmOpen && <ConfirmDialog title="停止备份任务" description="这是中危操作，任务会被取消，当前执行进度不会继续推进。" tone="warning" confirmText="确认停止" submitting={cancelling} onCancel={() => setCancelConfirmOpen(false)} onConfirm={cancel} details={[`任务：${job.name}`, "已生成的历史备份产物不会被自动删除。"]} />}
    </div>
  );
}

function RuntimeMetric({ icon, label, value, detail, warning = false, long = false }: { icon: React.ReactNode; label: string; value: string; detail: string; warning?: boolean; long?: boolean }) {
  return <Card className={`runtime-metric ${long ? "long-text" : ""}`}><span className={warning ? "warning" : ""}>{icon}</span><div><small>{label}</small><strong>{value}</strong><em className={warning ? "warning" : ""}>{detail}</em></div></Card>;
}

export function restoreSourceDatabases(job: BackupJob) {
  const names = job.artifacts?.map((artifact) => artifact.database).filter(Boolean) ?? [];
  if (!names.length) {
    names.push(...job.sourceDatabase.split(",").map((item) => item.trim()).filter(Boolean));
  }
  return Array.from(new Set(names));
}

function latestErrorEvent(job: BackupJob) {
  const event = [...job.events].reverse().find((item) => item.level === "ERROR");
  if (!event || event.message === job.error) return "";
  return event.message;
}

export function canShowRestorePanel(job: BackupJob) {
  return (job.destinationType === "local" || job.destinationType === "object") && (hasBackupArtifact(job) || job.status === "scheduled" || job.status === "completed");
}

export function canRestoreFromJob(job: BackupJob) {
  return (job.destinationType === "local" || job.destinationType === "object") && hasBackupArtifact(job) && job.status !== "running" && job.status !== "pending";
}

export function hasBackupArtifact(job: BackupJob) {
  return Boolean(job.artifactName || job.artifacts?.some((artifact) => artifact.name));
}

function restoreUnavailableHint(job: BackupJob) {
  if (job.status === "running" || job.status === "pending") return "任务正在执行，当前备份产物暂不可恢复。";
  return "备份生成文件后才可以启动恢复。";
}

export function backupArtifacts(job: BackupJob): BackupArtifactItem[] {
  if (job.artifacts?.length) {
    return job.artifacts.filter((artifact) => artifact.name).map((artifact) => ({
      database: artifact.database || job.sourceDatabase || "default",
      name: artifact.name,
      size: artifact.size,
      path: artifact.path,
    }));
  }
  if (!job.artifactName) return [];
  return [{
    database: job.sourceDatabase || "default",
    name: job.artifactName,
    size: job.artifactSize,
    path: job.artifactPath,
  }];
}

export function artifactVersionsForRestore(job: BackupJob): BackupArtifactVersionOption[] {
  const versions = (job.artifactVersions ?? [])
    .filter((version) => version.artifacts?.some((artifact) => artifact.name))
    .map((version): BackupArtifactVersionOption => ({
      id: version.id,
      label: formatDateTime(version.finishedAt ?? version.startedAt),
      startedAt: version.startedAt,
      finishedAt: version.finishedAt,
      status: version.status,
      artifacts: (version.artifacts ?? []).filter((artifact) => artifact.name).map((artifact) => ({
        database: artifact.database || job.sourceDatabase || "default",
        name: artifact.name,
        size: artifact.size,
        path: artifact.path,
      })),
      current: false,
    }))
    .sort((left, right) => new Date(right.finishedAt ?? right.startedAt).getTime() - new Date(left.finishedAt ?? left.startedAt).getTime());
  if (versions.length) return versions;
  if (!canRestoreFromJob(job)) return [];
  return [{
    id: "current",
    label: formatDateTime(job.finishedAt ?? job.lastRunAt ?? job.startedAt),
    startedAt: job.startedAt,
    finishedAt: job.finishedAt ?? job.lastRunAt,
    status: "completed",
    artifacts: backupArtifacts(job),
    current: true,
  }];
}

export function totalArtifactSize(artifacts: BackupArtifactItem[]) {
  return artifacts.reduce((sum, artifact) => sum + (artifact.size ?? 0), 0);
}

export function storageTypeLabel(value: string) {
  if (value === "local") return "本地目录";
  if (value === "object") return "对象存储";
  if (value === "restore") return "恢复任务";
  return value || "备份目标";
}

export function RestoreConnectionSelect({ connectionId, connections, onChange }: { connectionId: string; connections: DatabaseConnection[]; onChange: (value: string) => void }) {
  const connection = connections.find((item) => item.id === connectionId) ?? connections[0];
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!open) return;
    const close = (event: MouseEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    window.addEventListener("mousedown", close);
    window.addEventListener("keydown", closeOnEscape);
    return () => {
      window.removeEventListener("mousedown", close);
      window.removeEventListener("keydown", closeOnEscape);
    };
  }, [open]);

  useEffect(() => {
    if (connectionId) return;
    if (connections[0]) onChange(connections[0].id);
  }, [connectionId, connections, onChange]);

  return (
    <div className="field select-field restore-connection-field" ref={rootRef}>
      <span>目标连接</span>
      <button type="button" className="select-trigger restore-connection-trigger" aria-haspopup="listbox" aria-expanded={open} onClick={() => setOpen((value) => !value)}>
        <span className="endpoint-instance-trigger-content">
          {connection && <DatabaseTypeIcon type={connection.type} />}
          {connection?.name ?? "请选择连接"}
          {connection?.host ? ` · ${connection.host}` : ""}
          {connection?.region && connection.region !== "内网" && <RegionFlag region={connection.region} />}
        </span>
        <ChevronDown size={16} />
      </button>
      {open && (
        <div className="endpoint-instance-menu restore-connection-menu" role="listbox" aria-label="目标连接列表">
          {connections.map((item) => (
            <button
              type="button"
              key={item.id}
              className={`endpoint-instance-option ${item.id === connectionId ? "active" : ""}`}
              onClick={() => { onChange(item.id); setOpen(false); }}
            >
              <div>
                <strong>
                  <DatabaseTypeIcon type={item.type} />
                  {item.name} · {item.host}
                  {item.region && item.region !== "内网" && <RegionFlag region={item.region} />}
                </strong>
              </div>
            </button>
          ))}
        </div>
      )}
    </div>
  );
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

function stageIndex(stage: string, stages: string[][]) {
  const index = stages.findIndex(([id]) => id === stage);
  return index >= 0 ? index : 0;
}

function stageLabel(stage: string) {
  if (stage === "scheduled") return "等待调度";
  if (stage === "export") return "数据库导出";
  if (stage === "download") return "下载备份";
  if (stage === "extract") return "备份解压";
  if (stage === "restore") return "目标导入";
  if (stage === "validation") return "恢复校验";
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

export function formatDateTime(value?: string) {
  if (!value) return "待调度";
  return new Date(value).toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false });
}

export function formatBytes(value: number) {
  if (!value) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  return `${(value / 1024 ** index).toFixed(index > 2 ? 1 : 0)} ${units[index]}`;
}

function formatArtifactName(value: string) {
  return value;
}

function formatDuration(value: number) { const seconds = Math.round(value / 1000); return seconds < 60 ? `${seconds}s` : `${Math.floor(seconds / 60)}m ${seconds % 60}s`; }
