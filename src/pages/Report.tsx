import { AlertTriangle, Check, ChevronLeft, Download, Search, X } from "lucide-react";
import { useMemo, useState } from "react";
import { BridgeFlow } from "../components/BridgeFlow";
import { Badge, Button, Card } from "../components/UI";
import { migrationStageLabel } from "../labels";
import { migrationStatusLabel, migrationStatusTone } from "../status";
import type { DatabaseConnection, DatabaseType, MigrationJob } from "../types";

export function Report({
  job,
  connections = [],
  onBack,
}: {
  job: MigrationJob;
  connections?: DatabaseConnection[];
  onBack?: () => void;
}) {
  const [query, setQuery] = useState("");
  const isMongoMigration = job.results.some((item) => item.table.includes("collections)"));
  const isNeo4jMigration = job.results.some((item) => item.table.endsWith(" graph"));
  const validCount = job.results.filter((item) => item.valid).length;
  const total = job.results.length;
  const score = total ? Math.round((validCount / total) * 100) : 0;
  const allValid = total > 0 && validCount === total;

  const dbType: DatabaseType =
    connections.find((c) => c.name === job.sourceName)?.type ??
    (isNeo4jMigration ? "Neo4j" : isMongoMigration ? "MongoDB" : "PostgreSQL");

  const filteredResults = useMemo(() => {
    const keyword = query.trim().toLowerCase();
    if (!keyword) return job.results;
    return job.results.filter((row) =>
      `${row.table} ${row.error ?? ""} ${row.valid ? "一致" : "需确认"}`.toLowerCase().includes(keyword),
    );
  }, [job.results, query]);

  const maxRows = Math.max(1, ...filteredResults.map((r) => Math.max(r.sourceRows, r.targetRows)));
  const unit = isNeo4jMigration ? "节点/关系" : isMongoMigration ? "集合" : "行";

  return (
    <div className="page-stack">
      <div className="h-row no-print">
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
        <Badge tone={allValid ? "green" : "orange"}>{allValid ? "校验通过" : "存在差异"}</Badge>
        <span className="sp" />
        <Button variant="secondary" onClick={() => window.print()}>
          <Download size={15} />
          导出报告
        </Button>
      </div>

      <div>
        <h1 className="page-h">{job.name || `${job.sourceName} → ${job.targetName}`}</h1>
        <div className="sec-sub">
          数据一致性 {score}% · {validCount} / {total} 项一致 · 耗时 {duration(job)} · 开始于{" "}
          {new Date(job.startedAt).toLocaleString("zh-CN", { hour12: false })}
        </div>
      </div>

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
          isMongo={isMongoMigration}
          isNeo4j={isNeo4jMigration}
        />
      </Card>

      {job.status !== "completed" && job.error ? (
        <div className="attn">
          <AlertTriangle size={17} style={{ color: "var(--danger)", flex: "none", marginTop: 2 }} />
          <div>
            <div className="a-t">任务结束于「{migrationStageLabel(job.stage)}」阶段</div>
            <div className="a-s">{job.error}</div>
          </div>
        </div>
      ) : null}

      <div className="ov-grid">
        <div>
          <div className="h-row" style={{ marginBottom: 10 }}>
            <h3 className="sec-title">
              {isNeo4jMigration ? "图校验明细" : isMongoMigration ? "集合校验明细" : "逐表校验台账"}
            </h3>
            <span className="tag">
              一致性 <b className="num">&nbsp;{score}%</b>（{validCount} / {total}）
            </span>
            <span className="sp" />
            <span className="sfield">
              <span className="sfield-ic">
                <Search size={13} />
              </span>
              <input
                className="inp"
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder="搜索对象…"
                style={{ width: 190, height: 32, fontSize: 12.5 }}
              />
            </span>
          </div>

          {filteredResults.length ? (
            <div className="ledger">
              {filteredResults.map((row) => {
                const bad = !row.valid;
                const diff = row.targetRows - row.sourceRows;
                return (
                  <div className={`vrow ${bad ? "bad" : ""}`} key={row.table}>
                    <span className="vt">
                      {row.table}
                      {bad && diff !== 0 ? (
                        <span className="pill err" style={{ marginLeft: 6 }}>
                          相差 {diff > 0 ? `+${formatNumber(diff)}` : formatNumber(diff)}
                        </span>
                      ) : null}
                      {row.error ? (
                        <div style={{ fontSize: 11.5, color: "var(--danger)", fontFamily: "var(--font)", marginTop: 2 }}>
                          {row.error}
                        </div>
                      ) : null}
                    </span>
                    <span className="vbars">
                      <span className="vbar src">
                        <span style={{ width: 30 }}>源端</span>
                        <span className="track">
                          <i style={{ width: `${Math.max(4, (row.sourceRows / maxRows) * 100)}%` }} />
                        </span>
                        <span className="n">
                          {formatNumber(row.sourceRows)} {unit}
                        </span>
                      </span>
                      <span className={`vbar tgt ${bad ? "bad" : ""}`}>
                        <span style={{ width: 30 }}>目标</span>
                        <span className="track">
                          <i style={{ width: `${Math.max(4, (row.targetRows / maxRows) * 100)}%` }} />
                        </span>
                        <span className="n">
                          {formatNumber(row.targetRows)} {unit}
                        </span>
                      </span>
                    </span>
                    <span className="num" style={{ color: "var(--muted)", fontSize: 12.5 }}>
                      {formatDuration(row.durationMs)}
                    </span>
                    <span>
                      {row.valid ? (
                        <Badge tone="green">
                          <Check size={12} /> 一致
                        </Badge>
                      ) : (
                        <Badge tone="red">
                          <X size={12} /> 需确认
                        </Badge>
                      )}
                    </span>
                  </div>
                );
              })}
            </div>
          ) : (
            <div className="empty">
              <div className="e-t">没有匹配的校验结果</div>
            </div>
          )}
        </div>

        <div>
          <h3 className="sec-title" style={{ marginBottom: 10 }}>
            执行事件流
          </h3>
          <Card className="pad" style={{ maxHeight: 420, overflowY: "auto" }}>
            {job.events.length ? (
              job.events.map((event, index) => (
                <div className="evt" key={`${event.time}-${index}`}>
                  <span className="et">{new Date(event.time).toLocaleTimeString("zh-CN", { hour12: false })}</span>
                  <span className={`ed2 ed-${event.level}`} />
                  <span className="em">{event.message}</span>
                </div>
              ))
            ) : (
              <div style={{ color: "var(--muted)", fontSize: 13 }}>暂无事件记录</div>
            )}
          </Card>
        </div>
      </div>
    </div>
  );
}

function formatNumber(value: number) {
  return new Intl.NumberFormat("zh-CN").format(value);
}
function formatDuration(ms: number) {
  const seconds = Math.max(0, Math.round(ms / 1000));
  return seconds < 60 ? `${seconds}s` : `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
}
function duration(job: MigrationJob) {
  return job.finishedAt ? formatDuration(new Date(job.finishedAt).getTime() - new Date(job.startedAt).getTime()) : "—";
}
