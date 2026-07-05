import { AlertTriangle, Archive, Check, ChevronDown, ChevronRight, CircleX, Clock3, Clipboard, Copy, DatabaseBackup, Download, FileArchive, Gauge, HardDrive, Pencil, Play, Square, Trash2 } from "lucide-react";
import { type FormEvent, useEffect, useRef, useState } from "react";
import { api } from "../api";
import { migrationStatusLabel, migrationStatusTone } from "../status";
import type { BackupConfig, BackupJob, DatabaseConnection, DatabaseInfo, RemoteStorage } from "../types";
import { Badge, Button, Card, ConfirmDialog, DatabaseTypeIcon, RegionFlag, SelectField } from "../components/UI";
import { BackupTargetSelector, buildBackupTargets, DatabaseMultiSelect, normalizeTargetSelection, NotificationTriggerSelector, RetentionDaysField, selectedBackupTargetIds, TimeSelect } from "./BackupWizard";
import { NotificationRecipientPicker, useNotificationRecipients } from "../components/NotificationRecipientPicker";

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
  connections,
  storages,
  onComplete,
  onOpenRestore,
  onOpenArtifacts,
}: {
  jobId: string;
  connections: DatabaseConnection[];
  storages: RemoteStorage[];
  onComplete: (job: BackupJob) => void;
  onOpenRestore: (job: BackupJob) => void;
  onOpenArtifacts: (job: BackupJob) => void;
}) {
  const [job, setJob] = useState<BackupJob | null>(null);
  const [detailedJob, setDetailedJob] = useState<BackupJob | null>(null);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState(false);
  const [collapsedRuns, setCollapsedRuns] = useState<Record<string, boolean>>({});
  const [loadingRunId, setLoadingRunId] = useState("");
  const [triggering, setTriggering] = useState(false);
  const [cancelConfirmOpen, setCancelConfirmOpen] = useState(false);
  const [cancelling, setCancelling] = useState(false);
  const [artifactError, setArtifactError] = useState("");

  useEffect(() => {
    let stopped = false;
    async function refresh() {
      try {
        const next = await api.backup(jobId, true);
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
        const refreshed = await api.backup(jobId, true);
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

  const scheduled = job.scheduleMode === "scheduled" || job.status === "scheduled";
  const latestRunStatus = job.artifactVersions?.[0]?.status;
  const failed = job.status === "failed" || job.status === "cancelled" || latestRunStatus === "failed" || latestRunStatus === "cancelled";
  const elapsed = Math.max(1, (Date.now() - new Date(job.startedAt).getTime()) / 1000);
  const stageItems = job.destinationType === "restore" ? restoreStages : backupStages;
  const currentStage = stageIndex(job.stage, stageItems);
  const eventRuns = groupBackupEvents(job);
  const lastError = latestErrorEvent(job);

  async function toggleRun(run: BackupEventRun, collapsed: boolean) {
    setCollapsedRuns((current) => ({ ...current, [run.id]: !collapsed }));
    if (!collapsed || detailedJob || !run.startedAt) return;
    setLoadingRunId(run.id);
    try {
      const next = await api.backup(jobId);
      setDetailedJob(next);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "无法加载本次备份日志");
    } finally {
      setLoadingRunId("");
    }
  }

  return (
    <div className="page-stack">
      {error && <div className="error-banner">{error}</div>}
      {failed && <div className="error-banner detail-error"><strong>{job.error || "备份任务已取消"}</strong>{lastError && <span>{lastError}</span>}</div>}
      <div className="runtime-heading"><div><div><Badge tone={migrationStatusTone(job.status)}>{migrationStatusLabel(job.status)}</Badge><h2>{job.name}</h2></div><p>{job.sourceName} / {job.sourceDatabase} · 任务 ID {job.id}</p></div><div className="runtime-actions"><Button variant="secondary" className="action-blue" onClick={() => onOpenArtifacts(job)}><Archive size={15} />备份产物</Button>{canShowRestorePanel(job) && <Button variant="secondary" className="action-green" onClick={() => onOpenRestore(job)}><DatabaseBackup size={15} />还原数据</Button>}{scheduled && <Button variant="secondary" className="action-indigo" disabled={triggering} onClick={triggerNow}><Play size={15} />{triggering ? "触发中..." : "立即触发"}</Button>}{scheduled && <Button variant="secondary" className="action-amber" onClick={() => setEditing(true)}><Pencil size={15} />编辑计划</Button>}{!scheduled && !failed && job.status !== "completed" && <Button variant="danger" onClick={() => setCancelConfirmOpen(true)}><Square size={15} />停止任务</Button>}</div></div>
      <Card className="overall-progress"><div><span>总体进度 · {stageLabel(job.stage)}</span><strong>{job.progress}%</strong></div><div className="large-progress"><i style={{ width: `${job.progress}%` }} /></div><footer><span>目标：{job.destination}</span><span>{job.artifactName ? `文件：${job.artifactName}` : "等待生成备份文件"}</span></footer></Card>
      <div className="runtime-metrics"><RuntimeMetric icon={<Gauge />} label={scheduled ? "计划频率" : "当前阶段"} value={scheduled ? scheduleLabel(job) : stageLabel(job.stage)} detail={scheduled ? "定时自动执行" : "导出与上传分阶段执行"} /><RuntimeMetric icon={<FileArchive />} label={scheduled ? "保留期限" : "备份文件"} value={scheduled ? retentionLabel(retentionDaysFromJob(job)) : job.artifactName || "待生成"} detail={scheduled ? "按保留期限自动清理" : formatBytes(job.artifactSize ?? 0)} long /><RuntimeMetric icon={<Clock3 />} label={scheduled ? "下次执行" : "运行时长"} value={scheduled ? formatDateTime(job.nextRunAt) : formatDuration(elapsed * 1000)} detail={scheduled ? "由后端调度器触发" : "从开始到现在"} /><RuntimeMetric icon={<AlertTriangle />} label="错误事件" value={String(job.events.filter((item) => item.level === "ERROR").length)} detail={failed ? scheduled ? "上次执行失败，计划仍有效" : "任务已停止" : scheduled ? "计划可执行" : "持续监控"} warning={failed} /></div>
      <div className="monitor-grid single">
        <Card className="phase-card"><h3>执行阶段</h3>{stageItems.map(([id,label],index) => <div className={`phase ${index < currentStage || job.status === "completed" ? "done" : index === currentStage && !failed ? "active" : ""}`} key={id}><span>{index < currentStage || job.status === "completed" ? <Check size={13} /> : failed && index === currentStage ? <CircleX size={13}/> : index + 1}</span><strong>{label}</strong><small>{index < currentStage ? "完成" : index === currentStage ? failed ? "失败" : "进行中" : "等待中"}</small></div>)}</Card>
      </div>
      <Card className="event-card"><div className="section-title compact"><div><h3>{scheduled ? "定时任务执行记录" : "实时事件"}</h3><p>{scheduled ? "按每次触发的具体任务查看事件。" : "查看备份过程中的关键日志。"}</p></div></div>{eventRuns.map((run) => {
        const collapsed = collapsedRuns[run.id] ?? scheduled;
        const detailEvents = !collapsed ? detailedEventsForRun(detailedJob, run) : run.events;
        const loadingThisRun = loadingRunId === run.id;
        const runKindLabel = run.kind === "manual" ? "手动执行" : run.kind === "auto" ? "自动执行" : "";
        const runTitle = runKindLabel ? run.title.replace(`${runKindLabel} · `, "") : run.title;
        return <div className="backup-run" key={run.id}><button type="button" className="backup-run-head" onClick={() => toggleRun(run, collapsed)}>{collapsed ? <ChevronRight size={15} /> : <ChevronDown size={15} />}<div><strong>{runKindLabel && <span className={`run-kind-tag ${run.kind}`}>{runKindLabel}</span>}<span>{runTitle}</span></strong><small>{run.subtitle}</small></div><Badge tone={run.failed ? "red" : run.completed ? "green" : "blue"}>{run.failed ? "失败" : run.completed ? "完成" : "执行中"}</Badge></button>{!collapsed && loadingThisRun && <div className="event-row event-loading"><time>--:--:--</time><Archive size={15}/><span>正在加载本次备份日志...</span><Badge tone="blue">加载中</Badge></div>}{!collapsed && !loadingThisRun && detailEvents.map((event,index)=><div className="event-row" key={`${run.id}-${event.time}-${index}`}><time>{new Date(event.time).toLocaleTimeString("zh-CN",{hour12:false})}</time><Archive size={15}/><span>{event.message}</span><Badge tone={event.level === "ERROR" ? "red" : event.level === "WARN" ? "orange" : "green"}>{event.level}</Badge></div>)}</div>;
      })}</Card>
      {editing && <ScheduleEditor job={job} connections={connections} storages={storages} onClose={() => setEditing(false)} onSave={saveSchedule} />}
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
  return job.sourceType !== "etcd" && backupDestinationSupportsArtifacts(job) && (hasBackupArtifact(job) || job.status === "scheduled" || job.status === "completed");
}

export function canRestoreFromJob(job: BackupJob) {
  return job.sourceType !== "etcd" && backupDestinationSupportsArtifacts(job) && hasBackupArtifact(job) && job.status !== "running" && job.status !== "pending";
}

export function hasBackupArtifact(job: BackupJob) {
  return Boolean(job.artifactName || job.artifacts?.some((artifact) => artifact.name || artifact.copies?.some((copy) => copy.name)));
}

function backupDestinationSupportsArtifacts(job: BackupJob) {
  return job.destinationType === "local" || job.destinationType === "object" || job.destinationType === "webdav" || job.destinationType === "multi";
}

function restoreUnavailableHint(job: BackupJob) {
  if (job.status === "running" || job.status === "pending") return "任务正在执行，当前备份产物暂不可恢复。";
  return "备份生成文件后才可以启动恢复。";
}

export function backupArtifacts(job: BackupJob): BackupArtifactItem[] {
  if (job.artifacts?.length) {
    return job.artifacts
      .map((artifact) => {
        const primaryCopy = artifact.copies?.find((copy) => copy.role === "primary" && copy.name) ?? artifact.copies?.find((copy) => copy.name);
        return {
          database: artifact.database || job.sourceDatabase || "default",
          name: artifact.name || primaryCopy?.name || "",
          size: artifact.size || primaryCopy?.size,
          path: artifact.path || primaryCopy?.path,
        };
      })
      .filter((artifact) => artifact.name);
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
    .filter((version) => version.artifacts?.some((artifact) => artifact.name || artifact.copies?.some((copy) => copy.name)))
    .map((version): BackupArtifactVersionOption => ({
      id: version.id,
      label: formatDateTime(version.finishedAt ?? version.startedAt),
      startedAt: version.startedAt,
      finishedAt: version.finishedAt,
      status: version.status,
      artifacts: (version.artifacts ?? []).map((artifact) => {
        const primaryCopy = artifact.copies?.find((copy) => copy.role === "primary" && copy.name) ?? artifact.copies?.find((copy) => copy.name);
        return {
          database: artifact.database || job.sourceDatabase || "default",
          name: artifact.name || primaryCopy?.name || "",
          size: artifact.size || primaryCopy?.size,
          path: artifact.path || primaryCopy?.path,
        };
      }).filter((artifact) => artifact.name),
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
  if (value === "webdav") return "WebDAV";
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

type BackupEventRun = {
  id: string;
  kind?: "manual" | "auto" | "plan";
  title: string;
  subtitle: string;
  startedAt?: string;
  endedAt?: string;
  events: BackupJob["events"];
  failed: boolean;
  completed: boolean;
};

function groupBackupEvents(job: BackupJob): BackupEventRun[] {
  const events = [...job.events].sort((left, right) => new Date(left.time).getTime() - new Date(right.time).getTime());
  const runs: BackupEventRun[] = [];
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
        kind: isManualRunStart ? "manual" : "auto",
        title: `${isManualRunStart ? "手动执行" : "自动执行"} · 第 ${runs.length + 1} 次`,
        subtitle: new Date(event.time).toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false }),
        startedAt: event.time,
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
  runs.forEach((run, index) => {
    run.endedAt = runs[index + 1]?.startedAt;
  });
  if (!runs.length && planEvents.length) {
    runs.push({
      id: "plan-events",
      kind: "plan",
      title: "计划事件",
      subtitle: "尚未触发执行",
      startedAt: planEvents[0]?.time,
      events: planEvents,
      failed: planEvents.some((event) => event.level === "ERROR"),
      completed: false,
    });
  }
  const versions = job.artifactVersions ?? [];
  const artifactRuns = versions.map((version, index): BackupEventRun => {
    const startTime = version.startedAt || version.finishedAt || job.startedAt;
    const matchedRun = runs.find((run) => run.startedAt && Math.abs(new Date(run.startedAt).getTime() - new Date(startTime).getTime()) < 60_000);
    const kind = matchedRun?.kind ?? "auto";
    return {
      id: `artifact-${version.id}`,
      kind,
      title: `${kind === "manual" ? "手动执行" : "自动执行"} · 第 ${versions.length - index} 次`,
      subtitle: new Date(startTime).toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false }),
      startedAt: startTime,
      endedAt: version.finishedAt,
      events: matchedRun?.events ?? [],
      failed: version.status === "failed" || Boolean(version.error),
      completed: version.status === "completed",
    };
  });
  if (artifactRuns.length > runs.length) return artifactRuns;
  return runs.reverse();
}

function detailedEventsForRun(job: BackupJob | null, run: BackupEventRun) {
  if (!job || !run.startedAt) return run.events;
  const events = [...job.events].sort((left, right) => new Date(left.time).getTime() - new Date(right.time).getTime());
  const start = new Date(run.startedAt).getTime();
  const version = (job.artifactVersions ?? []).find((item) => `artifact-${item.id}` === run.id);
  const endSource = version?.finishedAt ?? run.endedAt;
  const end = endSource ? new Date(endSource).getTime() + 60_000 : Number.POSITIVE_INFINITY;
  const items = events.filter((event) => {
    const time = new Date(event.time).getTime();
    return time >= start - 1000 && time <= end;
  });
  return items.length ? items.reverse() : run.events;
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
function ScheduleEditor({
  job,
  connections,
  storages,
  onClose,
  onSave,
}: {
  job: BackupJob;
  connections: DatabaseConnection[];
  storages: RemoteStorage[];
  onClose: () => void;
  onSave: (config: BackupConfig) => Promise<void>;
}) {
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const { recipients, loadingRecipients } = useNotificationRecipients();
  const writableStorages = storages.filter((item) => item.type === "object" || item.type === "webdav" || item.type === "local");
  const initialSource = connections.find((item) => item.name === job.sourceName && item.type === job.sourceType) ?? connections.find((item) => item.name === job.sourceName) ?? connections[0];
  const initialTargets = initialBackupTargets(job, writableStorages);
  const [databases, setDatabases] = useState<DatabaseInfo[]>([]);
  const [databaseConnectionId, setDatabaseConnectionId] = useState("");
  const [loadingDatabases, setLoadingDatabases] = useState(false);
  const [form, setForm] = useState<BackupConfig>({
    name: job.name,
    sourceId: initialSource?.id ?? "",
    sourceDatabase: job.sourceDatabase,
    sourceDatabases: job.sourceDatabase.split(",").map((item) => item.trim()).filter(Boolean),
    destinationType: initialTargets.length > 1 ? "multi" : "storage",
    destination: initialTargets.find((target) => target.role === "primary")?.storageId ?? initialTargets[0]?.storageId ?? "",
    primaryDestinationId: initialTargets.find((target) => target.role === "primary")?.storageId ?? initialTargets[0]?.storageId ?? "",
    backupTargets: initialTargets,
    scheduleMode: "scheduled",
    scheduleFrequency: job.scheduleFrequency ?? "daily",
    scheduleTime: job.scheduleTime ?? "02:00:00",
    scheduleWeekday: job.scheduleWeekday ?? 1,
    scheduleMonthDay: job.scheduleMonthDay ?? 1,
    retentionDays: retentionDaysFromJob(job),
    notificationEnabled: job.notificationEnabled ?? false,
    notificationRecipientIds: job.notificationRecipientIds ?? [],
    notificationTrigger: job.notificationTrigger ?? "all",
  });
  const source = connections.find((item) => item.id === form.sourceId);
  const selectedTargetIds = selectedBackupTargetIds(form, writableStorages);
  const primaryDestinationId = selectedTargetIds.includes(form.primaryDestinationId ?? "") ? form.primaryDestinationId ?? "" : selectedTargetIds[0] ?? "";

  useEffect(() => {
    if (!form.sourceId) return;
    let cancelled = false;
    setLoadingDatabases(true);
    setError("");
    api.databases(form.sourceId)
      .then((items) => {
        if (cancelled) return;
        setDatabases(items);
        setDatabaseConnectionId(form.sourceId);
        setForm((current) => {
          if (current.sourceId !== form.sourceId) return current;
          const valid = new Set(items.map((item) => item.name));
          const kept = current.sourceDatabases.filter((name) => valid.has(name));
          const nextDatabases = kept.length ? kept : (items[0]?.name ? [items[0].name] : []);
          return { ...current, sourceDatabases: nextDatabases, sourceDatabase: nextDatabases.join(", ") };
        });
      })
      .catch((reason) => {
        if (!cancelled) setError(reason instanceof Error ? reason.message : "加载数据库列表失败");
      })
      .finally(() => {
        if (!cancelled) setLoadingDatabases(false);
      });
    return () => { cancelled = true; };
  }, [form.sourceId]);

  async function submit(event: FormEvent) {
    event.preventDefault();
    const sourceDatabases = form.sourceDatabases.filter(Boolean);
    if (!sourceDatabases.length || databaseConnectionId !== form.sourceId) {
      setError("请等待数据库列表加载完成，并选择要备份的数据库");
      return;
    }
    if (!primaryDestinationId) {
      setError("请至少选择一个备份目标");
      return;
    }
    if (form.notificationEnabled && !(form.notificationRecipientIds?.length)) {
      setError("请至少选择一个通知接收者，或关闭备份通知");
      return;
    }
    setSaving(true);
    setError("");
    try {
      const targets = buildBackupTargets(selectedTargetIds, primaryDestinationId);
      await onSave({
        ...form,
        sourceDatabase: sourceDatabases.join(", "),
        sourceDatabases,
        destinationType: targets.length > 1 ? "multi" : "storage",
        destination: primaryDestinationId,
        primaryDestinationId,
        backupTargets: targets,
      });
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "保存计划失败");
      setSaving(false);
    }
  }

  return (
    <div className="modal-backdrop">
      <form className="modal schedule-editor-modal" onSubmit={submit}>
        <div className="modal-title"><div><h3>编辑备份任务</h3><p>{job.sourceName} / {job.sourceDatabase}</p></div></div>
        {error && <div className="error-banner">{error}</div>}
        <div className="schedule-editor-section">
          <label className="field"><span>任务名称</span><input value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} /></label>
          <div className="schedule-editor-source-grid">
            <SelectField label="源端连接" value={form.sourceId} onChange={(sourceId) => setForm({ ...form, sourceId, sourceDatabase: "", sourceDatabases: [] })}>
              {connections.map((item) => <option key={item.id} value={item.id}>{item.name} · {item.type}</option>)}
            </SelectField>
            <DatabaseMultiSelect type={source?.type} databases={databases} loading={loadingDatabases} selected={form.sourceDatabases} onChange={(sourceDatabases) => setForm({ ...form, sourceDatabases, sourceDatabase: sourceDatabases.join(", ") })} />
          </div>
        </div>
        <div className="schedule-editor-section">
          <div className="section-title compact"><div><h3>备份目标</h3><p>选择一个或多个远端存储，并指定恢复和下载优先使用的主节点。</p></div></div>
          <BackupTargetSelector
            storages={writableStorages}
            selectedIds={selectedTargetIds}
            primaryId={primaryDestinationId}
            onChange={(nextIds, nextPrimaryId) => {
              const targets = buildBackupTargets(nextIds, nextPrimaryId);
              setForm({ ...form, destination: nextPrimaryId, primaryDestinationId: nextPrimaryId, backupTargets: targets, destinationType: targets.length > 1 ? "multi" : "storage" });
            }}
          />
        </div>
        <div className="schedule-editor-section">
          <div className="section-title compact"><div><h3>执行计划</h3><p>调整定时频率、执行时间和备份保留期限。</p></div></div>
        <div className="schedule-grid compact">
          <SelectField label="频率" value={form.scheduleFrequency ?? "daily"} onChange={(scheduleFrequency) => setForm({ ...form, scheduleFrequency: scheduleFrequency as BackupConfig["scheduleFrequency"] })}><option value="daily">每天</option><option value="weekly">每周</option><option value="monthly">每月</option></SelectField>
          <TimeSelect value={form.scheduleTime ?? "02:00:00"} onChange={(scheduleTime) => setForm({ ...form, scheduleTime })} />
          {form.scheduleFrequency === "weekly" && <SelectField label="每周" value={form.scheduleWeekday ?? 1} onChange={(scheduleWeekday) => setForm({ ...form, scheduleWeekday: Number(scheduleWeekday) })}>{weekdays.map((item, index) => <option key={item} value={index}>{item}</option>)}</SelectField>}
          {form.scheduleFrequency === "monthly" && <SelectField label="每月" value={form.scheduleMonthDay ?? 1} onChange={(scheduleMonthDay) => setForm({ ...form, scheduleMonthDay: Number(scheduleMonthDay) })}>{Array.from({ length: 28 }, (_, index) => index + 1).map((day) => <option key={day} value={day}>{day} 日</option>)}</SelectField>}
          <RetentionDaysField value={form.retentionDays ?? 30} onChange={(retentionDays) => setForm({ ...form, retentionDays })} />
        </div>
        </div>
        <div className="notification-panel modal-notification-panel">
          <button type="button" className="toggle-row" onClick={() => setForm({ ...form, notificationEnabled: !form.notificationEnabled, notificationTrigger: form.notificationTrigger ?? "all" })}><span className={`switch ${form.notificationEnabled ? "on" : ""}`}><i /></span><div><strong>备份通知</strong><small>选择定时任务结束后的通知发送范围</small></div></button>
          {form.notificationEnabled && (
            <>
              <NotificationTriggerSelector value={form.notificationTrigger ?? "all"} onChange={(notificationTrigger) => setForm({ ...form, notificationTrigger })} />
              <div className="notification-recipient-field"><span>通知接收者</span><NotificationRecipientPicker recipients={recipients} loading={loadingRecipients} selected={form.notificationRecipientIds ?? []} onChange={(notificationRecipientIds) => setForm({ ...form, notificationRecipientIds })} /></div>
            </>
          )}
        </div>
        <div className="modal-actions"><Button type="button" variant="secondary" onClick={onClose}>取消</Button><Button disabled={saving} type="submit">{saving ? "保存中..." : "保存计划"}</Button></div>
      </form>
    </div>
  );
}

function initialBackupTargets(job: BackupJob, storages: RemoteStorage[]) {
  const fallbackStorage = storages.find((storage) => {
    const destination = job.destination || "";
    return destination.includes(storage.id) || destination.includes(storage.name) || (storage.bucket && destination.includes(storage.bucket)) || (storage.path && destination.includes(storage.path));
  });
  const normalized = normalizeTargetSelection({
    name: job.name,
    sourceId: "",
    sourceDatabase: job.sourceDatabase,
    sourceDatabases: job.sourceDatabase.split(",").map((item) => item.trim()).filter(Boolean),
    destinationType: job.destinationType === "multi" ? "multi" : "storage",
    destination: job.primaryDestinationId ?? fallbackStorage?.id ?? "",
    primaryDestinationId: job.primaryDestinationId,
    backupTargets: job.backupTargets?.length ? job.backupTargets : (fallbackStorage ? [{ storageId: fallbackStorage.id, role: "primary", order: 0 }] : undefined),
    scheduleMode: "scheduled",
  }, storages);
  return normalized.backupTargets ?? [];
}

function scheduleLabel(job: BackupJob) {
  if (job.scheduleFrequency === "weekly") return `每周${weekdays[job.scheduleWeekday ?? 1]} ${job.scheduleTime}`;
  if (job.scheduleFrequency === "monthly") return `每月 ${job.scheduleMonthDay ?? 1} 日 ${job.scheduleTime}`;
  return `每天 ${job.scheduleTime ?? "02:00:00"}`;
}

function retentionDaysFromJob(job: BackupJob) {
  return job.retentionDays ?? ((job.retentionMonths ?? 1) * 30);
}

function retentionLabel(days: number) {
  return `${days} 天`;
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
