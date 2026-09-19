import { Archive, CircleAlert, Download, PauseCircle, Play, PlayCircle, Plus, Search, Star, Trash2 } from "lucide-react";
import { useMemo, useState } from "react";
import { migrationStatusLabel, migrationStatusTone } from "../status";
import type { BackupJob, DatabaseType } from "../types";
import { Badge, Button, Card, ConfirmDialog, DatabaseTypeIcon } from "../components/UI";
import { api } from "../api";

export function BackupList({
  backups,
  onCreate,
  onOpen,
  onDelete,
  onTrigger,
  onSetSchedulePaused,
  onToggleFavorite,
}: {
  backups: BackupJob[];
  onCreate: () => void;
  onOpen: (job: BackupJob) => void;
  onDelete: (job: BackupJob) => Promise<void>;
  onTrigger: (job: BackupJob) => Promise<void>;
  onSetSchedulePaused: (job: BackupJob, paused: boolean) => Promise<void>;
  onToggleFavorite: (job: BackupJob) => Promise<void>;
}) {
  const [query, setQuery] = useState("");
  const [deleteTarget, setDeleteTarget] = useState<BackupJob | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [triggeringId, setTriggeringId] = useState("");
  const [updatingScheduleId, setUpdatingScheduleId] = useState("");
  const [error, setError] = useState("");
  const [favoritingId, setFavoritingId] = useState("");
  async function exportSchedules() {
    try {
      setError("");
      const { blob, fileName } = await api.exportExcel("schedules");
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = fileName;
      link.click();
      URL.revokeObjectURL(url);
    } catch (exportError) {
      setError(exportError instanceof Error ? exportError.message : "导出定时备份任务失败");
    }
  }
  const filtered = useMemo(() => {
    const keyword = query.trim().toLowerCase();
    const matching = !keyword ? backups : backups.filter((item) =>
      `${item.name} ${item.sourceName} ${item.sourceDatabase} ${item.destination} ${item.artifactName ?? ""} ${latestBackupRun(item)?.error ?? ""}`.toLowerCase().includes(keyword),
    );
    return [...matching].sort((left, right) => Number(Boolean(right.favorite)) - Number(Boolean(left.favorite)));
  }, [backups, query]);
  const running = backups.filter((item) => item.status === "running" || item.status === "pending").length;
  const scheduled = backups.filter((item) => item.status === "scheduled" && !item.schedulePaused).length;
  const paused = backups.filter((item) => item.scheduleMode === "scheduled" && item.schedulePaused).length;
  const failedLastRuns = backups.filter(hasFailedLastRun).length;
  async function triggerJob(job: BackupJob) {
    setTriggeringId(job.id);
    setError("");
    try {
      await onTrigger(job);
    } catch (triggerError) {
      setError(triggerError instanceof Error ? triggerError.message : "立即触发备份失败");
    } finally {
      setTriggeringId("");
    }
  }
  async function setSchedulePaused(job: BackupJob) {
    setUpdatingScheduleId(job.id);
    setError("");
    try {
      await onSetSchedulePaused(job, !job.schedulePaused);
    } catch (scheduleError) {
      setError(scheduleError instanceof Error ? scheduleError.message : "更新调度状态失败");
    } finally {
      setUpdatingScheduleId("");
    }
  }
  async function toggleFavorite(job: BackupJob) {
    setFavoritingId(job.id);
    setError("");
    try {
      await onToggleFavorite(job);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "更新收藏失败");
    } finally {
      setFavoritingId("");
    }
  }

  return (
    <div className="page-stack">
      <div className="page-heading">
        <div><h2>备份任务</h2><p>创建数据库备份并同步到本地目录或远端存储。</p></div>
        <div className="page-heading-actions"><Button variant="secondary" onClick={exportSchedules}><Download size={17} />导出定时任务</Button><Button onClick={onCreate}><Plus size={17} />新建备份</Button></div>
      </div>
      <div className="task-summary-grid">
        <TaskSummary title="全部任务" value={backups.length} detail="任务记录已保存" />
        <TaskSummary title="进行中" value={running} detail="可查看实时进度" tone="blue" />
        <TaskSummary title="定时计划" value={scheduled} detail={paused ? `${paused} 个计划已暂停` : "到点自动执行"} tone="orange" />
        <TaskSummary title="上次失败" value={failedLastRuns} detail={failedLastRuns ? "请优先检查失败任务" : "最近任务均执行正常"} tone="red" />
      </div>
      <Card className="task-table-card backup-list-card">
        <div className="section-title compact">
          <div><h3>任务列表</h3><p>备份完成后可查看文件名、大小和目标位置。</p></div>
          <label className="search-box small"><Search size={15} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索任务或数据库" /></label>
        </div>
        {filtered.length ? (
          <div className="backup-list-scroll">
            <div className="task-table backup-list-table">
              <div className="table-row table-head"><span aria-hidden="true" /><span>任务名称</span><span>任务类型</span><span>状态</span><span>上次调度</span><span>下次调度</span><span>源端 / 数据库</span><span>目标存储</span><span>进度</span><span>操作</span></div>
              {filtered.map((job) => {
                const failedLastRun = hasFailedLastRun(job);
                const deleteDisabled = job.status === "running" || job.status === "pending";
                const triggerVisible = job.scheduleMode === "scheduled" || job.status === "scheduled";
                const triggerDisabled = deleteDisabled || triggeringId === job.id;
                const scheduleActionVisible = job.scheduleMode === "scheduled";
                const scheduleActionDisabled = updatingScheduleId === job.id;
                const lastScheduleTime = lastScheduleLabel(job);
                const nextScheduleTime = nextScheduleLabel(job);
                return (
                <button className={`table-row task-row-button ${failedLastRun ? "has-failed-last-run" : ""}`} key={job.id} onClick={() => onOpen(job)}>
                  <div className="task-table-desktop">
                  <span role="button" tabIndex={favoritingId === job.id ? -1 : 0} aria-disabled={favoritingId === job.id} aria-label={job.favorite ? "取消收藏" : "收藏并置顶"} data-tooltip-mode="always" data-tooltip-content={job.favorite ? "取消收藏" : "收藏并置顶"} className={`row-favorite task-favorite-cell ${job.favorite ? "active" : ""} ${favoritingId === job.id ? "disabled" : ""}`} onClick={(event) => { event.stopPropagation(); if (favoritingId !== job.id) void toggleFavorite(job); }} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); event.stopPropagation(); if (favoritingId !== job.id) void toggleFavorite(job); } }}><Star size={15} fill={job.favorite ? "currentColor" : "none"} /></span>
                  <strong className="task-name" data-tooltip-content={job.name}>{job.name}</strong>
                  <TaskTypeTag job={job} />
                  <BackupRunStatus job={job} />
                  <time className="task-time" data-tooltip-content={lastScheduleTime}>{lastScheduleTime}</time>
                  <time className="task-time" data-tooltip-content={nextScheduleTime}>{nextScheduleTime}</time>
                  <SourceRoute job={job} />
                  <span className="task-name task-meta task-destination" data-tooltip-content={job.destination}>{job.destination}</span>
                  <div className={`table-progress ${failedLastRun ? "failed" : ""}`} data-tooltip-content={backupProgressTooltip(job)}><div><i style={{ width: `${job.progress}%` }} /></div><small>{job.progress}%</small></div>
                  <span className="row-actions">
                    {triggerVisible && <span role="button" tabIndex={triggerDisabled ? -1 : 0} aria-disabled={triggerDisabled} data-tooltip-content={triggerDisabled ? "任务执行中，请稍后触发" : "立即触发一次备份"} className={`row-trigger ${triggerDisabled ? "disabled" : ""}`} onClick={(event) => { event.stopPropagation(); if (!triggerDisabled) void triggerJob(job); }} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); event.stopPropagation(); if (!triggerDisabled) void triggerJob(job); } }}><Play size={14} />{triggeringId === job.id ? "触发中" : "立即触发"}</span>}
                    {scheduleActionVisible && <span role="button" tabIndex={scheduleActionDisabled ? -1 : 0} aria-disabled={scheduleActionDisabled} data-tooltip-content={job.schedulePaused ? "恢复按计划自动执行" : "暂停后将不再自动执行"} className={`row-schedule ${scheduleActionDisabled ? "disabled" : ""}`} onClick={(event) => { event.stopPropagation(); if (!scheduleActionDisabled) void setSchedulePaused(job); }} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); event.stopPropagation(); if (!scheduleActionDisabled) void setSchedulePaused(job); } }}>{job.schedulePaused ? <PlayCircle size={14} /> : <PauseCircle size={14} />}{scheduleActionDisabled ? "处理中" : job.schedulePaused ? "恢复调度" : "暂停调度"}</span>}
                    <span role="button" tabIndex={deleteDisabled ? -1 : 0} aria-disabled={deleteDisabled} data-tooltip-content={deleteDisabled ? "任务执行中，请先停止" : "删除任务"} className={`row-delete ${deleteDisabled ? "disabled" : ""}`} onClick={(event) => { event.stopPropagation(); if (!deleteDisabled) setDeleteTarget(job); }} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); event.stopPropagation(); if (!deleteDisabled) setDeleteTarget(job); } }}><Trash2 size={14} />删除</span>
                  </span>
                  </div>
                  <div className="task-mobile-card">
                  <div className="task-mobile-head">
                    <span className="task-mobile-icon"><Archive size={18} /></span>
                    <div className="task-mobile-copy">
                      <strong data-tooltip-content={job.name}>{job.name}</strong>
                      <span className="task-mobile-source" data-tooltip-content={job.sourceName}><SourceTypeIcon type={job.sourceType} />{job.sourceName}</span>
                      <small data-tooltip-content={`${job.sourceType} · ${job.sourceDatabase}`}>{job.sourceType} · {job.sourceDatabase}</small>
                    </div>
                  </div>
                  <div className="task-mobile-details">
                    <div className="task-mobile-row">
                      <span>任务类型</span>
                      <TaskTypeTag job={job} />
                    </div>
                    <div className="task-mobile-row">
                      <span>状态</span>
                      <BackupRunStatus job={job} />
                    </div>
                    <div className="task-mobile-row">
                      <span>上次调度</span>
                      <strong data-tooltip-content={lastScheduleTime}>{lastScheduleTime}</strong>
                    </div>
                    <div className="task-mobile-row">
                      <span>下次调度</span>
                      <strong data-tooltip-content={nextScheduleTime}>{nextScheduleTime}</strong>
                    </div>
                    <div className="task-mobile-row">
                      <span>目标存储</span>
                      <strong data-tooltip-content={job.destination}>{job.destination}</strong>
                    </div>
                    <div className="task-mobile-row">
                      <span>进度</span>
                      <strong>{job.progress}%</strong>
                    </div>
                  </div>
                  <div className="task-mobile-progress">
                    <i style={{ width: `${job.progress}%` }} />
                  </div>
                  <div className="task-mobile-foot">
                    <span role="button" tabIndex={favoritingId === job.id ? -1 : 0} aria-disabled={favoritingId === job.id} aria-label={job.favorite ? "取消收藏" : "收藏并置顶"} data-tooltip-mode="always" data-tooltip-content={job.favorite ? "取消收藏" : "收藏并置顶"} className={`row-favorite ${job.favorite ? "active" : ""} ${favoritingId === job.id ? "disabled" : ""}`} onClick={(event) => { event.stopPropagation(); if (favoritingId !== job.id) void toggleFavorite(job); }} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); event.stopPropagation(); if (favoritingId !== job.id) void toggleFavorite(job); } }}><Star size={15} fill={job.favorite ? "currentColor" : "none"} /></span>
                    <span className="row-actions">
                      {triggerVisible && <span role="button" tabIndex={triggerDisabled ? -1 : 0} aria-disabled={triggerDisabled} data-tooltip-content={triggerDisabled ? "任务执行中，请稍后触发" : "立即触发一次备份"} className={`row-trigger ${triggerDisabled ? "disabled" : ""}`} onClick={(event) => { event.stopPropagation(); if (!triggerDisabled) void triggerJob(job); }} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); event.stopPropagation(); if (!triggerDisabled) void triggerJob(job); } }}><Play size={14} />{triggeringId === job.id ? "触发中" : "立即触发"}</span>}
                      {scheduleActionVisible && <span role="button" tabIndex={scheduleActionDisabled ? -1 : 0} aria-disabled={scheduleActionDisabled} data-tooltip-content={job.schedulePaused ? "恢复按计划自动执行" : "暂停后将不再自动执行"} className={`row-schedule ${scheduleActionDisabled ? "disabled" : ""}`} onClick={(event) => { event.stopPropagation(); if (!scheduleActionDisabled) void setSchedulePaused(job); }} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); event.stopPropagation(); if (!scheduleActionDisabled) void setSchedulePaused(job); } }}>{job.schedulePaused ? <PlayCircle size={14} /> : <PauseCircle size={14} />}{scheduleActionDisabled ? "处理中" : job.schedulePaused ? "恢复调度" : "暂停调度"}</span>}
                      <span role="button" tabIndex={deleteDisabled ? -1 : 0} aria-disabled={deleteDisabled} data-tooltip-content={deleteDisabled ? "任务执行中，请先停止" : "删除任务"} className={`row-delete ${deleteDisabled ? "disabled" : ""}`} onClick={(event) => { event.stopPropagation(); if (!deleteDisabled) setDeleteTarget(job); }} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); event.stopPropagation(); if (!deleteDisabled) setDeleteTarget(job); } }}><Trash2 size={14} />删除</span>
                    </span>
                  </div>
                  </div>
                </button>
              );})}
            </div>
          </div>
        ) : (
          <div className="inline-empty">
            <Archive size={22} />
            <div><strong>{backups.length ? "未找到匹配任务" : "暂无备份任务"}</strong><span>{backups.length ? "换个关键词再试。" : "创建备份任务后，运行状态和备份文件会显示在这里。"}</span></div>
          </div>
        )}
      </Card>
      {error && <div className="error-banner">{error}</div>}
      {deleteTarget && <ConfirmDialog title="删除备份任务" description="这是中危操作，只会删除任务记录，不会自动删除已经生成的备份产物。" tone="warning" confirmText="确认删除" submitting={deleting} onCancel={() => setDeleteTarget(null)} onConfirm={async () => {
        setDeleting(true);
        setError("");
        try {
          await onDelete(deleteTarget);
          setDeleteTarget(null);
        } catch (reason) {
          setError(reason instanceof Error ? reason.message : "删除备份任务失败");
        } finally {
          setDeleting(false);
        }
      }} details={[`任务：${deleteTarget.name}`, `状态：${migrationStatusLabel(deleteTarget.status)}`]} />}
    </div>
  );
}

