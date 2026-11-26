import { AlertTriangle, CheckCircle2, Clock3, Database, Info, Search } from "lucide-react";
import { useMemo, useState } from "react";
import { Badge, Card, SelectField } from "../components/UI";
import type { AuditLogEntry, MigrationEvent, MigrationJob } from "../types";

interface LogEntry extends MigrationEvent {
  jobId: string;
  jobName: string;
  jobStatus: MigrationJob["status"];
  sourceName: string;
  targetName: string;
  sourceDatabase: string;
  targetDatabase: string;
  category: "migration" | "audit" | "api";
}

type CategoryFilter = "all" | "migration" | "audit" | "api";
type LevelFilter = "all" | "INFO" | "WARN" | "ERROR";

const visibleLogLimit = 200;

export function Logs({ migrations, auditLogs = [] }: { migrations: MigrationJob[]; auditLogs?: AuditLogEntry[] }) {
  const [query, setQuery] = useState("");
  const [category, setCategory] = useState<CategoryFilter>("all");
  const [level, setLevel] = useState<LevelFilter>("all");

  const entries = useMemo(() => {
    const migrationEntries: LogEntry[] = migrations.flatMap((job) =>
      job.events.map((event) => ({
        ...event,
        jobId: job.id,
        jobName: job.name,
        jobStatus: job.status,
        sourceName: job.sourceName,
        targetName: job.targetName,
        sourceDatabase: job.sourceDatabase,
        targetDatabase: job.targetDatabase,
        category: "migration" as const,
      })),
    );
    const auditEntries: LogEntry[] = auditLogs.map((event) => ({
      time: event.time,
      level: event.level,
      message: event.detail ? `${event.message} · ${event.detail}` : event.message,
      jobId: event.id,
      jobName: event.action,
      jobStatus:
        event.level === "ERROR"
          ? ("failed" as MigrationJob["status"])
          : ("completed" as MigrationJob["status"]),
      sourceName:
        event.action === "api_request"
          ? "API 请求"
          : event.connectionName || event.resourceName || "系统操作",
      targetName: event.status || "完成",
      sourceDatabase: event.database || "—",
      targetDatabase: event.resourceType || "connection",
      category: event.action === "api_request" ? ("api" as const) : ("audit" as const),
    }));
    return [...migrationEntries, ...auditEntries].sort(
      (a, b) => new Date(b.time).getTime() - new Date(a.time).getTime(),
    );
  }, [migrations, auditLogs]);

  const counts = useMemo(
    () => ({
      all: entries.length,
      migration: entries.filter((item) => item.category === "migration").length,
      audit: entries.filter((item) => item.category === "audit").length,
      api: entries.filter((item) => item.category === "api").length,
      errors: entries.filter((item) => item.level === "ERROR").length,
      warnings: entries.filter((item) => item.level === "WARN").length,
    }),
    [entries],
  );

  const filtered = useMemo(() => {
    const keyword = query.trim().toLowerCase();
    return entries.filter((entry) => {
      if (category !== "all" && entry.category !== category) return false;
      if (level !== "all" && entry.level !== level) return false;
      if (!keyword) return true;
      return `${entry.jobName} ${entry.sourceName} ${entry.targetName} ${entry.sourceDatabase} ${entry.targetDatabase} ${entry.level} ${entry.message}`
        .toLowerCase()
        .includes(keyword);
    });
  }, [category, entries, level, query]);

  const visibleEntries = filtered.slice(0, visibleLogLimit);
  const hiddenEntries = Math.max(0, filtered.length - visibleEntries.length);
  const latest = entries[0];

  const groupedByDay = useMemo(() => {
    const groups: Array<{ day: string; label: string; items: LogEntry[] }> = [];
    const map = new Map<string, { day: string; label: string; items: LogEntry[] }>();
    visibleEntries.forEach((entry) => {
      const key = dayKey(entry.time);
      let group = map.get(key);
      if (!group) {
        group = { day: key, label: formatDayLabel(entry.time), items: [] };
        map.set(key, group);
        groups.push(group);
      }
      group.items.push(entry);
    });
    return groups;
  }, [visibleEntries]);

  return (
    <div className="page-stack">
      <div className="h-row">
        <div className="page-h">
          <h2>运行日志</h2>
          <p>汇总迁移任务事件、系统审计操作与 API 访问记录</p>
        </div>
      </div>

      <div className="log-summary-grid">
        <LogSummary title="迁移任务" value={migrations.length} detail="已保存任务" />
        <LogSummary
          title="事件总数"
          value={counts.all}
          detail={latest ? `最近 ${formatFullTime(latest.time)}` : "暂无任务事件"}
          tone="blue"
        />
        <LogSummary title="告警事件" value={counts.warnings} detail="WARN 级别事件" tone="orange" />
        <LogSummary
          title="错误事件"
          value={counts.errors}
          detail="ERROR 级别事件"
          tone={counts.errors ? "red" : "green"}
        />
      </div>

      <div className="h-row log-filters" style={{ gap: 8 }}>
        <div className="sfield log-search">
          <Search size={14} className="sfield-ic" />
          <input
            className="inp"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="搜索任务、实例、数据库或事件..."
            aria-label="搜索运行日志"
          />
        </div>
        <div className="chiprow">
          <button type="button" className={category === "all" ? "fchip on" : "fchip"} onClick={() => setCategory("all")}>
            全部 {counts.all}
          </button>
          <button
            type="button"
            className={category === "migration" ? "fchip on" : "fchip"}
            onClick={() => setCategory("migration")}
          >
            TASK {counts.migration}
          </button>
          <button
            type="button"
            className={category === "audit" ? "fchip on" : "fchip"}
            onClick={() => setCategory("audit")}
          >
            AUDIT {counts.audit}
          </button>
          <button
            type="button"
            className={category === "api" ? "fchip on" : "fchip"}
            onClick={() => setCategory("api")}
          >
            API {counts.api}
          </button>
        </div>
        <div className="sp" />
        <div className="log-level-select">
          <SelectField
            inline
            width="100%"
            ariaLabel="日志级别筛选"
            value={level}
            onChange={(next) => setLevel(next as LevelFilter)}
          >
            <option value="all">全部级别</option>
            <option value="INFO">INFO</option>
            <option value="WARN">WARN</option>
            <option value="ERROR">ERROR</option>
          </SelectField>
        </div>
      </div>

      {filtered.length ? (
        <div className="log-stream">
          {groupedByDay.map((group) => (
            <div key={group.day} style={{ display: "grid", gap: 12 }}>
              <div className="log-date">
                <span>{group.label}</span>
                <span className="num" style={{ marginLeft: 8, color: "var(--faint)" }}>
                  · {group.items.length} 条
                </span>
              </div>
              <div className="ledger">
                {group.items.map((entry, index) => (
                  <div className="lrow" key={`${entry.jobId}-${entry.time}-${index}`}>
                    <time className="mono num log-time">
                      {formatClockTime(entry.time)}
                    </time>
                    <div className="log-badges">
                      <span className="log-col log-col-level">
                        <Badge tone={levelTone(entry.level)}>{entry.level}</Badge>
                      </span>
                      <span className="log-col log-col-cat">
                        <Badge tone={categoryTone(entry.category)}>{categoryLabel(entry.category)}</Badge>
                      </span>
                    </div>
                    <div className="lr-main">
                      <div
                        className="lr-t"
                        style={{
                          fontWeight: entry.level === "ERROR" || entry.level === "WARN" ? 500 : 400,
                          color:
                            entry.level === "ERROR"
                              ? "var(--danger)"
                              : entry.level === "WARN"
                                ? "var(--warn)"
                                : "var(--ink-2)",
                        }}
                      >
                        {entry.message}
                      </div>
                      <div className="lr-s mono">
                        {entry.sourceName} → {entry.targetName} · {entry.sourceDatabase} → {entry.targetDatabase}
                      </div>
                    </div>
                    <span className="log-col log-col-status">
                      <Badge tone={statusBadgeTone(entry.jobStatus)}>
                        {statusIcon(entry.jobStatus)}
                        {statusLabel(entry.jobStatus)}
                      </Badge>
                    </span>
                  </div>
                ))}
              </div>
            </div>
          ))}

          {hiddenEntries > 0 ? (
            <div className="info-callout">
              仅显示最近 {visibleLogLimit} 条事件，还有 {hiddenEntries} 条历史记录已收起；可使用顶部搜索或类别/级别筛选精确定位。
            </div>
          ) : null}
        </div>
      ) : (
        <Card className="empty">
          <Clock3 size={28} style={{ color: "var(--muted)", marginBottom: 8 }} />
          <div className="e-t">{entries.length ? "未找到匹配日志" : "暂无运行日志"}</div>
          <div className="e-s">
            {entries.length
              ? "调整搜索关键词、类别或级别筛选再试。"
              : "发生接口调用、迁移或备份后，这里会按时间展示事件。"}
          </div>
        </Card>
      )}
    </div>
  );
}

