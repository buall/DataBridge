import { Activity, ArrowRight, FileCheck2, Plus, Search, Star, Trash2 } from "lucide-react";
import { useMemo, useState } from "react";
import { migrationStatusLabel, migrationStatusTone } from "../status";
import type { DatabaseConnection, DatabaseType, MigrationJob } from "../types";
import { Badge, Button, Card, ConfirmDialog, DatabaseTypeIcon } from "../components/UI";

export function MigrationList({
  title = "迁移任务",
  description = "查看任务进度、结果和校验报告。",
  migrations,
  connections,
  onCreate,
  onOpen,
  onDelete,
  onToggleFavorite,
}: {
  title?: string;
  description?: string;
  migrations: MigrationJob[];
  connections: DatabaseConnection[];
  onCreate: () => void;
  onOpen: (job: MigrationJob) => void;
  onDelete: (job: MigrationJob) => Promise<void>;
  onToggleFavorite: (job: MigrationJob) => Promise<void>;
}) {
  const [query, setQuery] = useState("");
  const [deleteTarget, setDeleteTarget] = useState<MigrationJob | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState("");
  const [favoritingId, setFavoritingId] = useState("");
  const filtered = useMemo(() => {
    const keyword = query.trim().toLowerCase();
    const matching = !keyword ? migrations : migrations.filter((item) =>
      `${item.name} ${item.sourceDatabase} ${item.targetDatabase} ${item.sourceName} ${item.targetName}`
        .toLowerCase()
        .includes(keyword),
    );
    return [...matching].sort((left, right) => Number(Boolean(right.favorite)) - Number(Boolean(left.favorite)));
  }, [migrations, query]);

  const running = migrations.filter((item) => item.status === "running" || item.status === "pending").length;
  const completed = migrations.filter((item) => item.status === "completed").length;

  async function toggleFavorite(job: MigrationJob) {
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
        <div><h2>{title}</h2><p>{description}</p></div>
        <Button onClick={onCreate}><Plus size={17} />新建迁移</Button>
      </div>

      <div className="task-summary-grid">
        <TaskSummary title="全部任务" value={migrations.length} detail="任务记录已保存" />
        <TaskSummary title="进行中" value={running} detail="可查看实时进度" tone="blue" />
        <TaskSummary title="已完成" value={completed} detail="可查看校验报告" tone="green" />
      </div>

      <Card className="task-table-card">
        <div className="section-title compact">
          <div><h3>任务列表</h3><p>任务结束后可继续查看结果和报告。</p></div>
          <label className="search-box small"><Search size={15} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索任务或数据库" /></label>
        </div>
        {filtered.length ? (
          <div className="task-table migration-list-table">
            <div className="table-row table-head"><span aria-hidden="true" /><span>任务名称</span><span>源端 / 目标端</span><span>开始时间</span><span>进度</span><span>状态</span><span>操作</span></div>
            {filtered.map((job) => {
              const deleteDisabled = job.status === "running" || job.status === "pending";
              return (
              <button className="table-row task-row-button" key={job.id} onClick={() => onOpen(job)}>
                <div className="task-table-desktop">
                  <span role="button" tabIndex={favoritingId === job.id ? -1 : 0} aria-disabled={favoritingId === job.id} aria-label={job.favorite ? "取消收藏" : "收藏并置顶"} data-tooltip-mode="always" data-tooltip-content={job.favorite ? "取消收藏" : "收藏并置顶"} className={`row-favorite task-favorite-cell ${job.favorite ? "active" : ""} ${favoritingId === job.id ? "disabled" : ""}`} onClick={(event) => { event.stopPropagation(); if (favoritingId !== job.id) void toggleFavorite(job); }} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); event.stopPropagation(); if (favoritingId !== job.id) void toggleFavorite(job); } }}><Star size={15} fill={job.favorite ? "currentColor" : "none"} /></span>
                  <strong className="task-name" data-tooltip-content={displayTaskName(job)}>{displayTaskName(job)}</strong>
                  <MigrationRoute job={job} connections={connections} />
                  <span className="task-meta task-started-at">{formatDate(job.startedAt)}</span>
                  <div className="table-progress"><div><i style={{ width: `${job.progress}%` }} /></div><small>{job.progress}%</small></div>
                  <Badge tone={migrationStatusTone(job.status)}>{migrationStatusLabel(job.status)}</Badge>
                  <span className="row-actions">
                    <span className="row-action">{job.status === "running" || job.status === "pending" ? <Activity size={14} /> : <FileCheck2 size={14} />}{job.status === "running" || job.status === "pending" ? "查看进度" : "查看结果"}<ArrowRight size={14} /></span>
                    <span role="button" tabIndex={deleteDisabled ? -1 : 0} aria-disabled={deleteDisabled} data-tooltip-content={deleteDisabled ? "任务执行中，请先停止" : "删除任务"} className={`row-delete ${deleteDisabled ? "disabled" : ""}`} onClick={(event) => { event.stopPropagation(); if (!deleteDisabled) setDeleteTarget(job); }} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); event.stopPropagation(); if (!deleteDisabled) setDeleteTarget(job); } }}><Trash2 size={14} />删除</span>
                  </span>
                </div>
                <div className="task-mobile-card">
                  <div className="task-mobile-head">
                    <span className="task-mobile-icon"><Activity size={18} /></span>
                    <div className="task-mobile-copy">
                      <strong data-tooltip-content={displayTaskName(job)}>{displayTaskName(job)}</strong>
                      <MigrationRoute job={job} connections={connections} mobile />
                    </div>
                  </div>
                  <div className="task-mobile-details">
                    <div className="task-mobile-row">
                      <span>开始时间</span>
                      <strong>{formatDate(job.startedAt)}</strong>
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
                    <span className="task-mobile-leading">
                      <span role="button" tabIndex={favoritingId === job.id ? -1 : 0} aria-disabled={favoritingId === job.id} aria-label={job.favorite ? "取消收藏" : "收藏并置顶"} data-tooltip-mode="always" data-tooltip-content={job.favorite ? "取消收藏" : "收藏并置顶"} className={`row-favorite ${job.favorite ? "active" : ""} ${favoritingId === job.id ? "disabled" : ""}`} onClick={(event) => { event.stopPropagation(); if (favoritingId !== job.id) void toggleFavorite(job); }} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); event.stopPropagation(); if (favoritingId !== job.id) void toggleFavorite(job); } }}><Star size={15} fill={job.favorite ? "currentColor" : "none"} /></span>
                      <Badge tone={migrationStatusTone(job.status)}>{migrationStatusLabel(job.status)}</Badge>
                    </span>
                    <span className="row-actions">
                      <span className="row-action">{job.status === "running" || job.status === "pending" ? <Activity size={14} /> : <FileCheck2 size={14} />}{job.status === "running" || job.status === "pending" ? "查看进度" : "查看结果"}<ArrowRight size={14} /></span>
                      <span role="button" tabIndex={deleteDisabled ? -1 : 0} aria-disabled={deleteDisabled} data-tooltip-content={deleteDisabled ? "任务执行中，请先停止" : "删除任务"} className={`row-delete ${deleteDisabled ? "disabled" : ""}`} onClick={(event) => { event.stopPropagation(); if (!deleteDisabled) setDeleteTarget(job); }} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); event.stopPropagation(); if (!deleteDisabled) setDeleteTarget(job); } }}><Trash2 size={14} />删除</span>
                    </span>
                  </div>
                </div>
              </button>
            );})}
          </div>
        ) : (
          <div className="inline-empty">
            <Activity size={22} />
            <div><strong>{migrations.length ? "未找到匹配任务" : "暂无迁移任务"}</strong><span>{migrations.length ? "换个关键词再试。" : "创建迁移任务后，运行状态和校验报告会显示在这里。"}</span></div>
          </div>
        )}
      </Card>
      {error && <div className="error-banner">{error}</div>}
      {deleteTarget && <ConfirmDialog title="删除迁移任务" description="这是中危操作，会删除任务记录和校验结果记录。" tone="warning" confirmText="确认删除" submitting={deleting} onCancel={() => setDeleteTarget(null)} onConfirm={async () => {
        setDeleting(true);
        setError("");
        try {
          await onDelete(deleteTarget);
          setDeleteTarget(null);
        } catch (reason) {
          setError(reason instanceof Error ? reason.message : "删除迁移任务失败");
        } finally {
          setDeleting(false);
        }
      }} details={[`任务：${displayTaskName(deleteTarget)}`, `状态：${migrationStatusLabel(deleteTarget.status)}`]} />}
    </div>
  );
}

