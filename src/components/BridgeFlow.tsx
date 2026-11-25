import { migrationStageLabel } from "../labels";
import type { DatabaseType, MigrationJob } from "../types";
import { DatabaseLogo } from "./UI";

export const MIGRATION_STAGES: Array<[string, string]> = [
  ["precheck", "连接检查"],
  ["target-prep", "目标处理"],
  ["schema", "结构准备"],
  ["dump", "源端导出"],
  ["data", "数据传输"],
  ["restore", "目标导入"],
  ["constraints", "约束索引"],
  ["validation", "结果校验"],
];

function stageIndex(stage: string) {
  if (stage === "pending") return -1;
  if (stage === "completed") return MIGRATION_STAGES.length;
  const idx = MIGRATION_STAGES.findIndex(([key]) => key === stage);
  return idx >= 0 ? idx : 0;
}

export function BridgeFlow({
  sourceName,
  sourceDatabase,
  targetName,
  targetDatabase,
  type,
  status,
  stage,
  progress,
  rowsCopied = 0,
  rowsTotal = 0,
  tablesDone = 0,
  tablesTotal = 0,
  currentTable,
  ratePerSecond,
  etaMinutes,
  mini = false,
  showStats = true,
  isMongo = false,
  isNeo4j = false,
}: {
  sourceName: string;
  sourceDatabase: string;
  targetName: string;
  targetDatabase: string;
  type: DatabaseType;
  status: MigrationJob["status"];
  stage: string;
  progress: number;
  rowsCopied?: number;
  rowsTotal?: number;
  tablesDone?: number;
  tablesTotal?: number;
  currentTable?: string;
  ratePerSecond?: number;
  etaMinutes?: number | null;
  mini?: boolean;
  showStats?: boolean;
  isMongo?: boolean;
  isNeo4j?: boolean;
}) {
  const idx = stageIndex(stage);
  const unitLabel = isNeo4j ? "节点/关系" : isMongo ? "集合" : "行";
  const countLabel = isNeo4j ? "迁移项" : isMongo ? "已完成集合" : "已完成表";

  return (
    <div className={`bridge ${mini ? "mini" : ""}`}>
      <div className="bridge-nodes">
        <div className="bnode">
          <DatabaseLogo type={type} name={sourceName} size={mini ? "sm" : "md"} />
          <div style={{ minWidth: 0 }}>
            <div className="bn-t" data-tooltip-content={sourceName}>{sourceName}</div>
            <div className="bn-s" data-tooltip-content={sourceDatabase}>{sourceDatabase}{mini ? "" : " · 源端"}</div>
          </div>
        </div>
        <div className="beam">
          <div className="beam-line">
            <i style={{ width: `${Math.max(0, Math.min(100, progress))}%` }} />
            {status === "running" ? <span className="flowdot" /> : null}
          </div>
          {!mini ? (
            <div className="beads">
              {MIGRATION_STAGES.map(([id, label], i) => {
                const cls =
                  status === "completed" || i < idx
                    ? "done"
                    : i === idx
                      ? status === "failed" || status === "cancelled"
                        ? "fail"
                        : "now"
                      : "";
                return (
                  <div className={`bead ${cls}`} key={id}>
                    <span className="bd" />
                    <span className="bl">{label}</span>
                  </div>
                );
              })}
            </div>
          ) : null}
        </div>
        <div className="bnode">
          <DatabaseLogo type={type} name={targetName} size={mini ? "sm" : "md"} />
          <div style={{ minWidth: 0 }}>
            <div className="bn-t" data-tooltip-content={targetName}>{targetName}</div>
            <div className="bn-s" data-tooltip-content={targetDatabase}>{targetDatabase}{mini ? "" : " · 目标端"}</div>
          </div>
        </div>
      </div>

      {showStats && !mini ? (
        <div className="beam-stats">
          <div className="bstat">
            <div className="bs-v num">{progress}%</div>
            <div className="bs-l">整体进度</div>
          </div>
          <div className="bstat">
            <div className="bs-v num">
              {formatNum(rowsCopied)}{" "}
              <span style={{ color: "var(--faint)", fontSize: 13 }}>/ {formatNum(rowsTotal)}</span>
            </div>
            <div className="bs-l">已迁移{unitLabel}</div>
          </div>
          <div className="bstat">
            <div className="bs-v num">
              {tablesDone} / {tablesTotal}
            </div>
            <div className="bs-l">{countLabel}</div>
          </div>
          {status === "running" && ratePerSecond != null ? (
            <div className="bstat">
              <div className="bs-v num">
                {formatNum(ratePerSecond)} {unitLabel}/秒
              </div>
              <div className="bs-l">实时吞吐</div>
            </div>
          ) : null}
          {status === "running" && etaMinutes != null ? (
            <div className="bstat">
              <div className="bs-v num">约 {etaMinutes} 分钟</div>
              <div className="bs-l">预计剩余</div>
            </div>
          ) : null}
          <div className="bstat">
            <div className="bs-v" style={{ fontSize: 14.5, paddingTop: 3 }}>
              {currentTable || migrationStageLabel(stage)}
            </div>
            <div className="bs-l">{currentTable ? "当前对象" : "当前阶段"}</div>
          </div>
        </div>
      ) : null}
    </div>
  );
}

function formatNum(value: number) {
  return new Intl.NumberFormat("zh-CN").format(value);
}
