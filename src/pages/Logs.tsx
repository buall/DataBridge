import { AlertTriangle, CheckCircle2, Clock3, Database, Info, Search } from "lucide-react";
import { useMemo, useState } from "react";
import { Badge, Card } from "../components/UI";
import type { AuditLogEntry, MigrationEvent, MigrationJob } from "../types";

interface LogEntry extends MigrationEvent {
  jobId: string;
  jobName: string;
  jobStatus: MigrationJob["status"];
  sourceName: string;
  targetName: string;
  sourceDatabase: string;
  targetDatabase: string;
}

export function Logs({ migrations, auditLogs = [] }: { migrations: MigrationJob[]; auditLogs?: AuditLogEntry[] }) {
  const [query, setQuery] = useState("");
  const entries = useMemo(
    () => {
      const migrationEntries = migrations.flatMap((job) =>
        job.events.map((event) => ({
          ...event,
          jobId: job.id,
          jobName: job.name,
          jobStatus: job.status,
          sourceName: job.sourceName,
          targetName: job.targetName,
          sourceDatabase: job.sourceDatabase,
          targetDatabase: job.targetDatabase,
        })),
      );
      const auditEntries = auditLogs.map((event) => ({
        time: event.time,
        level: event.level,
        message: event.detail ? `${event.message} · ${event.detail}` : event.message,
        jobId: event.id,
        jobName: event.action,
        jobStatus: "completed" as MigrationJob["status"],
        sourceName: event.connectionName || event.resourceName || "系统操作",
        targetName: event.status || "完成",
        sourceDatabase: event.database || "—",
        targetDatabase: event.resourceType || "connection",
      }));
      return [...migrationEntries, ...auditEntries].sort((a, b) => new Date(b.time).getTime() - new Date(a.time).getTime());
    },
    [migrations, auditLogs],
  );

  const filtered = useMemo(() => {
    const keyword = query.trim().toLowerCase();
    if (!keyword) return entries;
    return entries.filter((entry) =>
      `${entry.jobName} ${entry.sourceName} ${entry.targetName} ${entry.sourceDatabase} ${entry.targetDatabase} ${entry.level} ${entry.message}`
        .toLowerCase()
        .includes(keyword),
    );
  }, [entries, query]);

  const errors = entries.filter((entry) => entry.level === "ERROR").length;
  const warnings = entries.filter((entry) => entry.level === "WARN").length;
  const latest = entries[0];

  return (
    <div className="page-stack">
      <div className="page-heading">
        <div>
          <h2>运行日志</h2>
          <p>按时间汇总迁移事件。</p>
        </div>
        <label className="search-box small">
          <Search size={15} />
          <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索任务或事件" />
        </label>
      </div>

      <div className="log-summary-grid">
        <LogSummary title="任务数量" value={migrations.length} detail="已保存任务" />
        <LogSummary title="事件总数" value={entries.length} detail={latest ? `最近 ${formatTime(latest.time)}` : "暂无任务事件"} tone="blue" />
        <LogSummary title="告警事件" value={warnings} detail="WARN 级别事件" tone="orange" />
        <LogSummary title="错误事件" value={errors} detail="ERROR 级别事件" tone={errors ? "red" : "green"} />
      </div>

      <Card className="log-table-card">
        <div className="section-title compact">
          <div>
            <h3>事件列表</h3>
            <p>{entries.length ? "来自后端迁移引擎的事件。" : "当前还没有迁移事件。"}</p>
          </div>
        </div>
        {filtered.length ? (
          <div className="log-table">
            <div className="log-row log-head"><span>时间</span><span>级别</span><span>实例 / 数据库</span><span>事件内容</span><span>状态</span></div>
            {filtered.map((entry, index) => (
              <div className="log-row" key={`${entry.jobId}-${entry.time}-${index}`}>
                <time>{formatTime(entry.time)}</time>
                <Badge tone={levelTone(entry.level)}>{entry.level}</Badge>
                <div className="log-task">
                  <strong>{entry.sourceName} → {entry.targetName}</strong>
                  <small>{entry.sourceDatabase} → {entry.targetDatabase}</small>
                </div>
                <span className="log-message">{entry.message}</span>
                <span className="log-status">{statusIcon(entry.jobStatus)}{statusLabel(entry.jobStatus)}</span>
              </div>
            ))}
          </div>
        ) : (
          <div className="inline-empty">
            <Clock3 size={22} />
            <div>
              <strong>{entries.length ? "未找到匹配日志" : "暂无运行日志"}</strong>
              <span>{entries.length ? "换个关键词再试。" : "启动迁移后，这里会按时间展示事件。"}</span>
            </div>
          </div>
        )}
      </Card>
    </div>
  );
}

function LogSummary({ title, value, detail, tone = "gray" }: { title: string; value: number; detail: string; tone?: "gray" | "blue" | "green" | "orange" | "red" }) {
  return <Card className={`log-summary-card ${tone}`}><span>{title}</span><strong>{value}</strong><small>{detail}</small></Card>;
}

function formatTime(value: string) {
  return new Date(value).toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false });
}

function levelTone(level: string) {
  return level === "ERROR" ? "red" : level === "WARN" ? "orange" : "green";
}

function statusLabel(status: MigrationJob["status"]) {
  return status === "completed" ? "已完成" : status === "failed" ? "失败" : status === "cancelled" ? "已取消" : status === "pending" ? "等待中" : "运行中";
}

function statusIcon(status: MigrationJob["status"]) {
  if (status === "failed" || status === "cancelled") return <AlertTriangle size={14} />;
  if (status === "completed") return <CheckCircle2 size={14} />;
  if (status === "running") return <Database size={14} />;
  return <Info size={14} />;
}
