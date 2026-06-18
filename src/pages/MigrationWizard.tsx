import { ArrowLeft, ArrowRight, Check, ChevronDown, Database, Search } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { api } from "../api";
import type { DatabaseConnection, DatabaseInfo, MigrationConfig, TableInfo } from "../types";
import { Button, Card, ConfirmDialog, DatabaseLogo, DatabaseTypeIcon, RegionFlag, SelectField } from "../components/UI";

const steps = ["连接与数据库", "选择数据表", "迁移配置", "确认执行"];

export function MigrationWizard({
  connections,
  initialSourceId,
  onStart,
}: {
  connections: DatabaseConnection[];
  initialSourceId?: string;
  onStart: (config: MigrationConfig) => Promise<void>;
}) {
  const initialSource = initialSourceId && connections.some((item) => item.id === initialSourceId) ? initialSourceId : (connections[0]?.id ?? "");
  const initialTarget = connections.find((item) => item.id !== initialSource)?.id ?? connections[1]?.id ?? "";
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
    const nextSource = initialSourceId && connections.some((item) => item.id === initialSourceId) ? initialSourceId : (connections[0]?.id ?? "");
    const nextTarget = connections.find((item) => item.id !== nextSource)?.id ?? connections.find((item) => item.id !== config.sourceId)?.id ?? "";
    setConfig((current) => {
      const sourceId = connections.some((item) => item.id === current.sourceId) ? current.sourceId : nextSource;
      const targetCandidate = connections.some((item) => item.id === current.targetId) && current.targetId !== sourceId
        ? current.targetId
        : (connections.find((item) => item.id !== sourceId)?.id ?? "");
      if (sourceId === current.sourceId && targetCandidate === current.targetId) {
        return current;
      }
      return { ...current, sourceId, targetId: targetCandidate };
    });
  }, [connections, config.sourceId, initialSourceId]);

  useEffect(() => {
    if (!config.sourceId) return;
    setLoading(true);
    setError("");
    api.databases(config.sourceId)
      .then((items) => {
        setSourceDatabases(items);
        setConfig((current) => ({
          ...current,
          sourceDatabase: items.some((item) => item.name === current.sourceDatabase) ? current.sourceDatabase : (items[0]?.name ?? ""),
        }));
      })
      .catch((reason) => setError(reason.message))
      .finally(() => setLoading(false));
  }, [config.sourceId]);

  useEffect(() => {
    if (!config.targetId) return;
    setLoading(true);
    setError("");
    api.databases(config.targetId)
      .then((items) => {
        setTargetDatabases(items);
        setConfig((current) => ({
          ...current,
          targetDatabase: items.some((item) => item.name === current.targetDatabase) ? current.targetDatabase : (items[0]?.name ?? ""),
        }));
      })
      .catch((reason) => setError(reason.message))
      .finally(() => setLoading(false));
  }, [config.targetId]);

  useEffect(() => {
    if (step !== 1 || !config.sourceId || !config.sourceDatabase) return;
    setLoading(true);
    setError("");
    api.tables(config.sourceId, config.sourceDatabase)
      .then((items) => {
        setTables(items);
        setConfig((current) => ({ ...current, tables: current.tables.filter((id) => items.some((item) => item.id === id)) }));
      })
      .catch((reason) => setError(reason.message))
      .finally(() => setLoading(false));
  }, [step, config.sourceId, config.sourceDatabase]);

  const source = connections.find((item) => item.id === config.sourceId);
  const target = connections.find((item) => item.id === config.targetId);
  const databaseType = source?.type && target?.type && source.type === target.type ? source.type : source?.type;
  const supportsCopy = databaseType === "PostgreSQL";
  const selectedTables = tables.filter((item) => config.tables.includes(item.id));
  const selectedBytes = selectedTables.reduce((sum, item) => sum + item.sizeBytes, 0);
  const selectedRows = selectedTables.reduce((sum, item) => sum + item.estimatedRows, 0);
  const filteredTables = useMemo(
    () => tables.filter((item) => item.id.toLowerCase().includes(query.toLowerCase())),
    [tables, query],
  );

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
      await onStart({
        ...config,
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

  if (!connections.length) {
    return <div className="empty-state"><div>DB</div><h2>暂无可用连接</h2><p>请先添加至少两个可访问的同类型数据库连接，再创建迁移任务。</p></div>;
  }

  return (
    <div className="wizard-page">
      <div className="stepper">
        {steps.map((label, index) => <div className={`step ${index === step ? "current" : ""} ${index < step ? "done" : ""}`} key={label}><span>{index < step ? <Check size={14} /> : index + 1}</span><strong>{label}</strong>{index < steps.length - 1 && <i />}</div>)}
      </div>
      {error && <div className="error-banner">{error}</div>}

      {step === 0 && (
        <Card className="wizard-card connection-step">
          <div className="card-heading"><h3>选择源端与目标端数据库</h3><p>目标数据库需提前创建，迁移任务会在其中写入所选对象和数据。</p></div>
          <div className="endpoint-grid">
            <EndpointSelect title="源端" connectionId={config.sourceId} database={config.sourceDatabase} connections={connections} databases={sourceDatabases} onConnection={(sourceId) => setConfig({ ...config, sourceId, sourceDatabase: "" })} onDatabase={(sourceDatabase) => setConfig({ ...config, sourceDatabase })} />
            <div className="route-arrow"><span><ArrowRight /></span></div>
            <EndpointSelect title="目标端" connectionId={config.targetId} database={config.targetDatabase} connections={connections} databases={targetDatabases} onConnection={(targetId) => setConfig({ ...config, targetId, targetDatabase: "" })} onDatabase={(targetDatabase) => setConfig({ ...config, targetDatabase })} />
          </div>
          {loading && <p className="loading-copy">正在加载数据库...</p>}
          {config.sourceId === config.targetId && config.sourceDatabase === config.targetDatabase && <p className="form-error">源端和目标端不能是同一个数据库。</p>}
        </Card>
      )}

      {step === 1 && (
        <div className="wizard-columns">
          <Card className="object-panel">
            <div className="card-heading"><h3>选择迁移数据表</h3><p>已选择 {config.tables.length} / {tables.length} 张表</p></div>
            <label className="search-box"><Search size={17} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索 schema.table" /></label>
            <div className="table-selection-actions"><button onClick={() => setConfig({ ...config, tables: tables.map((item) => item.id) })}>全选</button><button onClick={() => setConfig({ ...config, tables: [] })}>清空</button></div>
            <div className="database-list">
              {loading && <div className="loading-copy">正在加载表信息...</div>}
              {!loading && filteredTables.map((item) => {
                const checked = config.tables.includes(item.id);
                return <button className="database-row" key={item.id} onClick={() => toggleTable(item.id)}><span className={`checkbox ${checked ? "checked" : ""}`}>{checked && <Check size={13} />}</span><Database size={17} /><strong>{item.id}</strong><span>{formatBytes(item.sizeBytes)}</span><small>约 {formatNumber(item.estimatedRows)} 行</small><span /></button>;
              })}
              {!loading && !tables.length && <div className="loading-copy">该数据库没有可迁移的普通表。</div>}
            </div>
            <div className="info-banner">{databaseType === "MySQL" ? "MySQL 当前走原生工具链。普通表可直接迁移，复杂对象请先在测试环境验证。" : "当前支持普通表、分区表、序列、枚举、约束和索引；视图、函数和扩展暂不迁移。"}</div>
          </Card>
          <Card className="summary-panel">
            <div className="card-heading"><h3>选择摘要</h3><p>来自 {databaseType === "MySQL" ? "MySQL information_schema" : "PostgreSQL 系统目录"} 的估算值</p></div>
            <div className="summary-stat"><span>预计迁移数据量</span><strong>{formatBytes(selectedBytes)}</strong><small>实际耗时取决于网络、数据量和索引数量</small></div>
            <dl><div><dt>数据表</dt><dd>{selectedTables.length}</dd></div><div><dt>估算行数</dt><dd>{formatNumber(selectedRows)}</dd></div><div><dt>源数据库</dt><dd>{config.sourceDatabase}</dd></div></dl>
          </Card>
        </div>
      )}

      {step === 2 && (
        <Card className="wizard-card config-step">
          <div className="card-heading"><h3>迁移配置</h3><p>当前采用一次性全量迁移，完成后会逐表校验行数。</p></div>
          <div className="config-grid">
            <label className="field"><span>任务名称</span><input value={config.name} onChange={(event) => setConfig({ ...config, name: event.target.value })} placeholder={`${config.sourceDatabase} → ${config.targetDatabase}`} /></label>
            <SelectField label="迁移模式" value={config.engine} onChange={(engine) => setConfig({ ...config, engine: engine as MigrationConfig["engine"] })}><option value="native">{databaseType === "MySQL" ? "原生工具：mysqldump / mysql" : "原生工具：pg_dump / pg_restore"}</option>{supportsCopy && <option value="copy">内置引擎：binary COPY</option>}</SelectField>
            <SelectField label="同名对象冲突" value={config.conflictStrategy} onChange={(conflictStrategy) => setConfig({ ...config, conflictStrategy: conflictStrategy as MigrationConfig["conflictStrategy"] })}><option value="backup">备份原表后迁移</option><option value="overwrite">删除原表后迁移</option><option value="skip">存在同名表则失败</option></SelectField>
          </div>
          {config.engine === "native" ? (
            <div className="scope-note"><strong>本次迁移会执行</strong>{databaseType === "MySQL" ? <><span>预处理目标端同名表</span><span>全选时整库 mysqldump，部分选择时按表导出</span><span>使用 mysql 导入结构和数据</span><span>完成后逐表校验行数</span></> : <><span>使用 psql 预处理目标同名对象</span><span>全选时整库 pg_dump，部分选择时按表导出</span><span>使用 pg_restore 恢复结构、数据、索引和约束</span><span>完成后逐表校验行数</span></>}</div>
          ) : (
            <div className="scope-note"><strong>本次迁移会执行</strong><span>创建 Schema、枚举与序列</span><span>创建表并使用 binary COPY 传输数据</span><span>创建主键、唯一约束、外键和索引</span><span>同步序列值并逐表校验行数</span></div>
          )}
        </Card>
      )}

      {step === 3 && (
        <Card className="wizard-card confirm-step">
          <div className="confirm-icon"><Check /></div><div className="card-heading centered"><h3>确认执行迁移</h3><p>任务启动后会直接写入目标数据库，请确认目标库和冲突策略。</p></div>
          <div className="route-summary"><Endpoint connection={source} database={config.sourceDatabase} label="源端" /><div className="route-line"><ArrowRight /></div><Endpoint connection={target} database={config.targetDatabase} label="目标端" /></div>
          <div className="confirmation-grid"><div><span>迁移引擎</span><strong>{engineLabel(config.engine)}</strong></div><div><span>迁移数据表</span><strong>{config.tables.length} 张</strong></div><div><span>估算数据量</span><strong>{formatBytes(selectedBytes)}</strong></div><div><span>冲突策略</span><strong>{strategyLabel(config.conflictStrategy)}</strong></div><div><span>完成校验</span><strong>逐表精确行数</strong></div></div>
        </Card>
      )}

      <div className="wizard-actions">
        <span>连接凭证已加密保存，迁移任务和校验结果会写入本地 SQLite</span>
        <div>{step > 0 && <Button variant="secondary" onClick={() => setStep(step - 1)}><ArrowLeft size={16} />上一步</Button>}<Button disabled={loading || starting || (step === 0 && (!config.sourceDatabase || !config.targetDatabase || (config.sourceId === config.targetId && config.sourceDatabase === config.targetDatabase))) || (step === 1 && !config.tables.length)} onClick={() => step === 3 ? requestStart() : setStep(step + 1)}>{starting ? "正在创建..." : step === 3 ? "启动迁移" : "下一步"}<ArrowRight size={16} /></Button></div>
      </div>
      {startConfirmOpen && <ConfirmDialog title={config.conflictStrategy === "overwrite" ? "删除原表后迁移" : "备份原表后迁移"} description={config.conflictStrategy === "overwrite" ? "这是高危操作，迁移前会删除目标端同名对象。" : "这是中危操作，迁移前会改名保存目标端同名对象。"} tone={config.conflictStrategy === "overwrite" ? "danger" : "warning"} confirmText="确认启动迁移" submitting={starting} onCancel={() => setStartConfirmOpen(false)} onConfirm={start} details={[`目标：${target?.name ?? "目标端"} / ${config.targetDatabase}`, `迁移对象：${config.tables.length} 张表`, config.conflictStrategy === "overwrite" ? "目标端同名表和相关对象会被删除后重新写入。" : "目标端同名表会被改名备份，仍会改变目标库对象结构。"]} />}
    </div>
  );
}

function EndpointSelect({ title, connectionId, database, connections, databases, onConnection, onDatabase }: { title: string; connectionId: string; database: string; connections: DatabaseConnection[]; databases: DatabaseInfo[]; onConnection: (value: string) => void; onDatabase: (value: string) => void }) {
  const connection = connections.find((item) => item.id === connectionId);
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!open) return;
    const handlePointerDown = (event: MouseEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) {
        setOpen(false);
      }
    };
    const handleEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setOpen(false);
      }
    };
    window.addEventListener("mousedown", handlePointerDown);
    window.addEventListener("keydown", handleEscape);
    return () => {
      window.removeEventListener("mousedown", handlePointerDown);
      window.removeEventListener("keydown", handleEscape);
    };
  }, [open]);

  return <div className="endpoint-select"><span>{title}</span><div className="endpoint-control stacked">{connection && <DatabaseLogo type={connection.type} name={connection.name} />}<div ref={rootRef}><div className="endpoint-instance-picker"><button type="button" className="endpoint-instance-trigger" aria-label={`${title}实例`} aria-haspopup="listbox" aria-expanded={open} onClick={() => setOpen((value) => !value)}><span className="endpoint-instance-trigger-content">{connection && <DatabaseTypeIcon type={connection.type} />}{connection?.name} · {connection?.host}{connection?.region && connection.region !== "内网" && <RegionFlag region={connection.region} />}</span><ChevronDown size={16} /></button>{open && <div className="endpoint-instance-menu" role="listbox" aria-label={`${title}实例列表`}>{connections.map((item) => <button type="button" key={item.id} className={`endpoint-instance-option ${item.id === connectionId ? "active" : ""}`} onClick={() => { onConnection(item.id); setOpen(false); }}><div><strong><DatabaseTypeIcon type={item.type} />{item.name} · {item.host}{item.region && item.region !== "内网" && <RegionFlag region={item.region} />}</strong></div></button>)}</div>}</div><SelectField label={`${title}数据库`} value={database} onChange={onDatabase}>{databases.map((item) => <option key={item.name} value={item.name}>{item.name} · {formatBytes(item.sizeBytes)}</option>)}</SelectField></div></div></div>;
}

function Endpoint({ connection, database, label }: { connection?: DatabaseConnection; database: string; label: string }) {
  return <div className="endpoint-summary">{connection && <DatabaseLogo type={connection.type} name={connection.name} />}<div><span>{label}</span><strong>{connection?.name} / {database}</strong><small>{connection?.host}:{connection?.port}</small></div></div>;
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

function strategyLabel(value: MigrationConfig["conflictStrategy"]) {
  return value === "backup" ? "备份原表后迁移" : value === "overwrite" ? "删除原表后迁移" : "同名表时报错";
}

function engineLabel(value: MigrationConfig["engine"]) {
  if (value === "native") {
    return "原生工具";
  }
  return "内置 binary COPY";
}
