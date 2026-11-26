import { Activity, Eye, FileCheck2, Plus, Search, Star, Trash2 } from "lucide-react";
import { useMemo, useState } from "react";
import { BridgeFlow } from "../components/BridgeFlow";
import { ActionMenu, Badge, Button, ConfirmDialog, DatabaseLogo, DensitySwitch, useCollectionView } from "../components/UI";
import { migrationStageLabel } from "../labels";
import { migrationStatusLabel, migrationStatusTone } from "../status";
import type { DatabaseConnection, DatabaseType, MigrationJob } from "../types";

export function MigrationList({
  title = "迁移任务",
  description = "同类型实例之间迁移 · 查看任务进度、结果与校验报告。",
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
  const { prefs, updatePref } = useCollectionView();
  const [query, setQuery] = useState("");
  const [statusFilter, setStatusFilter] = useState<string>("全部");
  const [deleteTarget, setDeleteTarget] = useState<MigrationJob | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState("");
  const [favoritingId, setFavoritingId] = useState("");

  const filtered = useMemo(() => {
    const keyword = query.trim().toLowerCase();
    return migrations
      .filter((item) => {
        if (statusFilter !== "全部" && migrationStatusLabel(item.status) !== statusFilter) return false;
        if (
          keyword &&
          !`${item.name} ${item.id} ${item.sourceDatabase} ${item.targetDatabase} ${item.sourceName} ${item.targetName}`
            .toLowerCase()
            .includes(keyword)
        ) {
          return false;
        }
        return true;
      })
      .sort((left, right) => Number(Boolean(right.favorite)) - Number(Boolean(left.favorite)));
  }, [migrations, query, statusFilter]);

  const running = migrations.filter((item) => item.status === "running" || item.status === "pending").length;
  const completed = migrations.filter((item) => item.status === "completed").length;
  const failed = migrations.filter((item) => item.status === "failed").length;

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
      <div className="h-row">
        <div>
          <h1 className="page-h">{title}</h1>
          <div className="sec-sub">{description}</div>
        </div>
        <span className="sp" />
        <Button variant="primary" onClick={onCreate}>
          <Plus size={15} />
          新建迁移
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
            placeholder="搜索任务名、编号、源 / 目标…"
            style={{ width: 250 }}
          />
        </span>

        <span className="chiprow">
          {[
            ["全部", migrations.length],
            ["进行中", running],
            ["已完成", completed],
            ["已失败", failed],
            ["已取消", migrations.filter((m) => m.status === "cancelled").length],
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
        <DensitySwitch value={prefs.mig} onChange={(v) => updatePref("mig", v as "comfortable" | "compact")} />
      </div>

      {error ? <div className="error-banner">{error}</div> : null}

      {filtered.length ? (
        <div className={`ledger card-list ${prefs.mig}`}>
          {filtered.map((job) => {
            const active = job.status === "running" || job.status === "pending";
            const dbType: DatabaseType =
              connections.find((c) => c.name === job.sourceName)?.type ??
              connections.find((c) => c.name === job.targetName)?.type ??
              "PostgreSQL";

            return (
              <div
                key={job.id}
                className="lrow"
                onClick={() => onOpen(job)}
              >
                <DatabaseLogo type={dbType} name={job.sourceName} />
                <div className="lr-main">
                  <div className="lr-t">
                    <span>{displayTaskName(job)}</span>
                    <Badge tone={migrationStatusTone(job.status)}>
                      <span className="pd" />
                      {migrationStatusLabel(job.status)}
                    </Badge>
                    <span className="tag mono-t">{job.id}</span>
                  </div>

                  <div className="mig-bridge" style={{ margin: "9px 0 7px", maxWidth: 560 }}>
                    <BridgeFlow
                      sourceName={job.sourceName}
                      sourceDatabase={job.sourceDatabase}
                      targetName={job.targetName}
                      targetDatabase={job.targetDatabase}
                      type={dbType}
                      status={job.status}
                      stage={job.stage}
                      progress={job.progress}
                      mini
                      showStats={false}
                    />
                  </div>

                  <div className="lr-s">
                    <span>阶段：{migrationStageLabel(job.stage)}</span>
                    <span className="num">
                      {formatNumber(job.rowsCopied)} / {formatNumber(job.rowsTotal)}
                    </span>
                    <span>
                      {job.tablesDone} / {job.tablesTotal} 项
                    </span>
                    <span>{formatDate(job.startedAt)}</span>
                  </div>

                  {job.status === "running" ? (
                    <div className="progress" style={{ marginTop: 8, maxWidth: 520 }}>
                      <i className="shimmer" style={{ width: `${job.progress}%` }} />
                    </div>
                  ) : null}

                  {job.status === "failed" && job.error ? (
                    <div className="diff-callout" style={{ marginTop: 8 }}>
                      <span>{job.error}</span>
                    </div>
                  ) : null}
                </div>

                <div
                  className="lr-actions"
                  onClick={(event) => event.stopPropagation()}
                >
                  <div className="lr-act-btns">
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
                    <ActionMenu
                      ariaLabel={`${displayTaskName(job)} 更多操作`}
                      items={[
                        {
                          key: "open",
                          label: active ? "查看实时进度" : "查看校验报告",
                          icon: active ? <Activity size={14} /> : <FileCheck2 size={14} />,
                          onClick: () => onOpen(job),
                        },
                        {
                          key: "detail",
                          label: "打开详情页",
                          icon: <Eye size={14} />,
                          onClick: () => onOpen(job),
                        },
                        {
                          key: "del",
                          label: "删除任务",
                          icon: <Trash2 size={14} />,
                          danger: true,
                          disabled: active,
                          dividerBefore: true,
                          tooltip: active ? "任务执行中，请先停止" : "删除任务",
                          onClick: () => setDeleteTarget(job),
                        },
                      ]}
                    />
                  </div>
                  <div className="lr-act-meta">
                    <span className="num" style={{ fontWeight: 600, color: "var(--ink-2)" }}>
                      {job.progress}%
                    </span>
                    <span>·</span>
                    <span>{active ? "实时监控" : "校验报告"}</span>
                  </div>
                </div>
              </div>
            );
          })}
        </div>
      ) : (
        <div className="empty">
          <div className="e-t">{migrations.length ? "未找到匹配任务" : "暂无迁移任务"}</div>
          <div className="e-s">
            {migrations.length ? "换个关键词或状态筛选再试。" : "创建迁移任务后，运行状态和校验报告会显示在这里。"}
          </div>
        </div>
      )}

      {deleteTarget ? (
        <ConfirmDialog
          title="删除迁移任务"
          description="这是中危操作，会删除任务记录和校验结果记录。"
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
              setError(reason instanceof Error ? reason.message : "删除迁移任务失败");
            } finally {
              setDeleting(false);
            }
          }}
          details={[`任务：${displayTaskName(deleteTarget)}`, `状态：${migrationStatusLabel(deleteTarget.status)}`]}
        />
      ) : null}
    </div>
  );
}

function displayTaskName(job: MigrationJob) {
  const databaseRoute = `${job.sourceDatabase} → ${job.targetDatabase}`;
  return job.name && job.name !== databaseRoute ? job.name : `${job.sourceName} 到 ${job.targetName}迁移`;
}

function formatNumber(value: number) {
  return new Intl.NumberFormat("zh-CN").format(value);
}

function formatDate(value: string) {
  return new Date(value).toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false });
}
