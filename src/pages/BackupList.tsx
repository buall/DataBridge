import { Archive, ArrowRight, Download, FileCheck2, Plus, Search, Trash2 } from "lucide-react";
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
}: {
  backups: BackupJob[];
  onCreate: () => void;
  onOpen: (job: BackupJob) => void;
  onDelete: (job: BackupJob) => Promise<void>;
}) {
  const [query, setQuery] = useState("");
  const [deleteTarget, setDeleteTarget] = useState<BackupJob | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState("");
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
    if (!keyword) return backups;
    return backups.filter((item) =>
      `${item.name} ${item.sourceName} ${item.sourceDatabase} ${item.destination} ${item.artifactName ?? ""}`.toLowerCase().includes(keyword),
    );
  }, [backups, query]);
  const running = backups.filter((item) => item.status === "running" || item.status === "pending").length;
  const scheduled = backups.filter((item) => item.status === "scheduled").length;
  const completed = backups.filter((item) => item.status === "completed").length;
  const totalBytes = backups.reduce((sum, item) => sum + (item.artifactSize ?? 0), 0);

  return (
    <div className="page-stack">
      <div className="page-heading">
        <div><h2>备份任务</h2><p>创建数据库备份并同步到本地目录或远端存储。</p></div>
        <div className="page-heading-actions"><Button variant="secondary" onClick={exportSchedules}><Download size={17} />导出定时任务</Button><Button onClick={onCreate}><Plus size={17} />新建备份</Button></div>
      </div>
      <div className="task-summary-grid">
        <TaskSummary title="全部任务" value={backups.length} detail="任务记录已保存" />
        <TaskSummary title="进行中" value={running} detail="可查看实时进度" tone="blue" />
        <TaskSummary title="定时计划" value={scheduled} detail="到点自动执行" tone="orange" />
        <TaskSummary title="已完成" value={completed} detail={`累计 ${formatBytes(totalBytes)}`} tone="green" />
      </div>
      <Card className="task-table-card">
        <div className="section-title compact">
          <div><h3>任务列表</h3><p>备份完成后可查看文件名、大小和目标位置。</p></div>
          <label className="search-box small"><Search size={15} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索任务或数据库" /></label>
        </div>
        {filtered.length ? (
          <div className="task-table backup-list-table">
            <div className="table-row table-head"><span>任务名称</span><span>任务类型</span><span>创建时间</span><span>源端 / 数据库</span><span>目标存储</span><span>进度</span><span>状态</span><span>操作</span></div>
            {filtered.map((job) => {
              const deleteDisabled = job.status === "running" || job.status === "pending";
              return (
              <button className="table-row task-row-button" key={job.id} onClick={() => onOpen(job)}>
                <div className="task-table-desktop">
                  <strong className="task-name">{job.name}</strong>
                  <TaskTypeTag job={job} />
                  <time className="task-time">{formatTaskTime(job.startedAt)}</time>
                  <SourceRoute job={job} />
                  <span className="task-name task-meta task-destination">{job.destination}</span>
                  <div className="table-progress"><div><i style={{ width: `${job.progress}%` }} /></div><small>{job.status === "scheduled" ? nextRunLabel(job.nextRunAt) : `${job.progress}%`}</small></div>
                  <Badge tone={migrationStatusTone(job.status)}>{migrationStatusLabel(job.status)}</Badge>
                  <span className="row-actions">
                    <span className="row-action">{job.status === "running" || job.status === "pending" ? <Archive size={14} /> : <FileCheck2 size={14} />}{job.status === "running" || job.status === "pending" ? "查看进度" : job.status === "scheduled" ? "查看计划" : "查看结果"}<ArrowRight size={14} /></span>
                    <span role="button" tabIndex={deleteDisabled ? -1 : 0} aria-disabled={deleteDisabled} title={deleteDisabled ? "任务执行中，请先停止" : "删除任务"} className={`row-delete ${deleteDisabled ? "disabled" : ""}`} onClick={(event) => { event.stopPropagation(); if (!deleteDisabled) setDeleteTarget(job); }} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); event.stopPropagation(); if (!deleteDisabled) setDeleteTarget(job); } }}><Trash2 size={14} />删除</span>
                  </span>
                </div>
                <div className="task-mobile-card">
                  <div className="task-mobile-head">
                    <span className="task-mobile-icon"><Archive size={18} /></span>
                    <div className="task-mobile-copy">
                      <strong>{job.name}</strong>
                      <span className="task-mobile-source"><SourceTypeIcon type={job.sourceType} />{job.sourceName}</span>
                      <small>{job.sourceType} · {job.sourceDatabase}</small>
                    </div>
                  </div>
                  <div className="task-mobile-details">
                    <div className="task-mobile-row">
                      <span>任务类型</span>
                      <TaskTypeTag job={job} />
                    </div>
                    <div className="task-mobile-row">
                      <span>创建时间</span>
                      <strong>{formatTaskTime(job.startedAt)}</strong>
                    </div>
                    <div className="task-mobile-row">
                      <span>目标存储</span>
                      <strong>{job.destination}</strong>
                    </div>
                    <div className="task-mobile-row">
                      <span>{job.status === "scheduled" ? "下次执行" : "进度"}</span>
                      <strong>{job.status === "scheduled" ? nextRunLabel(job.nextRunAt) : `${job.progress}%`}</strong>
                    </div>
                  </div>
                  <div className="task-mobile-progress">
                    <i style={{ width: `${job.progress}%` }} />
                  </div>
                  <div className="task-mobile-foot">
                    <Badge tone={migrationStatusTone(job.status)}>{migrationStatusLabel(job.status)}</Badge>
                    <span className="row-actions">
                      <span className="row-action">{job.status === "running" || job.status === "pending" ? <Archive size={14} /> : <FileCheck2 size={14} />}{job.status === "running" || job.status === "pending" ? "查看进度" : job.status === "scheduled" ? "查看计划" : "查看结果"}<ArrowRight size={14} /></span>
                      <span role="button" tabIndex={deleteDisabled ? -1 : 0} aria-disabled={deleteDisabled} title={deleteDisabled ? "任务执行中，请先停止" : "删除任务"} className={`row-delete ${deleteDisabled ? "disabled" : ""}`} onClick={(event) => { event.stopPropagation(); if (!deleteDisabled) setDeleteTarget(job); }} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); event.stopPropagation(); if (!deleteDisabled) setDeleteTarget(job); } }}><Trash2 size={14} />删除</span>
                    </span>
                  </div>
                </div>
              </button>
            );})}
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

function TaskSummary({ title, value, detail, tone = "gray" }: { title: string; value: number; detail: string; tone?: "gray" | "blue" | "green" | "orange" }) {
  return <Card className={`task-summary-card ${tone}`}><span>{title}</span><strong>{value}</strong><small>{detail}</small></Card>;
}

function TaskTypeTag({ job }: { job: BackupJob }) {
  const type = backupTaskType(job);
  return <span className={`task-type-tag ${type.tone}`}>{type.label}</span>;
}

function SourceRoute({ job }: { job: BackupJob }) {
  return (
    <div className="task-route route-with-icon">
      <SourceTypeIcon type={job.sourceType} />
      <strong>{job.sourceName}</strong>
      <small>{job.sourceType} · {job.sourceDatabase}</small>
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

function formatTaskTime(value?: string) {
  if (!value) return "-";
  return new Date(value).toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false });
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