function TaskSummary({ title, value, detail, tone = "gray" }: { title: string; value: number; detail: string; tone?: "gray" | "blue" | "green" | "orange" | "red" }) {
  return <Card className={`task-summary-card ${tone}`}><span>{title}</span><strong>{value}</strong><small>{detail}</small></Card>;
}

function BackupRunStatus({ job }: { job: BackupJob }) {
  const latestRun = latestBackupRun(job);
  if (hasFailedLastRun(job)) {
    const message = latestRun?.error ? `上次备份失败：${latestRun.error}` : "上次备份失败，请打开任务查看运行日志";
    return <span className="backup-run-status" data-tooltip-content={message}><Badge tone="red"><CircleAlert size={13} />上次失败</Badge><small>{job.schedulePaused ? "调度已暂停" : "计划仍有效"}</small></span>;
  }
  return <Badge tone={job.schedulePaused ? "gray" : migrationStatusTone(job.status)}>{job.schedulePaused ? "已暂停" : migrationStatusLabel(job.status)}</Badge>;
}

function TaskTypeTag({ job }: { job: BackupJob }) {
  const type = backupTaskType(job);
  return <span className={`task-type-tag ${type.tone}`}>{type.label}</span>;
}

function SourceRoute({ job }: { job: BackupJob }) {
  const meta = `${job.sourceType} · ${job.sourceDatabase}`;
  return (
    <div className="task-route route-with-icon" data-tooltip-content={`${job.sourceName}\n${meta}`}>
      <SourceTypeIcon type={job.sourceType} />
      <strong data-tooltip-content={job.sourceName}>{job.sourceName}</strong>
      <small data-tooltip-content={meta}>{meta}</small>
    </div>
  );
}

