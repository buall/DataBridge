import {
  AlertTriangle,
  Archive,
  Check,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  DatabaseBackup,
  Download,
  PauseCircle,
  Pencil,
  Play,
  PlayCircle,
  Square,
  X,
} from "lucide-react";
import { type FormEvent, useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { api } from "../api";
import { NotificationRecipientPicker, useNotificationRecipients } from "../components/NotificationRecipientPicker";
import { Badge, Button, Card, ConfirmDialog, DatabaseTypeIcon, RegionFlag, SelectField } from "../components/UI";
import { migrationStatusLabel, migrationStatusTone } from "../status";
import type { BackupConfig, BackupJob, DatabaseConnection, DatabaseInfo, NotificationRecipient, RemoteStorage } from "../types";
import {
  BackupTargetSelector,
  buildBackupTargets,
  DatabaseMultiSelect,
  normalizeTargetSelection,
  NotificationTriggerSelector,
  notificationTriggerLabel,
  RetentionDaysField,
  selectedBackupTargetIds,
  TimeSelect,
} from "./BackupWizard";

type BackupArtifactItem = { database: string; name: string; size?: number; path?: string };
export type BackupArtifactVersionOption = {
  id: string;
  label: string;
  startedAt: string;
  finishedAt?: string;
  status: BackupJob["status"];
  artifacts: BackupArtifactItem[];
  backupMode?: string;
  current?: boolean;
};

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

const weekdays = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];
const etcdClusterDatabase: DatabaseInfo = { name: "整个集群", sizeBytes: 0 };

