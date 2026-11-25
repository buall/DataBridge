import {
  Archive,
  CircleAlert,
  Clock,
  Download,
  Eye,
  PauseCircle,
  Play,
  PlayCircle,
  Plus,
  Search,
  Square,
  Star,
  Trash2,
} from "lucide-react";
import { useMemo, useState } from "react";
import { api } from "../api";
import { ActionMenu, Badge, Button, ConfirmDialog, DatabaseLogo, DensitySwitch, useCollectionView, type ActionMenuItem } from "../components/UI";
import { migrationStatusLabel, migrationStatusTone } from "../status";
import type { BackupJob, DatabaseType } from "../types";

export function BackupList({
  backups,
  onCreate,
  onOpen,
  onDelete,
  onTrigger,
  onCancel,
  onSetSchedulePaused,
  onToggleFavorite,
}: {
  backups: BackupJob[];
  onCreate: () => void;
  onOpen: (job: BackupJob) => void;
  onDelete: (job: BackupJob) => Promise<void>;
  onTrigger: (job: BackupJob) => Promise<void>;
  onCancel: (job: BackupJob) => Promise<void>;
  onSetSchedulePaused: (job: BackupJob, paused: boolean) => Promise<void>;
  onToggleFavorite: (job: BackupJob) => Promise<void>;
}) {
  const { prefs, updatePref } = useCollectionView();
  const [query, setQuery] = useState("");
  const [statusFilter, setStatusFilter] = useState<string>("全部");
  const [deleteTarget, setDeleteTarget] = useState<BackupJob | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [cancelTarget, setCancelTarget] = useState<BackupJob | null>(null);
  const [cancelling, setCancelling] = useState(false);
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

  const running = backups.filter((item) => item.status === "running" || item.status === "pending").length;
  const scheduled = backups.filter((item) => item.status === "scheduled" && !item.schedulePaused).length;
  const paused = backups.filter((item) => item.scheduleMode === "scheduled" && item.schedulePaused).length;
  const failedLastRuns = backups.filter((item) => hasFailedLastRun(item) || item.status === "failed").length;

  const filtered = useMemo(() => {
    const keyword = query.trim().toLowerCase();
    return backups
      .filter((item) => {
        if (statusFilter === "进行中" && item.status !== "running" && item.status !== "pending") return false;
        if (statusFilter === "定时计划" && !(item.status === "scheduled" && !item.schedulePaused)) return false;
        if (statusFilter === "已暂停" && !(item.scheduleMode === "scheduled" && item.schedulePaused)) return false;
        if (statusFilter === "上次失败" && !hasFailedLastRun(item) && item.status !== "failed") return false;
        if (
          keyword &&
          !`${item.name} ${item.sourceName} ${item.sourceDatabase} ${item.destination} ${item.artifactName ?? ""} ${latestBackupRun(item)?.error ?? ""}`
            .toLowerCase()
            .includes(keyword)
        ) {
          return false;
        }
        return true;
      })
      .sort((left, right) => Number(Boolean(right.favorite)) - Number(Boolean(left.favorite)));
  }, [backups, query, statusFilter]);

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
      <div className="h-row">
        <div>
          <h1 className="page-h">备份任务</h1>
          <div className="sec-sub">创建数据库备份并同步到本地目录或远端存储 · 支持版本链与增量还原</div>
        </div>
        <span className="sp" />
        <Button variant="secondary" onClick={exportSchedules}>
          <Download size={15} />
          导出定时任务
        </Button>
        <Button variant="primary" onClick={onCreate}>
          <Plus size={15} />
          新建备份
        </Button>
      </div>

      <div className="h-row">
        <span className="sfield">
          <span className="sfield-ic">
            <Search size={14} />
          </span>
          <input
            className="inp"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="搜索任务、数据库或存储…"
            style={{ width: 250 }}
          />
        </span>

        <span className="chiprow">
          {[
            ["全部", backups.length],
            ["定时计划", scheduled],
            ["进行中", running],
            ["上次失败", failedLastRuns],
            ["已暂停", paused],
          ].map(([label, count]) => (
            <button
              key={String(label)}
              type="button"
              className={`fchip ${statusFilter === label ? "on" : ""}`}
              onClick={() => setStatusFilter(String(label))}
            >
              {label} <span className="num" style={{ opacity: 0.72 }}>{count}</span>
            </button>
          ))}
        </span>

        <span className="sp" />
        <DensitySwitch value={prefs.bkp} onChange={(v) => updatePref("bkp", v as "comfortable" | "compact")} />
      </div>

      {error ? <div className="error-banner">{error}</div> : null}

      {filtered.length ? (
        <div className={`ledger ${prefs.bkp}`}>
          {filtered.map((job) => {
            const failedLastRun = hasFailedLastRun(job);
            const deleteDisabled = job.status === "running" || job.status === "pending";
            const triggerVisible = !deleteDisabled && (job.scheduleMode === "scheduled" || job.status === "scheduled");
            const triggerDisabled = deleteDisabled || triggeringId === job.id;
            const scheduleActionVisible = job.scheduleMode === "scheduled";
            const scheduleActionDisabled = updatingScheduleId === job.id;
            const lastScheduleTime = lastScheduleLabel(job);
            const nextScheduleTime = nextScheduleLabel(job);
            const dbType = backupDatabaseType(job.sourceType);
            const latestErr = latestBackupRun(job)?.error || job.error;

            const menuItems: ActionMenuItem[] = [
              {
                key: "open",
                label: "查看详情与版本",
                icon: <Eye size={14} />,
                onClick: () => onOpen(job),
              },
            ];
            if (deleteDisabled) {
              menuItems.push({
                key: "stop",
                label: "停止本次执行",
                icon: <Square size={14} />,
                danger: true,
                onClick: () => setCancelTarget(job),
              });
            }
            if (triggerVisible) {
              menuItems.push({
                key: "trigger",
                label: triggeringId === job.id ? "触发中..." : "立即触发",
                icon: <Play size={14} />,
                disabled: triggerDisabled,
                onClick: () => void triggerJob(job),
              });
            }
            if (scheduleActionVisible) {
              menuItems.push({
                key: "sched",
                label: scheduleActionDisabled ? "处理中..." : job.schedulePaused ? "恢复调度" : "暂停调度",
                icon: job.schedulePaused ? <PlayCircle size={14} /> : <PauseCircle size={14} />,
                disabled: scheduleActionDisabled,
                onClick: () => void setSchedulePaused(job),
              });
            }
            menuItems.push({
              key: "del",
              label: "删除任务",
              icon: <Trash2 size={14} />,
              danger: true,
              disabled: deleteDisabled,
              dividerBefore: true,
              tooltip: deleteDisabled ? "任务执行中，请先停止" : "删除任务",
              onClick: () => setDeleteTarget(job),
            });

            return (
              <div
                key={job.id}
                className="lrow"
                style={{ alignItems: "flex-start" }}
                onClick={() => onOpen(job)}
              >
                {dbType ? (
                  <DatabaseLogo type={dbType} name={job.sourceName} size="sm" />
                ) : (
                  <span className="tglyph sm">
                    <Archive size={16} />
                  </span>
                )}
                <div className="lr-main">
                  <div className="lr-t">
                    <span>{job.name}</span>
                    <BackupRunStatus job={job} />
                    <span className="tag">{backupTaskType(job)}</span>
                    {job.backupMode === "mongo_oplog" ? (
                      <span className="tag">
                        oplog 增量{job.mongoRpoHours ? ` · RPO ${job.mongoRpoHours}h` : ""}
                      </span>
                    ) : null}
                  </div>
                  <div className="lr-s">
                    <span>
                      {job.sourceName} / {job.sourceDatabase}
                    </span>
                    <span>
                      <Clock size={12} style={{ verticalAlign: "-2px" }} /> 上次 {lastScheduleTime}
                    </span>
                    {nextScheduleTime !== "-" ? <span>下次 {nextScheduleTime}</span> : null}
                    <span>目标：{job.destination}</span>
                  </div>
                  {deleteDisabled ? (
                    <div className="progress" style={{ marginTop: 8, maxWidth: 520 }}>
                      <i className="shimmer" style={{ width: `${job.progress}%` }} />
                    </div>
                  ) : null}
                  {(failedLastRun || job.status === "failed") && latestErr ? (
                    <div className="diff-callout" style={{ marginTop: 8 }}>
                      <span>{latestErr}</span>
                    </div>
                  ) : null}
                </div>

                <div
                  style={{ display: "flex", alignItems: "center", gap: 6, flex: "none" }}
                  onClick={(event) => event.stopPropagation()}
                >
                  <div style={{ textAlign: "right", marginRight: 6 }}>
                    <div className="num" style={{ fontWeight: 600 }}>
                      {job.artifactSize ? formatBytes(job.artifactSize) : `${job.progress}%`}
                    </div>
                    <div style={{ fontSize: 11.5, color: "var(--muted)" }}>
                      {job.retentionDays ? `保留 ${job.retentionDays} 天` : "备份任务"}
                    </div>
                  </div>
                  <button
                    type="button"
                    className={`star-btn ${job.favorite ? "on" : ""}`}
                    aria-label={job.favorite ? "取消收藏" : "收藏并置顶"}
                    data-tooltip-mode="always"
                    data-tooltip-content={job.favorite ? "取消收藏" : "收藏并置顶"}
                    disabled={favoritingId === job.id}
                    onClick={() => void toggleFavorite(job)}
                  >
                    <Star size={16} fill={job.favorite ? "currentColor" : "none"} />
                  </button>
                  <ActionMenu ariaLabel={`${job.name} 更多操作`} items={menuItems} />
                </div>
              </div>
            );
          })}
        </div>
      ) : (
        <div className="empty">
          <div className="e-t">{backups.length ? "未找到匹配任务" : "暂无备份任务"}</div>
          <div className="e-s">
            {backups.length ? "换个关键词或筛选状态再试。" : "创建备份任务后，运行状态和备份文件会显示在这里。"}
          </div>
        </div>
      )}

      {cancelTarget ? (
        <ConfirmDialog
          title="停止备份任务"
          description={
            cancelTarget.scheduleMode === "scheduled"
              ? "停止本次执行，后续定时调度保持原设置。"
              : "停止当前任务，未完成的备份不会继续执行。"
          }
          tone="warning"
          confirmText="确认停止"
          submitting={cancelling}
          onCancel={() => setCancelTarget(null)}
          onConfirm={async () => {
            setCancelling(true);
            setError("");
            try {
              await onCancel(cancelTarget);
              setCancelTarget(null);
            } catch (reason) {
              setError(reason instanceof Error ? reason.message : "停止备份任务失败");
            } finally {
              setCancelling(false);
            }
          }}
          details={[`任务：${cancelTarget.name}`, "已生成的历史备份产物不会被自动删除。"]}
        />
      ) : null}

      {deleteTarget ? (
        <ConfirmDialog
          title="删除备份任务"
          description="这是中危操作，只会删除任务记录，不会自动删除已经生成的备份产物。"
          tone="warning"
          confirmText="确认删除"
          submitting={deleting}
          onCancel={() => setDeleteTarget(null)}
          onConfirm={async () => {
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
          }}
          details={[`任务：${deleteTarget.name}`, `状态：${migrationStatusLabel(deleteTarget.status)}`]}
        />
      ) : null}
    </div>
  );
}

