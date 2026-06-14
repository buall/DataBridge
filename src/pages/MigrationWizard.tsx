import { ArrowLeft, ArrowRight, Check, Database, Search } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { api } from "../api";
import type { DatabaseConnection, DatabaseInfo, MigrationConfig, TableInfo } from "../types";
import { Button, Card, DatabaseLogo, SelectField } from "../components/UI";

const steps = ["连接与数据库", "选择数据表", "迁移配置", "确认执行"];

export function MigrationWizard({
  connections,
  onStart,
}: {
  connections: DatabaseConnection[];
  onStart: (config: MigrationConfig) => Promise<void>;
}) {
  const [step, setStep] = useState(0);
  const [query, setQuery] = useState("");
  const [sourceDatabases, setSourceDatabases] = useState<DatabaseInfo[]>([]);
  const [targetDatabases, setTargetDatabases] = useState<DatabaseInfo[]>([]);
  const [tables, setTables] = useState<TableInfo[]>([]);
  const [loading, setLoading] = useState(false);
  const [starting, setStarting] = useState(false);
  const [error, setError] = useState("");
  const [config, setConfig] = useState<MigrationConfig>({
    name: "",
    sourceId: connections[0]?.id ?? "",
    sourceDatabase: "",
    targetId: connections[1]?.id ?? "",
    targetDatabase: "",
    tables: [],
    conflictStrategy: "backup",
  });

  useEffect(() => {
    if (!config.sourceId && connections[0]) {
      setConfig((current) => ({ ...current, sourceId: connections[0].id, targetId: connections[1]?.id ?? "" }));
    }
  }, [connections, config.sourceId]);

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
  const selectedTables = tables.filter((item) => config.tables.includes(item.id));
  const selectedBytes = selectedTables.reduce((sum, item) => sum + item.sizeBytes, 0);
  const selectedRows = selectedTables.reduce((sum, item) => sum + item.estimatedRows, 0);
  const filteredTables = useMemo(
    () => tables.filter((item) => item.id.toLowerCase().includes(query.toLowerCase())),
    [tables, query],
  );

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
        name: config.name || `${config.sourceDatabase} → ${config.targetDatabase}`,
      });
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "创建迁移任务失败");
      setStarting(false);
    }
  }

  if (!connections.length) {
    return <div className="empty-state"><div>PG</div><h2>请先添加 PostgreSQL 连接</h2><p>至少需要两个可访问的实例连接，或同一实例上的两个连接配置。</p></div>;
  }

  return (
    <div className="wizard-page">
      <div className="stepper">
        {steps.map((label, index) => <div className={`step ${index === step ? "current" : ""} ${index < step ? "done" : ""}`} key={label}><span>{index < step ? <Check size={14} /> : index + 1}</span><strong>{label}</strong>{index < steps.length - 1 && <i />}</div>)}
      </div>
      {error && <div className="error-banner">{error}</div>}

      {step === 0 && (
        <Card className="wizard-card connection-step">
          <div className="card-heading"><h3>选择源端与目标端数据库</h3><p>目标数据库必须已经存在；迁移任务会在其中创建所选表的 Schema 和数据。</p></div>
          <div className="endpoint-grid">
            <EndpointSelect title="源端" connectionId={config.sourceId} database={config.sourceDatabase} connections={connections} databases={sourceDatabases} onConnection={(sourceId) => setConfig({ ...config, sourceId, sourceDatabase: "" })} onDatabase={(sourceDatabase) => setConfig({ ...config, sourceDatabase })} />
            <div className="route-arrow"><span><ArrowRight /></span></div>
            <EndpointSelect title="目标端" connectionId={config.targetId} database={config.targetDatabase} connections={connections} databases={targetDatabases} onConnection={(targetId) => setConfig({ ...config, targetId, targetDatabase: "" })} onDatabase={(targetDatabase) => setConfig({ ...config, targetDatabase })} />
          </div>
          {loading && <p className="loading-copy">正在发现数据库...</p>}
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
              {loading && <div className="loading-copy">正在读取表元数据...</div>}
              {!loading && filteredTables.map((item) => {
                const checked = config.tables.includes(item.id);
                return <button className="database-row" key={item.id} onClick={() => toggleTable(item.id)}><span className={`checkbox ${checked ? "checked" : ""}`}>{checked && <Check size={13} />}</span><Database size={17} /><strong>{item.id}</strong><span>{formatBytes(item.sizeBytes)}</span><small>约 {formatNumber(item.estimatedRows)} 行</small><span /></button>;
              })}
              {!loading && !tables.length && <div className="loading-copy">该数据库没有可迁移的普通表。</div>}
            </div>
            <div className="info-banner">MVP 支持普通表、分区表、序列、枚举、约束与索引；视图、函数和扩展暂不迁移。</div>
          </Card>
          <Card className="summary-panel">
            <div className="card-heading"><h3>选择摘要</h3><p>来自 PostgreSQL 系统目录的估算值</p></div>
            <div className="summary-stat"><span>预计迁移数据量</span><strong>{formatBytes(selectedBytes)}</strong><small>实际耗时取决于网络与索引数量</small></div>
            <dl><div><dt>数据表</dt><dd>{selectedTables.length}</dd></div><div><dt>估算行数</dt><dd>{formatNumber(selectedRows)}</dd></div><div><dt>源数据库</dt><dd>{config.sourceDatabase}</dd></div></dl>
          </Card>
        </div>
      )}

      {step === 2 && (
        <Card className="wizard-card config-step">
          <div className="card-heading"><h3>迁移配置</h3><p>当前版本执行一次性全量迁移，并在完成后逐表校验行数。</p></div>
          <div className="config-grid">
            <label className="field"><span>任务名称</span><input value={config.name} onChange={(event) => setConfig({ ...config, name: event.target.value })} placeholder={`${config.sourceDatabase} → ${config.targetDatabase}`} /></label>
            <SelectField label="迁移模式" value="full" onChange={() => {}}><option value="full">全量结构 + 数据</option></SelectField>
            <SelectField label="同名对象冲突" value={config.conflictStrategy} onChange={(conflictStrategy) => setConfig({ ...config, conflictStrategy: conflictStrategy as MigrationConfig["conflictStrategy"] })}><option value="backup">备份原表后迁移</option><option value="overwrite">删除原表后迁移</option><option value="skip">存在同名表则失败</option></SelectField>
          </div>
          <div className="scope-note"><strong>本次迁移会执行</strong><span>创建 Schema、枚举与序列</span><span>创建表并使用 binary COPY 传输数据</span><span>创建主键、唯一约束、外键和索引</span><span>同步序列值并校验逐表行数</span></div>
        </Card>
      )}

      {step === 3 && (
        <Card className="wizard-card confirm-step">
          <div className="confirm-icon"><Check /></div><div className="card-heading centered"><h3>确认执行真实迁移</h3><p>任务启动后会直接写入目标数据库。请确认目标库和冲突策略正确。</p></div>
          <div className="route-summary"><Endpoint connection={source} database={config.sourceDatabase} label="源端" /><div className="route-line"><ArrowRight /></div><Endpoint connection={target} database={config.targetDatabase} label="目标端" /></div>
          <div className="confirmation-grid"><div><span>迁移数据表</span><strong>{config.tables.length} 张</strong></div><div><span>估算数据量</span><strong>{formatBytes(selectedBytes)}</strong></div><div><span>冲突策略</span><strong>{strategyLabel(config.conflictStrategy)}</strong></div><div><span>完成校验</span><strong>逐表精确行数</strong></div></div>
        </Card>
      )}

      <div className="wizard-actions">
        <span>连接凭证仅保存在当前 API 进程内存中</span>
        <div>{step > 0 && <Button variant="secondary" onClick={() => setStep(step - 1)}><ArrowLeft size={16} />上一步</Button>}<Button disabled={loading || starting || (step === 0 && (!config.sourceDatabase || !config.targetDatabase || (config.sourceId === config.targetId && config.sourceDatabase === config.targetDatabase))) || (step === 1 && !config.tables.length)} onClick={() => step === 3 ? start() : setStep(step + 1)}>{starting ? "正在创建任务..." : step === 3 ? "启动真实迁移" : "下一步"}<ArrowRight size={16} /></Button></div>
      </div>
    </div>
  );
}