export function BackupMonitoring({
  jobId,
  connections,
  storages,
  onBack,
  onComplete,
  onOpenRestore,
  onOpenArtifacts,
}: {
  jobId: string;
  connections: DatabaseConnection[];
  storages: RemoteStorage[];
  onBack?: () => void;
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
  const [updatingSchedule, setUpdatingSchedule] = useState(false);
  const [exportingRecords, setExportingRecords] = useState(false);
  const [cancelConfirmOpen, setCancelConfirmOpen] = useState(false);
  const [cancelling, setCancelling] = useState(false);
  const { recipients, loadingRecipients } = useNotificationRecipients();

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
    return () => {
      stopped = true;
    };
  }, [jobId, onComplete]);

  async function cancel() {
    setCancelling(true);
    setError("");
    try {
      await api.cancelBackup(jobId);
      setCancelConfirmOpen(false);
      pollBackup();
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

  async function toggleSchedulePaused() {
    if (!job) return;
    setUpdatingSchedule(true);
    setError("");
    try {
      const next = job.schedulePaused ? await api.resumeBackupSchedule(jobId) : await api.pauseBackupSchedule(jobId);
      setJob(next);
      onComplete(next);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "更新调度状态失败");
    } finally {
      setUpdatingSchedule(false);
    }
  }

  async function exportRecords() {
    setExportingRecords(true);
    setError("");
    try {
      const { blob, fileName } = await api.backupRecords(jobId);
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = fileName;
      link.click();
      URL.revokeObjectURL(url);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "导出备份记录失败");
    } finally {
      setExportingRecords(false);
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

  if (!job) {
    return (
      <div className="empty">
        <div className="e-t">任务准备中</div>
        <div className="e-s">{error || "正在准备备份任务..."}</div>
      </div>
    );
  }

  const scheduled = job.scheduleMode === "scheduled" || job.status === "scheduled";
  const running = job.status === "running" || job.status === "pending";
  const paused = Boolean(job.schedulePaused && !running);
  const latestRunStatus = job.artifactVersions?.[0]?.status;
  const cancelled = !running && (job.status === "cancelled" || latestRunStatus === "cancelled");
  const failed = !running && (job.status === "failed" || latestRunStatus === "failed");
  const stageItems = job.destinationType === "restore" ? restoreStages : backupStages;
  const currentStage = stageIndex(job.stage, stageItems);
  const eventRuns = groupBackupEvents(job);
  const lastError = latestErrorEvent(job);
  const hasExportableRecords = Boolean(
    job.artifactVersions?.length ||
      job.events.some((event) => event.message.includes("定时备份已触发") || event.message.includes("手动触发定时备份")),
  );
  const targets = scheduleTargets(job, storages);
  const notification = scheduleNotification(job, recipients, loadingRecipients);

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
      <div className="h-row">
        {onBack ? (
          <Button variant="secondary" className="sm" onClick={onBack}>
            <ChevronLeft size={14} /> 返回列表
          </Button>
        ) : null}
        <span className="tag mono-t">{job.id}</span>
        <Badge tone={paused ? "gray" : migrationStatusTone(job.status)}>
          <span className="pd" />
          {paused ? "已暂停" : migrationStatusLabel(job.status)}
        </Badge>
        <span className="sp" />
        <Button variant="secondary" onClick={() => onOpenArtifacts(job)}>
          <Archive size={15} />
          备份产物
        </Button>
        {canShowRestorePanel(job) ? (
          <Button variant="secondary" onClick={() => onOpenRestore(job)}>
            <DatabaseBackup size={15} />
            还原数据
          </Button>
        ) : null}
        {scheduled ? (
          <>
            <Button variant="secondary" disabled={triggering || running} onClick={triggerNow}>
              <Play size={15} />
              {triggering ? "触发中..." : "立即触发"}
            </Button>
            <Button variant="secondary" disabled={updatingSchedule} onClick={toggleSchedulePaused}>
              {job.schedulePaused ? <PlayCircle size={15} /> : <PauseCircle size={15} />}
              {updatingSchedule ? "处理中..." : job.schedulePaused ? "恢复调度" : "暂停调度"}
            </Button>
            <Button variant="secondary" disabled={running} onClick={() => setEditing(true)}>
              <Pencil size={15} />
              编辑计划
            </Button>
          </>
        ) : null}
        {running ? (
          <Button variant="danger" disabled={cancelling} onClick={() => setCancelConfirmOpen(true)}>
            <Square size={14} />
            停止任务
          </Button>
        ) : null}
      </div>

      <div>
        <h1 className="page-h">{job.name}</h1>
        <div className="sec-sub">
          {job.sourceName} / {sourceDatabaseLabel(job)} · 目标 {job.destination}
        </div>
      </div>

      {error ? <div className="error-banner">{error}</div> : null}
      {failed || cancelled ? (
        <div className="attn">
          <AlertTriangle size={17} style={{ color: "var(--danger)", flex: "none", marginTop: 2 }} />
          <div>
            <div className="a-t">{cancelled ? "本次执行已取消" : job.error || "备份任务失败"}</div>
            {!cancelled && lastError ? <div className="a-s">{lastError}</div> : null}
          </div>
        </div>
      ) : null}

      <Card className="pad">
        <div className="h-row">
          <span style={{ fontWeight: 600 }}>
            {scheduled ? "调度概览" : "总体进度"} · {stageLabel(job.stage)}
          </span>
          <span className="sp" />
          <span className="num" style={{ fontWeight: 700, fontSize: 16 }}>
            {job.progress}%
          </span>
        </div>
        <div className="progress" style={{ margin: "10px 0 12px" }}>
          <i className={running ? "shimmer" : undefined} style={{ width: `${job.progress}%` }} />
        </div>
        <div className="chiprow">
          <span className="tag">{scheduled ? scheduleLabel(job) : "一次性任务"}</span>
          <span className="tag">保留 {retentionDaysFromJob(job)} 天</span>
          {scheduled ? (
            <span className="tag">下次执行：{job.schedulePaused ? "已暂停" : formatDateTime(job.nextRunAt)}</span>
          ) : null}
          <span className="tag">
            产物：{job.artifactName || "待生成"}
            {job.artifactSize ? `（${formatBytes(job.artifactSize)}）` : ""}
          </span>
        </div>
      </Card>

      <div className="ov-grid">
        <Card className="pad">
          <h3 className="sec-title" style={{ marginBottom: 10 }}>
            {scheduled ? "任务配置" : "执行信息"}
          </h3>
          <div className="kv">
            <span className="k">源端 / 数据库</span>
            <span className="v">
              {job.sourceName} · {sourceDatabaseLabel(job)}
            </span>
          </div>
          <div className="kv">
            <span className="k">备份目标</span>
            <span className="v">{targets.map((t) => `${t.name}（${t.detail}）`).join(" · ")}</span>
          </div>
          <div className="kv">
            <span className="k">保留策略</span>
            <span className="v">{retentionDaysFromJob(job)} 天</span>
          </div>
          <div className="kv">
            <span className="k">备份通知</span>
            <span className="v">
              {notification.value} · {notification.detail}
            </span>
          </div>
          {job.backupMode === "mongo_oplog" ? (
            <div className="kv">
              <span className="k">全量基线周期</span>
              <span className="v">每 {job.fullBackupIntervalDays ?? 7} 天</span>
            </div>
          ) : null}
        </Card>

        <Card className="pad">
          <h3 className="sec-title" style={{ marginBottom: 10 }}>
            执行阶段
          </h3>
          <div style={{ display: "flex", flexDirection: "column", gap: 9 }}>
            {stageItems.map(([id, label], index) => {
              const done = index < currentStage || job.status === "completed";
              const active = index === currentStage && !failed && !cancelled;
              return (
                <div key={id} style={{ display: "flex", alignItems: "center", gap: 10, fontSize: 13.5 }}>
                  <span
                    style={{
                      width: 22,
                      height: 22,
                      borderRadius: "50%",
                      display: "grid",
                      placeItems: "center",
                      fontSize: 11,
                      fontWeight: 600,
                      background: done ? "var(--ok-soft)" : active ? "var(--indigo)" : "transparent",
                      color: done ? "var(--ok)" : active ? "#fff" : "var(--faint)",
                      border: done || active ? "none" : "1.5px solid var(--hair)",
                    }}
                  >
                    {done ? <Check size={12} /> : index + 1}
                  </span>
                  <span style={{ fontWeight: active ? 650 : 500, color: done || active ? "var(--ink)" : "var(--muted)" }}>
                    {label}
                  </span>
                  <span className="sp" />
                  <span style={{ fontSize: 12, color: "var(--muted)" }}>
                    {done ? "完成" : active ? "进行中" : failed || cancelled ? "停止" : "等待中"}
                  </span>
                </div>
              );
            })}
          </div>
        </Card>
      </div>

      <Card style={{ overflow: "hidden" }}>
        <div className="h-row" style={{ padding: "14px 18px", borderBottom: "1px solid var(--hair-2)" }}>
          <div>
            <h3 className="sec-title">{scheduled ? "定时任务执行记录" : "实时事件"}</h3>
            <div className="sec-sub">{scheduled ? "按每次触发的具体任务查看事件。" : "查看备份过程中的关键日志。"}</div>
          </div>
          <span className="sp" />
          {scheduled ? (
            <Button variant="secondary" className="sm" disabled={!hasExportableRecords || exportingRecords} onClick={exportRecords}>
              <Download size={14} />
              {exportingRecords ? "导出中..." : "导出备份记录"}
            </Button>
          ) : null}
        </div>

        <div>
          {eventRuns.map((run) => {
            const collapsed = collapsedRuns[run.id] ?? scheduled;
            const detailEvents = !collapsed ? detailedEventsForRun(detailedJob, run) : run.events;
            const loadingThisRun = loadingRunId === run.id;
            const runKindLabel = run.kind === "manual" ? "手动执行" : run.kind === "auto" ? "自动执行" : "";
            const runTitle = runKindLabel ? run.title.replace(`${runKindLabel} · `, "") : run.title;

            return (
              <div className="run-group" key={run.id}>
                <button type="button" className="run-group-head" onClick={() => void toggleRun(run, collapsed)}>
                  {collapsed ? <ChevronRight size={15} /> : <ChevronDown size={15} />}
                  {runKindLabel ? (
                    <Badge tone={run.kind === "manual" ? "indigo" : "gray"}>{runKindLabel}</Badge>
                  ) : null}
                  <strong style={{ fontSize: 13.5 }}>{runTitle}</strong>
                  <span style={{ fontSize: 12, color: "var(--muted)" }}>{run.subtitle}</span>
                  <span className="sp" />
                  <Badge tone={run.cancelled ? "gray" : run.failed ? "red" : run.completed ? "green" : "blue"}>
                    {run.cancelled ? "已取消" : run.failed ? "失败" : run.completed ? "完成" : "执行中"}
                  </Badge>
                </button>
                {!collapsed ? (
                  <div className="run-group-body">
                    {loadingThisRun ? (
                      <div style={{ padding: "8px 0", fontSize: 12.5, color: "var(--muted)" }}>正在加载本次备份日志...</div>
                    ) : (
                      detailEvents.map((event, index) => (
                        <div className="evt" key={`${run.id}-${event.time}-${index}`}>
                          <span className="et">{new Date(event.time).toLocaleTimeString("zh-CN", { hour12: false })}</span>
                          <span className={`ed2 ed-${event.level}`} />
                          <span className="em">{event.message}</span>
                        </div>
                      ))
                    )}
                  </div>
                ) : null}
              </div>
            );
          })}
        </div>
      </Card>

      {editing ? <ScheduleEditor job={job} connections={connections} storages={storages} onClose={() => setEditing(false)} onSave={saveSchedule} /> : null}
      {cancelConfirmOpen ? (
        <ConfirmDialog
          title="停止备份任务"
          description={scheduled ? "停止本次执行，后续定时调度保持原设置。" : "停止当前任务，未完成的备份不会继续执行。"}
          tone="warning"
          confirmText="确认停止"
          submitting={cancelling}
          onCancel={() => setCancelConfirmOpen(false)}
          onConfirm={cancel}
          details={[`任务：${job.name}`, "已生成的历史备份产物不会被自动删除。"]}
        />
      ) : null}
    </div>
  );
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
  return job.destinationType === "local" || job.destinationType === "object" || job.destinationType === "webdav" || job.destinationType === "one_drive" || job.destinationType === "multi";
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
  return [{ database: job.sourceDatabase || "default", name: job.artifactName, size: job.artifactSize, path: job.artifactPath }];
}

