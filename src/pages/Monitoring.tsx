import { AlertTriangle, ChevronLeft, Square } from "lucide-react";
import { useEffect, useState } from "react";
import { api } from "../api";
import { BridgeFlow } from "../components/BridgeFlow";
import { Badge, Button, Card, ConfirmDialog } from "../components/UI";
import { migrationStageLabel } from "../labels";
import { migrationStatusLabel, migrationStatusTone } from "../status";
import type { DatabaseConnection, DatabaseType, MigrationJob } from "../types";

export function Monitoring({
  jobId,
  connections = [],
  onBack,
  onComplete,
}: {
  jobId: string;
  connections?: DatabaseConnection[];
  onBack?: () => void;
  onComplete: (job: MigrationJob) => void;
}) {
  const [job, setJob] = useState<MigrationJob | null>(null);
  const [error, setError] = useState("");
  const [cancelConfirmOpen, setCancelConfirmOpen] = useState(false);
  const [cancelling, setCancelling] = useState(false);

  useEffect(() => {
    let stopped = false;
    async function refresh() {
      try {
        const next = await api.migration(jobId);
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
          setError(reason instanceof Error ? reason.message : "无法获取任务状态");
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
    try {
      await api.cancelMigration(jobId);
      setCancelConfirmOpen(false);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "停止迁移任务失败");
    } finally {
      setCancelling(false);
    }
  }

  if (!job) {
    return (
      <div className="empty">
        <div className="e-t">任务准备中</div>
        <div className="e-s">{error || "正在连接源端和目标端数据库..."}</div>
      </div>
    );
  }

  const failed = job.status === "failed" || job.status === "cancelled";
  const elapsed = Math.max(1, (Date.now() - new Date(job.startedAt).getTime()) / 1000);
  const rowsPerSecond = Math.round(job.rowsCopied / elapsed);
  const isMongoMigration = job.results.some((result) => result.table.includes("collections)"));
  const isNeo4jMigration = job.results.some((result) => result.table.endsWith(" graph"));
  const nativeCommandStage = job.stage === "dump" || job.stage === "restore" || job.stage === "target-prep";
  const dbType: DatabaseType =
    connections.find((c) => c.name === job.sourceName)?.type ??
    (isNeo4jMigration ? "Neo4j" : isMongoMigration ? "MongoDB" : "PostgreSQL");

  const etaMinutes =
    job.status === "running" && rowsPerSecond > 0 && job.rowsTotal > job.rowsCopied
      ? Math.max(1, Math.round((job.rowsTotal - job.rowsCopied) / rowsPerSecond / 60))
      : null;

  return (
    <div className="page-stack">
      <div className="h-row">
        {onBack ? (
          <Button variant="secondary" className="sm" onClick={onBack}>
            <ChevronLeft size={14} /> 返回列表
          </Button>
        ) : null}
        <span className="tag mono-t">{job.id}</span>
        <Badge tone={migrationStatusTone(job.status)}>
          <span className="pd" />
          {migrationStatusLabel(job.status)}
        </Badge>
        <span className="sp" />
        {!failed ? (
          <Button variant="danger" onClick={() => setCancelConfirmOpen(true)}>
            <Square size={14} />
            停止任务
          </Button>
        ) : null}
      </div>

      <div>
        <h1 className="page-h">{job.name || `${job.sourceName} → ${job.targetName}`}</h1>
        <div className="sec-sub">
          {job.sourceName}/{job.sourceDatabase} → {job.targetName}/{job.targetDatabase} · 运行时长 {formatDuration(elapsed * 1000)}
        </div>
      </div>

      {error ? <div className="error-banner">{error}</div> : null}
      {failed ? (
        <div className="attn">
          <AlertTriangle size={17} style={{ color: "var(--danger)", flex: "none", marginTop: 2 }} />
          <div>
            <div className="a-t">任务已停止（{migrationStageLabel(job.stage)}）</div>
            <div className="a-s">{job.error || "迁移任务已取消"}</div>
          </div>
        </div>
      ) : null}

      <Card>
        <BridgeFlow
          sourceName={job.sourceName}
          sourceDatabase={job.sourceDatabase}
          targetName={job.targetName}
          targetDatabase={job.targetDatabase}
          type={dbType}
          status={job.status}
          stage={job.stage}
          progress={job.progress}
          rowsCopied={job.rowsCopied}
          rowsTotal={job.rowsTotal}
          tablesDone={job.tablesDone}
          tablesTotal={job.tablesTotal}
          currentTable={job.currentTable}
          ratePerSecond={nativeCommandStage ? undefined : rowsPerSecond}
          etaMinutes={etaMinutes}
          isMongo={isMongoMigration}
          isNeo4j={isNeo4jMigration}
        />
      </Card>

      <div className="ov-grid">
        <div>
          <div className="h-row" style={{ marginBottom: 10 }}>
            <h3 className="sec-title">
              {isNeo4jMigration ? "图迁移状态" : isMongoMigration ? "集合迁移状态" : "逐表迁移状态"}
            </h3>
            <Badge tone="green">实时</Badge>
          </div>
          {job.results.length ? (
            <div className="ledger">
              {job.results.map((result) => (
                <div className="vrow" key={result.table}>
                  <span className="vt">{result.table}</span>
                  <span style={{ color: "var(--muted)", fontSize: 12.5 }}>
                    已复制 <b className="num" style={{ color: "var(--ink)" }}>{formatNumber(result.copiedRows)}</b>{" "}
                    {isNeo4jMigration ? "节点/关系" : isMongoMigration ? "集合" : "行"}
                  </span>
                  <span className="num" style={{ color: "var(--muted)", fontSize: 12.5 }}>
                    {result.durationMs ? formatDuration(result.durationMs) : "—"}
                  </span>
                  <span>
                    {result.error ? (
                      <Badge tone="red">失败</Badge>
                    ) : (
                      <Badge tone={result.valid ? "green" : "blue"}>{result.valid ? "已校验" : "已复制"}</Badge>
                    )}
                  </span>
                </div>
              ))}
            </div>
          ) : (
            <div className="empty">
              <div className="e-t">
                {nativeCommandStage
                  ? `${migrationStageLabel(job.stage)}中`
                  : `等待首个${isNeo4jMigration ? "图" : isMongoMigration ? "集合" : "表"}完成迁移`}
              </div>
              <div className="e-s">原生命令阶段完成后将汇报对象级校验结果。</div>
            </div>
          )}
        </div>

        <div>
          <div className="h-row" style={{ marginBottom: 10 }}>
            <h3 className="sec-title">实时事件</h3>
            <span className="tag">错误 {job.events.filter((e) => e.level === "ERROR").length}</span>
          </div>
          <Card className="pad" style={{ maxHeight: 420, overflowY: "auto" }}>
            {[...job.events]
              .reverse()
              .slice(0, 20)
              .map((event, index) => (
                <div className="evt" key={`${event.time}-${index}`}>
                  <span className="et">{new Date(event.time).toLocaleTimeString("zh-CN", { hour12: false })}</span>
                  <span className={`ed2 ed-${event.level}`} />
                  <span className="em">{event.message}</span>
                </div>
              ))}
          </Card>
        </div>
      </div>

      {cancelConfirmOpen ? (
        <ConfirmDialog
          title="停止迁移任务"
          description="这是中危操作，任务会被取消，未完成的数据表不会继续迁移。"
          tone="warning"
          confirmText="确认停止"
          submitting={cancelling}
          onCancel={() => setCancelConfirmOpen(false)}
          onConfirm={cancel}
          details={[`任务：${job.sourceName} → ${job.targetName}`, `数据库：${job.sourceDatabase} → ${job.targetDatabase}`]}
        />
      ) : null}
    </div>
  );
}

function formatNumber(value: number) {
  return new Intl.NumberFormat("zh-CN").format(value);
}
function formatDuration(value: number) {
  const seconds = Math.round(value / 1000);
  return seconds < 60 ? `${seconds}s` : `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
}
