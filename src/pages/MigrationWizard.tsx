import { ArrowLeft, ArrowRight, Box, Check, ChevronDown, Database, Search, Zap } from "lucide-react";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { api } from "../api";
import { BridgeFlow } from "../components/BridgeFlow";
import { Badge, Button, Card, ConfirmDialog, DatabaseLogo, DatabaseTypeIcon, RegionFlag, SelectField } from "../components/UI";
import type { DatabaseConnection, DatabaseInfo, MigrationConfig, TableInfo } from "../types";

const steps = ["连接与数据库", "选择数据表", "迁移配置", "确认执行"];

function isMigrationConnection(connection: DatabaseConnection) {
  return (
    connection.type === "PostgreSQL" ||
    connection.type === "MySQL" ||
    connection.type === "MongoDB" ||
    connection.type === "Neo4j"
  );
}

export function MigrationWizard({
  connections,
  initialSourceId,
  onStart,
}: {
  connections: DatabaseConnection[];
  initialSourceId?: string;
  onStart: (config: MigrationConfig) => Promise<void>;
}) {
  const migrationConnections = useMemo(() => connections.filter(isMigrationConnection), [connections]);
  const initialSource =
    initialSourceId && migrationConnections.some((item) => item.id === initialSourceId)
      ? initialSourceId
      : (migrationConnections[0]?.id ?? "");
  const initialSourceType = migrationConnections.find((item) => item.id === initialSource)?.type;
  const initialTarget =
    migrationConnections.find((item) => item.id !== initialSource && item.type === initialSourceType)?.id ?? "";

  const [step, setStep] = useState(0);
  const [query, setQuery] = useState("");
  const [sourceDatabases, setSourceDatabases] = useState<DatabaseInfo[]>([]);
  const [targetDatabases, setTargetDatabases] = useState<DatabaseInfo[]>([]);
  const [tables, setTables] = useState<TableInfo[]>([]);
  const [loading, setLoading] = useState(false);
  const [starting, setStarting] = useState(false);
  const [error, setError] = useState("");
  const [startConfirmOpen, setStartConfirmOpen] = useState(false);
  const [config, setConfig] = useState<MigrationConfig>({
    name: "",
    engine: "native",
    sourceId: initialSource,
    sourceDatabase: "",
    targetId: initialTarget,
    targetDatabase: "",
    tables: [],
    conflictStrategy: "backup",
  });

  useEffect(() => {
    const nextSource =
      initialSourceId && migrationConnections.some((item) => item.id === initialSourceId)
        ? initialSourceId
        : (migrationConnections[0]?.id ?? "");
    setConfig((current) => {
      const sourceId = migrationConnections.some((item) => item.id === current.sourceId) ? current.sourceId : nextSource;
      const sourceType = migrationConnections.find((item) => item.id === sourceId)?.type;
      const targetCandidate = migrationConnections.some(
        (item) => item.id === current.targetId && item.id !== sourceId && item.type === sourceType,
      )
        ? current.targetId
        : (migrationConnections.find((item) => item.id !== sourceId && item.type === sourceType)?.id ?? "");
      if (sourceId === current.sourceId && targetCandidate === current.targetId) {
        return current;
      }
      return { ...current, sourceId, targetId: targetCandidate };
    });
  }, [migrationConnections, config.sourceId, initialSourceId]);

  useEffect(() => {
    if (!config.sourceId) {
      setSourceDatabases([]);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError("");
    api.databases(config.sourceId)
      .then((items) => {
        if (cancelled) return;
        setSourceDatabases(items);
        setConfig((current) => ({
          ...current,
          sourceDatabase: items.some((item) => item.name === current.sourceDatabase)
            ? current.sourceDatabase
            : (items[0]?.name ?? ""),
        }));
      })
      .catch((reason) => {
        if (!cancelled) setError(reason.message);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [config.sourceId]);

  useEffect(() => {
    if (!config.targetId) {
      setTargetDatabases([]);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError("");
    api.databases(config.targetId)
      .then((items) => {
        if (cancelled) return;
        setTargetDatabases(items);
        setConfig((current) => ({
          ...current,
          targetDatabase: items.some((item) => item.name === current.targetDatabase)
            ? current.targetDatabase
            : (items[0]?.name ?? ""),
        }));
      })
      .catch((reason) => {
        if (!cancelled) setError(reason.message);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [config.targetId]);

  const source = migrationConnections.find((item) => item.id === config.sourceId);
  const targetConnections = useMemo(
    () => migrationConnections.filter((item) => item.id !== config.sourceId && item.type === source?.type),
    [migrationConnections, config.sourceId, source?.type],
  );
  const target = targetConnections.find((item) => item.id === config.targetId);
  const databaseType = source?.type && target?.type && source.type === target.type ? source.type : source?.type;
  const supportsCopy = databaseType === "PostgreSQL";
  const isMongo = databaseType === "MongoDB";
  const isNeo4j = databaseType === "Neo4j";
  const selectedTables = tables.filter((item) => config.tables.includes(item.id));
  const selectedBytes = selectedTables.reduce((sum, item) => sum + item.sizeBytes, 0);
  const selectedRows = selectedTables.reduce((sum, item) => sum + item.estimatedRows, 0);
  const filteredTables = useMemo(
    () => tables.filter((item) => item.id.toLowerCase().includes(query.toLowerCase())),
    [tables, query],
  );

  useEffect(() => {
    if (step !== 1 || !config.sourceId || !config.sourceDatabase || isNeo4j) return;
    let cancelled = false;
    setLoading(true);
    setError("");
    api.tables(config.sourceId, config.sourceDatabase)
      .then((items) => {
        if (cancelled) return;
        setTables(items);
        setConfig((current) => ({
          ...current,
          tables: current.tables.filter((id) => items.some((item) => item.id === id)),
        }));
      })
      .catch((reason) => {
        if (!cancelled) setError(reason.message);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [step, config.sourceId, config.sourceDatabase, isNeo4j]);

  useEffect(() => {
    if (!supportsCopy && config.engine === "copy") {
      setConfig((current) => ({ ...current, engine: "native" }));
    }
  }, [supportsCopy, config.engine]);

  function toggleTable(id: string) {
    setConfig((current) => ({
      ...current,
      tables: current.tables.includes(id)
        ? current.tables.filter((item) => item !== id)
        : [...current.tables, id],
    }));
  }

  async function start() {
    setStarting(true);
    setError("");
    try {
      const validTableIds = new Set(tables.map((item) => item.id));
      const normalizedTables = isMongo
        ? config.tables.filter((id) => validTableIds.has(id))
        : isNeo4j
          ? []
          : config.tables;
      await onStart({
        ...config,
        tables: normalizedTables,
        name: config.name || `${source?.name ?? config.sourceDatabase} 到 ${target?.name ?? config.targetDatabase}迁移`,
      });
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "创建迁移任务失败");
      setStarting(false);
    }
  }

  function requestStart() {
    if (config.conflictStrategy !== "skip") {
      setStartConfirmOpen(true);
      return;
    }
    void start();
  }

  if (!migrationConnections.length) {
    return (
      <div className="empty">
        <div className="e-t">暂无可迁移连接</div>
        <div className="e-s">当前迁移支持 PostgreSQL、MySQL、MongoDB 和 Neo4j。请先添加至少两个可访问的同类型数据库连接。</div>
      </div>
    );
  }

  const nextDisabled =
    loading ||
    starting ||
    (step === 0 &&
      (!config.sourceDatabase ||
        !config.targetDatabase ||
        (config.sourceId === config.targetId && config.sourceDatabase === config.targetDatabase))) ||
    (step === 1 && !isMongo && !isNeo4j && !config.tables.length);

  return (
    <div className="page-stack constrained">
      <div>
        <h1 className="page-h">创建迁移任务</h1>
        <div className="sec-sub">仅支持同类型实例之间迁移 · 目标数据库需预先存在</div>
      </div>

      <div className="steps">
        {steps.map((label, index) => (
          <div className={`step ${index === step ? "on" : index < step ? "done" : ""}`} key={label}>
            <span className="sn">{index < step ? <Check size={12} /> : index + 1}</span>
            <span>{label}</span>
          </div>
        ))}
      </div>

      {error ? <div className="error-banner">{error}</div> : null}

      {step === 0 ? (
        <Card className="pad">
          <h3 className="sec-title">选择源端与目标端数据库</h3>
          <div className="sec-sub" style={{ marginBottom: 16 }}>
            目标数据库需提前创建，迁移任务会在其中写入所选对象和数据。
          </div>

          <div className="grid2">
            <EndpointSelect
              title="源端"
              connectionId={config.sourceId}
              database={config.sourceDatabase}
              connections={migrationConnections}
              databases={sourceDatabases}
              onConnection={(sourceId) =>
                setConfig({ ...config, sourceId, sourceDatabase: "", targetId: "", targetDatabase: "" })
              }
              onDatabase={(sourceDatabase) => setConfig({ ...config, sourceDatabase })}
            />
            <EndpointSelect
              title="目标端"
              connectionId={config.targetId}
              database={config.targetDatabase}
              connections={targetConnections}
              databases={targetDatabases}
              onConnection={(targetId) => setConfig({ ...config, targetId, targetDatabase: "" })}
              onDatabase={(targetDatabase) => setConfig({ ...config, targetDatabase })}
            />
          </div>

          {loading ? <div style={{ color: "var(--muted)", fontSize: 13 }}>正在加载数据库...</div> : null}
          {source && !targetConnections.length ? (
            <div className="error-banner" style={{ marginTop: 10 }}>
              没有可用的同类型目标连接，请先添加另一个 {source.type} 连接。
            </div>
          ) : null}
          {config.sourceId &&
          config.sourceId === config.targetId &&
          config.sourceDatabase &&
          config.sourceDatabase === config.targetDatabase ? (
            <div className="error-banner" style={{ marginTop: 10 }}>
              源端和目标端不能是同一个数据库。
            </div>
          ) : null}
        </Card>
      ) : null}

      {step === 1 ? (
        <div className="ov-grid">
          <Card className="pad">
            {isNeo4j ? (
              <>
                <h3 className="sec-title">确认迁移图数据库</h3>
                <div className="sec-sub" style={{ marginBottom: 12 }}>
                  Neo4j 当前按整库图数据迁移。
                </div>
                <div className="ledger">
                  <div className="lrow" style={{ cursor: "default" }}>
                    <Database size={16} />
                    <div className="lr-main">
                      <div className="lr-t">{config.sourceDatabase}</div>
                      <div className="lr-s">节点、关系、索引和约束</div>
                    </div>
                    <Badge tone="green">整库图</Badge>
                  </div>
                </div>
                <div className="info-callout" style={{ marginTop: 12 }}>
                  Neo4j 迁移会使用 APOC stream 导出整库 Cypher，再通过 cypher-shell 导入目标库；暂不支持按标签或关系类型拆分迁移。
                </div>
              </>
            ) : (
              <>
                <div className="h-row" style={{ marginBottom: 12 }}>
                  <div>
                    <h3 className="sec-title">{isMongo ? "选择迁移集合" : "选择迁移数据表"}</h3>
                    <div className="sec-sub">
                      已选择 {config.tables.length} / {tables.length} {isMongo ? "个集合" : "张表"}
                    </div>
                  </div>
                  <span className="sp" />
                  <Button variant="secondary" className="sm" onClick={() => setConfig({ ...config, tables: tables.map((i) => i.id) })}>
                    全选
                  </Button>
                  <Button variant="secondary" className="sm" onClick={() => setConfig({ ...config, tables: [] })}>
                    清空
                  </Button>
                </div>

                <div style={{ marginBottom: 12 }}>
                  <span className="sfield" style={{ width: "100%" }}>
                    <span className="sfield-ic">
                      <Search size={14} />
                    </span>
                    <input
                      className="inp"
                      value={query}
                      onChange={(event) => setQuery(event.target.value)}
                      placeholder={isMongo ? "搜索 collection…" : "搜索 schema.table…"}
                      style={{ width: "100%" }}
                    />
                  </span>
                </div>

                <div className="ledger" style={{ maxHeight: 320, overflowY: "auto" }}>
                  {loading ? (
                    <div style={{ padding: 18, color: "var(--muted)" }}>正在加载对象信息...</div>
                  ) : filteredTables.length ? (
                    filteredTables.map((item) => {
                      const checked = config.tables.includes(item.id);
                      return (
                        <label
                          key={item.id}
                          className="lrow"
                          style={{ cursor: "pointer", padding: "9px 14px" }}
                        >
                          <input
                            type="checkbox"
                            checked={checked}
                            onChange={() => toggleTable(item.id)}
                            style={{ width: 16, height: 16, accentColor: "var(--indigo)" }}
                          />
                          <div className="lr-main">
                            <div className="lr-t mono" style={{ fontSize: 13 }}>
                              {isMongo ? item.name : item.id}
                            </div>
                          </div>
                          <span className="num" style={{ fontSize: 12.5, color: "var(--muted)" }}>
                            {isMongo ? item.schema || "Collection" : `约 ${formatNumber(item.estimatedRows)} 行 · ${formatBytes(item.sizeBytes)}`}
                          </span>
                        </label>
                      );
                    })
                  ) : (
                    <div style={{ padding: 18, color: "var(--muted)" }}>
                      {tables.length ? "没有匹配的对象" : isMongo ? "该数据库没有可迁移的集合。" : "该数据库没有可迁移的普通表。"}
                    </div>
                  )}
                </div>

                <div className="info-callout" style={{ marginTop: 12 }}>
                  {isMongo
                    ? "MongoDB 迁移当前走原生工具链。可按集合选择，迁移时使用 mongodump --collection 与 mongorestore --nsInclude。"
                    : databaseType === "MySQL"
                      ? "MySQL 当前走原生工具链。普通表可直接迁移，复杂对象请先在测试环境验证。"
                      : "当前支持普通表、分区表、序列、枚举、约束和索引；视图、函数和扩展暂不迁移。"}
                </div>
              </>
            )}
          </Card>

          <Card className="pad">
            <h3 className="sec-title">选择摘要</h3>
            <div className="sec-sub" style={{ marginBottom: 14 }}>
              {isNeo4j
                ? "Neo4j 按整库图数据迁移"
                : isMongo
                  ? "MongoDB 集合迁移不按文档大小估算"
                  : `来自 ${databaseType === "MySQL" ? "MySQL information_schema" : "PostgreSQL 系统目录"} 的估算值`}
            </div>
            <div style={{ fontSize: 26, fontWeight: 700, letterSpacing: "-.02em", marginBottom: 4 }} className="num">
              {isNeo4j ? "整库图数据" : isMongo ? `${config.tables.length} 个集合` : formatBytes(selectedBytes)}
            </div>
            <div style={{ fontSize: 12.5, color: "var(--muted)", marginBottom: 14 }}>
              {isNeo4j
                ? "实际耗时取决于节点、关系、属性体积和网络吞吐"
                : isMongo
                  ? "实际耗时取决于集合数量、文档体积和网络吞吐"
                  : "实际耗时取决于网络、数据量和索引数量"}
            </div>
            <div className="kv">
              <span className="k">{isNeo4j ? "迁移范围" : isMongo ? "已选集合" : "已选数据表"}</span>
              <span className="v num">{isNeo4j ? "整库" : isMongo ? config.tables.length : selectedTables.length}</span>
            </div>
            <div className="kv">
              <span className="k">{isNeo4j || isMongo ? "目标数据库" : "估算行数"}</span>
              <span className="v num">{isNeo4j || isMongo ? config.targetDatabase : formatNumber(selectedRows)}</span>
            </div>
            <div className="kv">
              <span className="k">源数据库</span>
              <span className="v">{config.sourceDatabase}</span>
            </div>
          </Card>
        </div>
      ) : null}

      {step === 2 ? (
        <Card className="pad">
          <h3 className="sec-title">迁移配置</h3>
          <div className="sec-sub" style={{ marginBottom: 16 }}>
            当前采用一次性全量迁移，完成后会执行校验。
          </div>

          <label className="field">
            <span>任务名称</span>
            <input
              value={config.name}
              onChange={(event) => setConfig({ ...config, name: event.target.value })}
              placeholder={`${source?.name ?? config.sourceDatabase} 到 ${target?.name ?? config.targetDatabase}迁移`}
            />
          </label>

          <div className="field">
            <span>迁移模式</span>
            <div className="radio-cards">
              <button
                type="button"
                className={`radio-card ${config.engine === "native" ? "on" : ""}`}
                onClick={() => setConfig({ ...config, engine: "native" })}
              >
                <div className="rc-h">
                  <Box size={14} />
                  {isNeo4j
                    ? "原生工具：cypher-shell + APOC"
                    : isMongo
                      ? "原生工具：mongodump / mongorestore"
                      : databaseType === "MySQL"
                        ? "原生工具：mysqldump / mysql"
                        : "原生工具：pg_dump / pg_restore"}
                </div>
                <div className="rc-d">调用数据库原生工具链导出并恢复。</div>
              </button>

              {supportsCopy ? (
                <button
                  type="button"
                  className={`radio-card ${config.engine === "copy" ? "on" : ""}`}
                  onClick={() => setConfig({ ...config, engine: "copy" })}
                >
                  <div className="rc-h">
                    <Zap size={14} /> 内置引擎：binary COPY
                  </div>
                  <div className="rc-d">经 pgx 二进制 COPY 流式传输，自动处理结构、枚举、序列与约束索引。</div>
                </button>
              ) : null}
            </div>
          </div>

          <div className="field">
            <span>同名对象冲突策略</span>
            <div className="radio-cards">
              <button
                type="button"
                className={`radio-card warn-card ${config.conflictStrategy === "backup" ? "on" : ""}`}
                onClick={() => setConfig({ ...config, conflictStrategy: "backup" })}
              >
                <div className="rc-h">{isNeo4j ? "清空目标图后迁移" : isMongo ? "重建目标库后迁移" : "备份原表后迁移"}</div>
                <div className="rc-d">
                  {isNeo4j
                    ? "导入前会清空目标图中的节点与关系。"
                    : isMongo
                      ? "MongoDB 集合迁移会重建目标库后导入所选集合。"
                      : "将目标同名表重命名为 databridge_backup 后缀后再写入新表。"}
                </div>
              </button>

              <button
                type="button"
                className={`radio-card danger-card ${config.conflictStrategy === "overwrite" ? "on" : ""}`}
                onClick={() => setConfig({ ...config, conflictStrategy: "overwrite" })}
              >
                <div className="rc-h">{isNeo4j ? "清空目标图后迁移（覆盖）" : isMongo ? "删除目标库后迁移" : "删除原表后迁移"}</div>
                <div className="rc-d">先移除目标端已有对象再迁移，原数据不可恢复。</div>
              </button>

              <button
                type="button"
                className={`radio-card warn-card ${config.conflictStrategy === "skip" ? "on" : ""}`}
                onClick={() => setConfig({ ...config, conflictStrategy: "skip" })}
              >
                <div className="rc-h">{isNeo4j ? "目标图有数据则失败" : "存在同名表则失败"}</div>
                <div className="rc-d">目标端已有数据时任务直接失败，不做任何改动。</div>
              </button>
            </div>
          </div>

          <div className="info-callout">
            {config.engine === "native" ? (
              <span>
                {isNeo4j
                  ? "本次将执行：APOC stream 导出源端 Cypher → 按冲突策略处理目标图 → cypher-shell 导入 → 校验节点与关系数量。"
                  : isMongo
                    ? "本次将执行：mongodump 导出所选集合 → 按冲突策略处理目标库 → mongorestore 导入 → 校验目标库集合可读。"
                    : databaseType === "MySQL"
                      ? "本次将执行：预处理目标同名表 → mysqldump 导出 → mysql 导入 → 逐表校验行数。"
                      : "本次将执行：psql 预处理目标同名对象 → pg_dump 导出 → pg_restore 恢复 → 逐表校验行数。"}
              </span>
            ) : (
              <span>本次将执行：创建 Schema、枚举与序列 → 创建表并 binary COPY 传输 → 创建主键、外键与索引 → 同步序列值并逐表校验行数。</span>
            )}
          </div>
        </Card>
      ) : null}

      {step === 3 && source && target ? (
        <Card style={{ overflow: "hidden" }}>
          <BridgeFlow
            sourceName={source.name}
            sourceDatabase={config.sourceDatabase}
            targetName={target.name}
            targetDatabase={config.targetDatabase}
            type={source.type}
            status="pending"
            stage="pending"
            progress={0}
            showStats={false}
          />
          <div style={{ padding: "8px 22px 18px" }}>
            <div className="kv">
              <span className="k">任务名称</span>
              <span className="v">{config.name || `${source.name} 到 ${target.name}迁移`}</span>
            </div>
            <div className="kv">
              <span className="k">迁移引擎</span>
              <span className="v">{engineLabel(config.engine, databaseType)}</span>
            </div>
            <div className="kv">
              <span className="k">{isNeo4j ? "迁移范围" : isMongo ? "迁移集合" : "迁移数据表"}</span>
              <span className="v num">
                {isNeo4j ? "整库图" : isMongo ? `${config.tables.length} 个` : `${config.tables.length} 张 · ${formatBytes(selectedBytes)}`}
              </span>
            </div>
            <div className="kv">
              <span className="k">冲突策略</span>
              <span className="v">{strategyLabel(config.conflictStrategy, databaseType)}</span>
            </div>
            <div className="kv">
              <span className="k">完成校验</span>
              <span className="v">{isNeo4j ? "节点和关系数量" : isMongo ? "目标库集合可读" : "逐表精确行数"}</span>
            </div>
          </div>
        </Card>
      ) : null}

      <div className="h-row">
        <span style={{ fontSize: 12, color: "var(--muted)" }}>
          连接凭证已加密保存，迁移任务和校验结果会写入本地 SQLite
        </span>
        <span className="sp" />
        {step > 0 ? (
          <Button variant="secondary" onClick={() => setStep(step - 1)}>
            <ArrowLeft size={15} />
            上一步
          </Button>
        ) : null}
        <Button disabled={nextDisabled} onClick={() => (step === 3 ? requestStart() : setStep(step + 1))}>
          {starting ? "正在创建..." : step === 3 ? "启动迁移" : "下一步"}
          <ArrowRight size={15} />
        </Button>
      </div>

      {startConfirmOpen ? (
        <ConfirmDialog
          title={
            config.conflictStrategy === "overwrite"
              ? isNeo4j
                ? "清空目标图后迁移"
                : isMongo
                  ? "删除目标库后迁移"
                  : "删除原表后迁移"
              : isNeo4j
                ? "图迁移确认"
                : isMongo
                  ? "集合迁移确认"
                  : "备份原表后迁移"
          }
          description={
            config.conflictStrategy === "overwrite"
              ? isNeo4j
                ? "这是高危操作，迁移前会清空目标库中的图数据。"
                : isMongo
                  ? "这是高危操作，迁移前会删除目标数据库。"
                  : "这是高危操作，迁移前会删除目标端同名对象。"
              : isNeo4j
                ? "这是中危操作，Neo4j 迁移当前会清空目标图后导入源端图数据。"
                : isMongo
                  ? "这是中危操作，Mongo 集合迁移当前会重建目标数据库后导入所选集合。"
                  : "这是中危操作，迁移前会改名保存目标端同名对象。"
          }
          tone={config.conflictStrategy === "overwrite" ? "danger" : "warning"}
          confirmText="确认启动迁移"
          submitting={starting}
          onCancel={() => setStartConfirmOpen(false)}
          onConfirm={start}
          details={[
            `目标：${target?.name ?? "目标端"} / ${config.targetDatabase}`,
            `迁移对象：${isNeo4j ? "整库图数据" : isMongo ? `${config.tables.length} 个集合` : `${config.tables.length} 张表`}`,
          ]}
        />
      ) : null}
    </div>
  );
}

function EndpointSelect({
  title,
  connectionId,
  database,
  connections,
  databases,
  onConnection,
  onDatabase,
}: {
  title: string;
  connectionId: string;
  database: string;
  connections: DatabaseConnection[];
  databases: DatabaseInfo[];
  onConnection: (value: string) => void;
  onDatabase: (value: string) => void;
}) {
  const connection = connections.find((item) => item.id === connectionId);
  const [open, setOpen] = useState(false);
  const triggerRef = useRef<HTMLButtonElement | null>(null);
  const menuRef = useRef<HTMLDivElement | null>(null);
  const [pos, setPos] = useState<{ left: number; top: number; minWidth: number }>({ left: 0, top: 0, minWidth: 220 });

  const updatePos = useCallback(() => {
    const tr = triggerRef.current;
    if (!tr) return;
    const r = tr.getBoundingClientRect();
    setPos({ left: Math.max(8, r.left), top: r.bottom + 6, minWidth: r.width });
  }, []);

  useLayoutEffect(() => {
    if (open) updatePos();
  }, [open, updatePos]);

  useEffect(() => {
    if (!open) return;
    const close = (event: MouseEvent) => {
      const target = event.target as Node;
      if (triggerRef.current?.contains(target) || menuRef.current?.contains(target)) return;
      setOpen(false);
    };
    const onEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    window.addEventListener("mousedown", close);
    window.addEventListener("keydown", onEscape);
    window.addEventListener("resize", updatePos);
    window.addEventListener("scroll", updatePos, true);
    return () => {
      window.removeEventListener("mousedown", close);
      window.removeEventListener("keydown", onEscape);
      window.removeEventListener("resize", updatePos);
      window.removeEventListener("scroll", updatePos, true);
    };
  }, [open, updatePos]);

  return (
    <div className="card pad" style={{ background: "var(--surface-2)", marginBottom: 12 }}>
      <div className="h-row" style={{ marginBottom: 10 }}>
        {connection ? <DatabaseLogo type={connection.type} name={connection.name} size="sm" /> : null}
        <strong style={{ fontSize: 13.5 }}>{title}</strong>
      </div>

      <div className="field">
        <span>{title}实例</span>
        <button
          ref={triggerRef}
          type="button"
          className="csel-trigger"
          aria-label={`${title}实例`}
          aria-haspopup="listbox"
          aria-expanded={open}
          onClick={() => setOpen((v) => !v)}
        >
          <span className="csel-val">
            {connection ? <DatabaseTypeIcon type={connection.type} /> : null}
            <span className="csel-txt">
              {connection ? `${connection.name} · ${connection.host}` : "请选择实例"}
            </span>
            {connection?.region && connection.region !== "内网" ? <RegionFlag region={connection.region} /> : null}
          </span>
          <span className="csel-chev">
            <ChevronDown size={14} />
          </span>
        </button>
      </div>

      {open
        ? createPortal(
            <div
              ref={menuRef}
              className="csel-list"
              role="listbox"
              aria-label={`${title}实例列表`}
              style={{ left: pos.left, top: pos.top, minWidth: pos.minWidth, maxWidth: "min(420px, calc(100vw - 16px))" }}
            >
              {connections.map((item) => {
                const isSel = item.id === connectionId;
                return (
                  <button
                    type="button"
                    key={item.id}
                    className="csel-opt"
                    role="option"
                    aria-selected={isSel}
                    onClick={() => {
                      onConnection(item.id);
                      setOpen(false);
                    }}
                  >
                    <DatabaseTypeIcon type={item.type} />
                    <span className="csel-txt">
                      {item.name} · {item.host}
                    </span>
                    {item.region && item.region !== "内网" ? <RegionFlag region={item.region} /> : null}
                    {isSel ? (
                      <span className="csel-check">
                        <Check size={14} />
                      </span>
                    ) : null}
                  </button>
                );
              })}
            </div>,
            document.body,
          )
        : null}

      <SelectField label={`${title}数据库`} value={database} onChange={onDatabase}>
        {databases.map((item) => (
          <option key={item.name} value={item.name}>
            {item.name} · {formatDatabaseMetric(item, connection?.type)}
          </option>
        ))}
      </SelectField>
    </div>
  );
}

function formatBytes(value: number) {
  if (!value) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  return `${(value / 1024 ** index).toFixed(index > 2 ? 1 : 0)} ${units[index]}`;
}

function formatNumber(value: number) {
  return new Intl.NumberFormat("zh-CN", { notation: value > 999999 ? "compact" : "standard", maximumFractionDigits: 1 }).format(value);
}

function formatDatabaseMetric(database: DatabaseInfo, type?: DatabaseConnection["type"]) {
  if (type === "Neo4j") {
    return `${formatNumber(database.nodes ?? 0)} 节点 · ${formatNumber(database.relationships ?? 0)} 关系`;
  }
  return formatBytes(database.sizeBytes);
}

function strategyLabel(value: MigrationConfig["conflictStrategy"], databaseType?: DatabaseConnection["type"]) {
  if (databaseType === "Neo4j") return value === "skip" ? "目标图有数据则失败" : "清空目标图后迁移";
  if (databaseType === "MongoDB") return value === "skip" ? "目标库有集合则失败" : value === "overwrite" ? "删除目标库后迁移" : "重建目标库后迁移";
  return value === "backup" ? "备份原表后迁移" : value === "overwrite" ? "删除原表后迁移" : "同名表时报错";
}

function engineLabel(value: MigrationConfig["engine"], databaseType?: DatabaseConnection["type"]) {
  if (value === "native") {
    if (databaseType === "Neo4j") return "cypher-shell + APOC";
    if (databaseType === "MongoDB") return "mongodump / mongorestore";
    if (databaseType === "MySQL") return "mysqldump / mysql";
    return "pg_dump / pg_restore";
  }
  return "内置 binary COPY";
}