function BackupRunStatus({ job }: { job: BackupJob }) {
  const latestRun = latestBackupRun(job);
  if (hasFailedLastRun(job)) {
    const message = latestRun?.error ? `上次备份失败：${latestRun.error}` : "上次备份失败，请打开任务查看运行日志";
    return (
      <span data-tooltip-content={message}>
        <Badge tone="red">
          <CircleAlert size={12} />
          上次失败 · {job.schedulePaused ? "调度已暂停" : "计划仍有效"}
        </Badge>
      </span>
    );
  }
  if (job.scheduleMode === "scheduled" && job.status !== "running" && job.status !== "pending" && latestRun?.status === "cancelled") {
    return (
      <Badge tone="gray">
        上次取消 · {job.schedulePaused ? "调度已暂停" : "计划仍有效"}
      </Badge>
    );
  }
  const paused = job.schedulePaused && job.status !== "running" && job.status !== "pending";
  return (
    <Badge tone={paused ? "gray" : migrationStatusTone(job.status)}>
      <span className="pd" />
      {paused ? "已暂停" : migrationStatusLabel(job.status)}
    </Badge>
  );
}

function backupDatabaseType(type: string): DatabaseType | null {
  return ["PostgreSQL", "MySQL", "MongoDB", "Neo4j", "etcd", "Redis"].includes(type) ? (type as DatabaseType) : null;
}

function backupTaskType(job: BackupJob) {
  if (job.destinationType === "restore") return "恢复";
  if (job.scheduleMode === "scheduled" || job.status === "scheduled") return "定时备份";
  return "一次性备份";
}

function latestBackupRun(job: BackupJob) {
  return job.artifactVersions?.[0];
}

function hasFailedLastRun(job: BackupJob) {
  const scheduled = job.scheduleMode === "scheduled" || job.status === "scheduled";
  return scheduled && job.status !== "running" && job.status !== "pending" && latestBackupRun(job)?.status === "failed";
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
  return job.nextRunAt ? formatTaskTime(job.nextRunAt) : "待调度";
}

function formatBytes(value: number) {
  if (!value) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  return `${(value / 1024 ** index).toFixed(index > 2 ? 1 : 0)} ${units[index]}`;
}