export function artifactVersionsForRestore(job: BackupJob): BackupArtifactVersionOption[] {
  const versions = (job.artifactVersions ?? [])
    .filter((version) => version.artifacts?.some((artifact) => artifact.name || artifact.copies?.some((copy) => copy.name)))
    .map((version): BackupArtifactVersionOption => ({
      id: version.id,
      label: `${version.backupMode === "incremental" ? "增量" : version.backupMode === "full" ? "全量" : "备份"} · ${formatDateTime(version.finishedAt ?? version.startedAt)}`,
      startedAt: version.startedAt,
      finishedAt: version.finishedAt,
      status: version.status,
      artifacts: (version.artifacts ?? [])
        .map((artifact) => {
          const primaryCopy = artifact.copies?.find((copy) => copy.role === "primary" && copy.name) ?? artifact.copies?.find((copy) => copy.name);
          return {
            database: artifact.database || job.sourceDatabase || "default",
            name: artifact.name || primaryCopy?.name || "",
            size: artifact.size || primaryCopy?.size,
            path: artifact.path || primaryCopy?.path,
          };
        })
        .filter((artifact) => artifact.name),
      backupMode: version.backupMode,
      current: false,
    }))
    .sort((left, right) => new Date(right.finishedAt ?? right.startedAt).getTime() - new Date(left.finishedAt ?? left.startedAt).getTime());
  if (versions.length) return versions;
  if (!canRestoreFromJob(job)) return [];
  return [
    {
      id: "current",
      label: formatDateTime(job.finishedAt ?? job.lastRunAt ?? job.startedAt),
      startedAt: job.startedAt,
      finishedAt: job.finishedAt ?? job.lastRunAt,
      status: "completed",
      artifacts: backupArtifacts(job),
      current: true,
    },
  ];
}