function SourceTypeIcon({ type }: { type: string }) {
  const databaseType = backupDatabaseType(type);
  return databaseType ? <DatabaseTypeIcon type={databaseType} /> : null;
}

function backupDatabaseType(type: string): DatabaseType | null {
  return ["PostgreSQL", "MySQL", "MongoDB", "Neo4j", "etcd", "Redis"].includes(type) ? type as DatabaseType : null;
}

function backupTaskType(job: BackupJob) {
  if (job.destinationType === "restore") return { label: "恢复", tone: "restore" };
  if (job.scheduleMode === "scheduled" || job.status === "scheduled") return { label: "定时备份", tone: "scheduled" };
  return { label: "备份", tone: "backup" };
}

function latestBackupRun(job: BackupJob) {
  return job.artifactVersions?.[0];
}

function hasFailedLastRun(job: BackupJob) {
  const scheduled = job.scheduleMode === "scheduled" || job.status === "scheduled";
  return scheduled && latestBackupRun(job)?.status === "failed";
}

function backupProgressTooltip(job: BackupJob) {
  const latestRun = latestBackupRun(job);
  if (hasFailedLastRun(job)) return latestRun?.error ? `上次备份在 ${job.progress}% 时失败：${latestRun.error}` : `上次备份在 ${job.progress}% 时失败`;
  return `当前进度 ${job.progress}%`;
}

function formatTaskTime(value?: string) {
  if (!value) return "-";
  return new Date(value).toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false });
}

function lastScheduleLabel(job: BackupJob) {
  if (job.lastRunAt) return formatTaskTime(job.lastRunAt);
  if (job.finishedAt && (job.scheduleMode === "scheduled" || job.status === "scheduled")) return formatTaskTime(job.finishedAt);
  return "尚未执行";
}

function nextScheduleLabel(job: BackupJob) {
  if (job.scheduleMode !== "scheduled" && job.status !== "scheduled") return "-";
  if (job.schedulePaused) return "调度已暂停";
  if (job.status === "running" || job.status === "pending") return "执行后更新";
  return nextRunLabel(job.nextRunAt);
}

function nextRunLabel(value?: string) {
  if (!value) return "待调度";
  return new Date(value).toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false });
}

function formatBytes(value: number) {
  if (!value) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  return `${(value / 1024 ** index).toFixed(index > 2 ? 1 : 0)} ${units[index]}`;
}