function LogSummary({
  title,
  value,
  detail,
  tone = "gray",
}: {
  title: string;
  value: number;
  detail: string;
  tone?: "gray" | "blue" | "green" | "orange" | "red";
}) {
  const accent =
    tone === "blue"
      ? "var(--blue)"
      : tone === "green"
        ? "var(--ok)"
        : tone === "orange"
          ? "var(--warn)"
          : tone === "red"
            ? "var(--danger)"
            : "var(--ink)";
  return (
    <Card className="pad" style={{ display: "grid", gap: 4 }}>
      <span style={{ fontSize: 12, fontWeight: 600, color: "var(--muted)" }}>{title}</span>
      <strong className="num" style={{ fontSize: 24, fontWeight: 700, color: accent, letterSpacing: "-0.02em" }}>
        {value}
      </strong>
      <small style={{ fontSize: 12, color: "var(--muted)" }}>{detail}</small>
    </Card>
  );
}

function dayKey(value: string) {
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return "unknown";
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

function formatDayLabel(value: string) {
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  const target = new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const diffDays = Math.round((today - target) / 86400000);
  const formatted = d.toLocaleDateString("zh-CN", { year: "numeric", month: "2-digit", day: "2-digit" });
  if (diffDays === 0) return `今天 · ${formatted}`;
  if (diffDays === 1) return `昨天 · ${formatted}`;
  return formatted;
}

function formatClockTime(value: string) {
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  return d.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false });
}

function formatFullTime(value: string) {
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  return d.toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  });
}

function levelTone(level: string) {
  return level === "ERROR" ? "red" : level === "WARN" ? "orange" : "green";
}

function categoryTone(category: LogEntry["category"]) {
  if (category === "migration") return "blue";
  if (category === "audit") return "indigo";
  return "gray";
}

function categoryLabel(category: LogEntry["category"]) {
  if (category === "migration") return "TASK";
  if (category === "audit") return "AUDIT";
  return "API";
}

function statusBadgeTone(status: MigrationJob["status"]) {
  if (status === "failed" || status === "cancelled") return "red";
  if (status === "running") return "blue";
  if (status === "completed") return "green";
  return "gray";
}

function statusLabel(status: MigrationJob["status"]) {
  return status === "completed"
    ? "已完成"
    : status === "failed"
      ? "失败"
      : status === "cancelled"
        ? "已取消"
        : status === "pending"
          ? "等待中"
          : "运行中";
}

function statusIcon(status: MigrationJob["status"]) {
  if (status === "failed" || status === "cancelled") return <AlertTriangle size={11} />;
  if (status === "completed") return <CheckCircle2 size={11} />;
  if (status === "running") return <Database size={11} />;
  return <Info size={11} />;
}