export function totalArtifactSize(artifacts: BackupArtifactItem[]) {
  return artifacts.reduce((sum, artifact) => sum + (artifact.size ?? 0), 0);
}

export function storageTypeLabel(value: string) {
  if (value === "local") return "本地目录";
  if (value === "object") return "对象存储";
  if (value === "webdav") return "WebDAV";
  if (value === "one_drive") return "OneDrive";
  if (value === "restore") return "恢复任务";
  return value || "备份目标";
}

export function RestoreConnectionSelect({
  connectionId,
  connections,
  onChange,
}: {
  connectionId: string;
  connections: DatabaseConnection[];
  onChange: (value: string) => void;
}) {
  const connection = connections.find((item) => item.id === connectionId) ?? connections[0];
  const [open, setOpen] = useState(false);
  const triggerRef = useRef<HTMLButtonElement | null>(null);
  const menuRef = useRef<HTMLDivElement | null>(null);
  const [pos, setPos] = useState<{ left: number; top: number; minWidth: number }>({ left: 0, top: 0, minWidth: 220 });

  const updatePos = useCallback(() => {
    const tr = triggerRef.current;
    if (!tr) return;
    const r = tr.getBoundingClientRect();
    setPos({ left: r.left, top: r.bottom + 6, minWidth: r.width });
  }, []);

  useLayoutEffect(() => {
    if (open) updatePos();
  }, [open, updatePos]);

  useEffect(() => {
    if (!open) return;
    const close = (event: MouseEvent) => {
      const target = event.target as Node;
      if (triggerRef.current?.contains(target) || menuRef.current?.contains(target)) return;
      setOpen(false);
    };
    window.addEventListener("mousedown", close);
    return () => window.removeEventListener("mousedown", close);
  }, [open]);

  useEffect(() => {
    if (connectionId) return;
    if (connections[0]) onChange(connections[0].id);
  }, [connectionId, connections, onChange]);

  return (
    <div className="field">
      <span>目标连接</span>
      <button
        ref={triggerRef}
        type="button"
        className="csel-trigger"
        aria-haspopup="listbox"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
      >
        <span className="csel-val">
          {connection ? <DatabaseTypeIcon type={connection.type} /> : null}
          <span className="csel-txt">{connection ? `${connection.name} · ${connection.host}` : "请选择连接"}</span>
          {connection?.region && connection.region !== "内网" ? <RegionFlag region={connection.region} /> : null}
        </span>
        <span className="csel-chev">
          <ChevronDown size={14} />
        </span>
      </button>
      {open
        ? createPortal(
            <div ref={menuRef} className="csel-list" role="listbox" style={{ left: pos.left, top: pos.top, minWidth: pos.minWidth }}>
              {connections.map((item) => (
                <button
                  type="button"
                  key={item.id}
                  className="csel-opt"
                  aria-selected={item.id === connectionId}
                  onClick={() => {
                    onChange(item.id);
                    setOpen(false);
                  }}
                >
                  <DatabaseTypeIcon type={item.type} />
                  <span className="csel-txt">
                    {item.name} · {item.host}
                  </span>
                </button>
              ))}
            </div>,
            document.body,
          )
        : null}
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
  cancelled: boolean;
  completed: boolean;
};

function groupBackupEvents(job: BackupJob): BackupEventRun[] {
  const events = [...job.events].sort((left, right) => new Date(left.time).getTime() - new Date(right.time).getTime());
  const runs: BackupEventRun[] = [];
  const scheduled = job.scheduleMode === "scheduled";
  let current: (typeof runs)[number] | null = null;
  const planEvents: BackupJob["events"] = [];

  if (!scheduled) {
    return [
      {
        id: "single-run",
        title: "本次备份",
        subtitle: events[0]
          ? new Date(events[0].time).toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false })
          : "等待事件",
        events: events.reverse(),
        failed: events.some((event) => event.level === "ERROR"),
        cancelled: job.status === "cancelled",
        completed: events.some((event) => event.message.includes("备份任务完成")),
      },
    ];
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
        cancelled: false,
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
    if (event.message.includes("任务已取消")) current.cancelled = true;
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
      cancelled: false,
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
      failed: version.status !== "cancelled" && (version.status === "failed" || Boolean(version.error)),
      cancelled: version.status === "cancelled",
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

function sourceDatabaseLabel(job: BackupJob) {
  return job.sourceDatabase.split(",").map((item) => item.trim()).filter(Boolean).join("、") || "未选择数据库";
}

function scheduleTargets(job: BackupJob, storages: RemoteStorage[]) {
  const targets = job.backupTargets?.length
    ? [...job.backupTargets].sort((left, right) => (left.order ?? 0) - (right.order ?? 0))
    : initialBackupTargets(job, storages);
  if (!targets.length) {
    return [{ id: job.destination || "unknown", name: job.destination || "未配置目标存储", detail: storageTypeLabel(job.destinationType) }];
  }
  return targets.map((target, index) => {
    const storage = storages.find((item) => item.id === target.storageId);
    const role = target.role === "secondary" ? "备份节点" : index === 0 ? "主节点" : "备份节点";
    const storageName = storage?.name ?? target.storageId;
    const storageMeta = storage ? storageLocation(storage) : "未找到存储配置";
    return { id: target.storageId, name: storageName, detail: `${role} · ${storageTypeLabel(storage?.type ?? job.destinationType)} · ${storageMeta}` };
  });
}

function storageLocation(storage: RemoteStorage) {
  return storage.bucket || storage.path || storage.endpoint || storage.provider || "未填写位置";
}

function scheduleNotification(job: BackupJob, recipients: NotificationRecipient[], loadingRecipients: boolean) {
  if (!job.notificationEnabled) return { value: "未开启", detail: "不会发送备份结果通知" };
  const trigger = notificationTriggerLabel(job.notificationTrigger ?? "all");
  if (loadingRecipients) return { value: trigger, detail: "正在加载通知接收者" };
  const names = (job.notificationRecipientIds ?? []).map((id) => recipients.find((item) => item.id === id)?.name ?? id).filter(Boolean);
  if (names.length) return { value: trigger, detail: names.join("、") };
  if (job.notificationTarget) return { value: trigger, detail: job.notificationTarget };
  return { value: trigger, detail: "未选择接收者" };
}

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
  const writableStorages = storages.filter(
    (item) => item.type === "object" || item.type === "webdav" || item.type === "local" || (item.type === "one_drive" && item.status === "connected"),
  );
  const initialSource =
    connections.find((item) => item.name === job.sourceName && item.type === job.sourceType) ??
    connections.find((item) => item.name === job.sourceName) ??
    connections[0];
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
    backupMode: (job.backupMode as BackupConfig["backupMode"]) ?? "full",
    fullBackupIntervalDays: job.fullBackupIntervalDays ?? 7,
    restoreDrillEnabled: job.restoreDrillEnabled ?? false,
    restoreDrillTargetId: job.restoreDrillTargetId ?? "",
    retentionDays: retentionDaysFromJob(job),
    notificationEnabled: job.notificationEnabled ?? false,
    notificationRecipientIds: job.notificationRecipientIds ?? [],
    notificationTrigger: job.notificationTrigger ?? "all",
  });

  const source = connections.find((item) => item.id === form.sourceId);
  const mongoRestoreDrillTargets = connections.filter((item) => item.type === "MongoDB" && item.id !== form.sourceId);
  const selectedTargetIds = selectedBackupTargetIds(form, writableStorages);
  const primaryDestinationId = selectedTargetIds.includes(form.primaryDestinationId ?? "")
    ? (form.primaryDestinationId ?? "")
    : (selectedTargetIds[0] ?? "");

  useEffect(() => {
    if (!form.sourceId) return;
    const selectedSource = connections.find((item) => item.id === form.sourceId);
    if (selectedSource?.type === "etcd") {
      setDatabases([etcdClusterDatabase]);
      setDatabaseConnectionId(form.sourceId);
      setLoadingDatabases(false);
      setError("");
      setForm((current) =>
        current.sourceId === form.sourceId
          ? { ...current, sourceDatabase: etcdClusterDatabase.name, sourceDatabases: [etcdClusterDatabase.name] }
          : current,
      );
      return;
    }
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
          const nextDatabases = kept.length ? kept : items[0]?.name ? [items[0].name] : [];
          return { ...current, sourceDatabases: nextDatabases, sourceDatabase: nextDatabases.join(", ") };
        });
      })
      .catch((reason) => {
        if (!cancelled) setError(reason instanceof Error ? reason.message : "加载数据库列表失败");
      })
      .finally(() => {
        if (!cancelled) setLoadingDatabases(false);
      });
    return () => {
      cancelled = true;
    };
  }, [connections, form.sourceId]);

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
    if (form.notificationEnabled && !form.notificationRecipientIds?.length) {
      setError("请至少选择一个通知接收者，或关闭备份通知");
      return;
    }
    if (form.backupMode === "mongo_oplog" && form.restoreDrillEnabled && !form.restoreDrillTargetId) {
      setError("请为全量后恢复演练选择独立的目标副本集");
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

  return createPortal(
    <div className="modal-backdrop" onMouseDown={onClose}>
      <form className="modal wide" role="dialog" aria-modal="true" onSubmit={submit} onMouseDown={(e) => e.stopPropagation()}>
        <div className="modal-h">
          <div style={{ flex: 1 }}>
            <h3>编辑备份任务</h3>
            <div className="ms">
              {job.sourceName} / {job.sourceDatabase}
            </div>
          </div>
          <button type="button" className="icon-btn ghost" aria-label="关闭编辑备份任务" onClick={onClose}>
            <X size={17} />
          </button>
        </div>

        <div className="modal-b">
          {error ? (
            <div className="error-banner" style={{ marginBottom: 12 }}>
              {error}
            </div>
          ) : null}

          <label className="field">
            <span>任务名称</span>
            <input value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} />
          </label>

          {source?.type === "etcd" ? (
            <div className="info-callout" style={{ marginBottom: 14 }}>
              备份范围：整个集群
            </div>
          ) : (
            <DatabaseMultiSelect
              type={source?.type}
              databases={databases}
              loading={loadingDatabases}
              selected={form.sourceDatabases}
              onChange={(sourceDatabases) => setForm({ ...form, sourceDatabases, sourceDatabase: sourceDatabases.join(", ") })}
            />
          )}

          <div className="field">
            <span>备份目标</span>
            <BackupTargetSelector
              storages={writableStorages}
              selectedIds={selectedTargetIds}
              primaryId={primaryDestinationId}
              onChange={(nextIds, nextPrimaryId) => {
                const targets = buildBackupTargets(nextIds, nextPrimaryId);
                setForm({
                  ...form,
                  destination: nextPrimaryId,
                  primaryDestinationId: nextPrimaryId,
                  backupTargets: targets,
                  destinationType: targets.length > 1 ? "multi" : "storage",
                });
              }}
            />
          </div>

          <div className="grid2">
            <SelectField
              label="频率"
              value={form.scheduleFrequency ?? "daily"}
              onChange={(scheduleFrequency) =>
                setForm({ ...form, scheduleFrequency: scheduleFrequency as BackupConfig["scheduleFrequency"] })
              }
            >
              <option value="hourly">每小时</option>
              <option value="daily">每天</option>
              <option value="weekly">每周</option>
              <option value="monthly">每月</option>
            </SelectField>
            <TimeSelect value={form.scheduleTime ?? "02:00:00"} onChange={(scheduleTime) => setForm({ ...form, scheduleTime })} />
            {form.scheduleFrequency === "weekly" ? (
              <SelectField
                label="每周"
                value={form.scheduleWeekday ?? 1}
                onChange={(scheduleWeekday) => setForm({ ...form, scheduleWeekday: Number(scheduleWeekday) })}
              >
                {weekdays.map((item, index) => (
                  <option key={item} value={index}>
                    {item}
                  </option>
                ))}
              </SelectField>
            ) : null}
            {form.scheduleFrequency === "monthly" ? (
              <SelectField
                label="每月"
                value={form.scheduleMonthDay ?? 1}
                onChange={(scheduleMonthDay) => setForm({ ...form, scheduleMonthDay: Number(scheduleMonthDay) })}
              >
                {Array.from({ length: 28 }, (_, index) => index + 1).map((day) => (
                  <option key={day} value={day}>
                    {day} 日
                  </option>
                ))}
              </SelectField>
            ) : null}
            <RetentionDaysField value={form.retentionDays ?? 30} onChange={(retentionDays) => setForm({ ...form, retentionDays })} />
          </div>

          {form.backupMode === "mongo_oplog" ? (
            <div>
              <button
                type="button"
                className="toggle-row"
                onClick={() => setForm({ ...form, restoreDrillEnabled: !form.restoreDrillEnabled })}
              >
                <span className={`switch ${form.restoreDrillEnabled ? "on" : ""}`}>
                  <i />
                </span>
                <div>
                  <strong>全量后恢复演练</strong>
                  <small>仅恢复到独立副本集，目标数据会被覆盖</small>
                </div>
              </button>
              {form.restoreDrillEnabled ? (
                <SelectField
                  label="演练目标副本集"
                  value={form.restoreDrillTargetId ?? ""}
                  onChange={(restoreDrillTargetId) => setForm({ ...form, restoreDrillTargetId })}
                >
                  <option value="">选择独立 MongoDB 连接</option>
                  {mongoRestoreDrillTargets.map((connection) => (
                    <option key={connection.id} value={connection.id}>
                      {connection.name} · {connection.host}:{connection.port}
                    </option>
                  ))}
                </SelectField>
              ) : null}
            </div>
          ) : null}

          <button
            type="button"
            className="toggle-row"
            onClick={() =>
              setForm({
                ...form,
                notificationEnabled: !form.notificationEnabled,
                notificationTrigger: form.notificationTrigger ?? "all",
              })
            }
          >
            <span className={`switch ${form.notificationEnabled ? "on" : ""}`}>
              <i />
            </span>
            <div>
              <strong>备份通知</strong>
              <small>选择定时任务结束后的通知发送范围</small>
            </div>
          </button>

          {form.notificationEnabled ? (
            <div className="card pad" style={{ background: "var(--surface-2)" }}>
              <NotificationTriggerSelector
                value={form.notificationTrigger ?? "all"}
                onChange={(notificationTrigger) => setForm({ ...form, notificationTrigger })}
              />
              <div style={{ marginTop: 12 }}>
                <span style={{ display: "block", fontSize: 13, fontWeight: 550, marginBottom: 6 }}>通知接收者</span>
                <NotificationRecipientPicker
                  recipients={recipients}
                  loading={loadingRecipients}
                  selected={form.notificationRecipientIds ?? []}
                  onChange={(notificationRecipientIds) => setForm({ ...form, notificationRecipientIds })}
                />
              </div>
            </div>
          ) : null}
        </div>

        <div className="modal-f">
          <Button type="button" variant="secondary" onClick={onClose}>
            取消
          </Button>
          <Button disabled={saving} type="submit">
            {saving ? "保存中..." : "保存计划"}
          </Button>
        </div>
      </form>
    </div>,
    document.body,
  );
}