function displayTaskName(job: MigrationJob) {
  const databaseRoute = `${job.sourceDatabase} → ${job.targetDatabase}`;
  return job.name && job.name !== databaseRoute ? job.name : `${job.sourceName} 到 ${job.targetName}迁移`;
}

function MigrationRoute({ job, connections, mobile = false }: { job: MigrationJob; connections: DatabaseConnection[]; mobile?: boolean }) {
  const sourceType = migrationConnectionType(job.sourceName, connections);
  const targetType = migrationConnectionType(job.targetName, connections) ?? sourceType;
  return (
    <div className={mobile ? "migration-mobile-route" : "migration-route-pair"}>
      <MigrationEndpoint type={sourceType} name={job.sourceName} database={job.sourceDatabase} />
      <span className="migration-route-arrow" aria-hidden="true">→</span>
      <MigrationEndpoint type={targetType} name={job.targetName} database={job.targetDatabase} />
    </div>
  );
}

function MigrationEndpoint({ type, name, database }: { type: DatabaseType | null; name: string; database: string }) {
  const meta = type ? `${type} · ${database}` : database;
  return (
    <div className="migration-route-endpoint" data-tooltip-content={`${name}\n${meta}`}>
      {type ? <DatabaseTypeIcon type={type} /> : <span className="migration-route-icon-placeholder" aria-hidden="true">DB</span>}
      <strong data-tooltip-content={name}>{name}</strong>
      <small data-tooltip-content={meta}>{meta}</small>
    </div>
  );
}

function migrationConnectionType(name: string, connections: DatabaseConnection[]): DatabaseType | null {
  return connections.find((connection) => connection.name === name)?.type ?? null;
}

function TaskSummary({ title, value, detail, tone = "gray" }: { title: string; value: number; detail: string; tone?: "gray" | "blue" | "green" }) {
  return <Card className={`task-summary-card ${tone}`}><span>{title}</span><strong>{value}</strong><small>{detail}</small></Card>;
}

function formatDate(value: string) {
  return new Date(value).toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false });
}