function EndpointSelect({ title, connectionId, database, connections, databases, onConnection, onDatabase }: { title: string; connectionId: string; database: string; connections: DatabaseConnection[]; databases: DatabaseInfo[]; onConnection: (value: string) => void; onDatabase: (value: string) => void }) {
  const connection = connections.find((item) => item.id === connectionId);
  return <div className="endpoint-select"><span>{title}</span><div className="endpoint-control stacked">{connection && <DatabaseLogo type={connection.type} />}<div><select aria-label={`${title}实例`} value={connectionId} onChange={(event) => onConnection(event.target.value)}>{connections.map((item) => <option key={item.id} value={item.id}>{item.name} · {item.host}</option>)}</select><select aria-label={`${title}数据库`} value={database} onChange={(event) => onDatabase(event.target.value)}>{databases.map((item) => <option key={item.name} value={item.name}>{item.name} · {formatBytes(item.sizeBytes)}</option>)}</select></div></div></div>;
}

function Endpoint({ connection, database, label }: { connection?: DatabaseConnection; database: string; label: string }) {
  return <div className="endpoint-summary">{connection && <DatabaseLogo type={connection.type} />}<div><span>{label}</span><strong>{connection?.name} / {database}</strong><small>{connection?.host}:{connection?.port}</small></div></div>;
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