function initialBackupTargets(job: BackupJob, storages: RemoteStorage[]) {
  const fallbackStorage = storages.find((storage) => {
    const destination = job.destination || "";
    return (
      destination.includes(storage.id) ||
      destination.includes(storage.name) ||
      (storage.bucket && destination.includes(storage.bucket)) ||
      (storage.path && destination.includes(storage.path))
    );
  });
  const normalized = normalizeTargetSelection(
    {
      name: job.name,
      sourceId: "",
      sourceDatabase: job.sourceDatabase,
      sourceDatabases: job.sourceDatabase.split(",").map((item) => item.trim()).filter(Boolean),
      destinationType: job.destinationType === "multi" ? "multi" : "storage",
      destination: job.primaryDestinationId ?? fallbackStorage?.id ?? "",
      primaryDestinationId: job.primaryDestinationId,
      backupTargets: job.backupTargets?.length
        ? job.backupTargets
        : fallbackStorage
          ? [{ storageId: fallbackStorage.id, role: "primary", order: 0 }]
          : undefined,
      scheduleMode: "scheduled",
    },
    storages,
  );
  return normalized.backupTargets ?? [];
}

function scheduleLabel(job: BackupJob) {
  if (job.scheduleFrequency === "hourly") return `每小时第 ${(job.scheduleTime ?? "02:00:00").slice(3)}`;
  if (job.scheduleFrequency === "weekly") return `每周${weekdays[job.scheduleWeekday ?? 1]} ${job.scheduleTime}`;
  if (job.scheduleFrequency === "monthly") return `每月 ${job.scheduleMonthDay ?? 1} 日 ${job.scheduleTime}`;
  return `每天 ${job.scheduleTime ?? "02:00:00"}`;
}

function retentionDaysFromJob(job: BackupJob) {
  return job.retentionDays ?? (job.retentionMonths ?? 1) * 30;
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
