import { AlertTriangle, Check, Copy, Database, Edit3, FileSpreadsheet, Info, MoreHorizontal, Play, Plus, RefreshCw, Search, Trash2, WandSparkles, X } from "lucide-react";
import { useEffect, useState } from "react";
import { api } from "../api";
import type { BatchImportResult, ConnectionInput, ConnectionTLSConfig, DatabaseConnection, DatabaseInfo, DatabaseType, DuplicateConnectionInput, SeedDataResult } from "../types";
import { Badge, Button, Card, ConfirmDialog, DatabaseLogo, DatabaseTypeIcon, RegionFlag, SelectField } from "../components/UI";
import { BatchImportModal } from "../components/BatchImportModal";

type ConnectionFormState = {
  type: DatabaseType;
  name: string;
  note: string;
  host: string;
  port: string;
  username: string;
  password: string;
  sslMode: string;
  tlsConfig: ConnectionTLSConfig;
};

type EditConnectionFormState = Omit<ConnectionFormState, "type">;
type DuplicateConnectionFormState = Omit<ConnectionFormState, "type" | "password" | "tlsConfig"> & {
  password: string;
  tlsConfig: ConnectionTLSConfig;
};

type ConnectionFormErrors = Partial<Record<"type" | "name" | "host" | "port" | "username" | "password" | "sslMode", string>>;

export function Connections({
  connections,
  onAdd,
  onUpdate,
  onDelete,
  onCreateMigration,
  onBatchImported,
}: {
  connections: DatabaseConnection[];
  onAdd: (connection: DatabaseConnection) => void;
  onUpdate: (connection: DatabaseConnection) => void;
  onDelete: (connectionId: string) => void;
  onCreateMigration: (sourceId?: string) => void;
  onBatchImported: (result: BatchImportResult) => void;
}) {
  const supportsMigration = (type: DatabaseType) => type === "PostgreSQL" || type === "MySQL" || type === "MongoDB" || type === "Neo4j";
  const supportsDirectDatabaseCreate = (type: DatabaseType) => type === "PostgreSQL" || type === "MySQL" || type === "MongoDB" || type === "Neo4j";
  const supportsSeedData = (type: DatabaseType) => type === "PostgreSQL" || type === "MySQL" || type === "MongoDB" || type === "Neo4j";
  const [showForm, setShowForm] = useState(false);
  const [showBatchImport, setShowBatchImport] = useState(false);
  const [editModal, setEditModal] = useState<{ connection: DatabaseConnection; form: EditConnectionFormState; submitting: boolean; error: string } | null>(null);
  const [duplicateModal, setDuplicateModal] = useState<{ connection: DatabaseConnection; form: DuplicateConnectionFormState; submitting: boolean; error: string } | null>(null);
  const [testing, setTesting] = useState<string | null>(null);
  const [openMenu, setOpenMenu] = useState<string | null>(null);
  const [databaseModal, setDatabaseModal] = useState<{ connection: DatabaseConnection; databases: DatabaseInfo[]; loading: boolean; error: string } | null>(null);
  const [createDatabaseModal, setCreateDatabaseModal] = useState<{ connection: DatabaseConnection; name: string; submitting: boolean; error: string } | null>(null);
  const [seedDataModal, setSeedDataModal] = useState<{ connection: DatabaseConnection; database: string; rows: string; mode: "append" | "replace"; template: "users" | "orders" | "products"; databases: DatabaseInfo[]; loading: boolean; submitting: boolean; error: string; result: SeedDataResult | null } | null>(null);
  const [seedDataConfirmOpen, setSeedDataConfirmOpen] = useState(false);
  const [deleteModal, setDeleteModal] = useState<{ connection: DatabaseConnection; submitting: boolean; error: string } | null>(null);
  const [copied, setCopied] = useState<string | null>(null);
  const [manualCopyValue, setManualCopyValue] = useState("");
  const [actionError, setActionError] = useState("");
  const [query, setQuery] = useState("");
  const [form, setForm] = useState<ConnectionFormState>(emptyConnectionForm("PostgreSQL"));
  const [nameCustomized, setNameCustomized] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState("");
  const [formErrors, setFormErrors] = useState<ConnectionFormErrors>({});
  const filtered = connections.filter((item) => `${item.name} ${item.note ?? ""} ${item.host} ${item.type} ${item.region ?? ""}`.toLowerCase().includes(query.toLowerCase()));
  const formSecurityConfig = connectionSecurityConfig(form.type);
  const formCompatibilityHint = connectionCompatibilityHint(form.type);

  useEffect(() => {
    if (nameCustomized) return;
    const generatedName = suggestConnectionName(form.type, form.host, form.port);
    if (form.name === generatedName) return;
    setForm((current) => ({ ...current, name: generatedName }));
  }, [form.type, form.host, form.port, form.name, nameCustomized]);

  function updateFormField<K extends keyof ConnectionFormState>(key: K, value: ConnectionFormState[K]) {
    setForm((current) => ({ ...current, [key]: value }));
  }

  function validateSingleField(key: keyof ConnectionFormErrors, nextForm: ConnectionFormState) {
    const nextErrors = validateConnectionForm(nextForm);
    setFormErrors((current) => ({ ...current, [key]: nextErrors[key] }));
  }

  function fallbackCopyText(value: string) {
    const textarea = document.createElement("textarea");
    textarea.value = value;
    textarea.readOnly = true;
    textarea.style.position = "fixed";
    textarea.style.left = "-9999px";
    textarea.style.top = "0";
    document.body.appendChild(textarea);
    textarea.select();
    textarea.setSelectionRange(0, value.length);
    const copied = typeof document.execCommand === "function" && document.execCommand("copy");
    document.body.removeChild(textarea);
    return copied;
  }

  useEffect(() => {
    if (!openMenu) return;
    const close = () => setOpenMenu(null);
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") close();
    };
    window.addEventListener("click", close);
    window.addEventListener("keydown", closeOnEscape);
    return () => {
      window.removeEventListener("click", close);
      window.removeEventListener("keydown", closeOnEscape);
    };
  }, [openMenu]);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    const validationErrors = validateConnectionForm(form);
    setFormErrors(validationErrors);
    if (Object.keys(validationErrors).length > 0) {
      setFormError("请先修正表单中的必填项和格式问题。");
      return;
    }
    setSubmitting(true);
    setFormError("");
    try {
      const input: ConnectionInput = { ...form, port: Number(form.port) };
      const connection = await api.createConnection(input);
      onAdd(connection);
      closeConnectionForm();
    } catch (error) {
      setFormError(error instanceof Error ? error.message : "连接失败");
    } finally {
      setSubmitting(false);
    }
  }

  async function testConnection(id: string) {
    setTesting(id);
    setActionError("");
    try {
      const connection = await api.refreshConnection(id);
      onUpdate(connection);
    } catch (error) {
      const connection = connections.find((item) => item.id === id);
      if (connection) {
        onUpdate({ ...connection, status: "warning", latency: 0 });
      }
      setActionError(error instanceof Error ? error.message : "连接测试失败");
    } finally {
      setTesting(null);
    }
  }

  async function showDatabases(connection: DatabaseConnection) {
    setOpenMenu(null);
    await loadDatabases(connection);
  }

  async function loadDatabases(connection: DatabaseConnection) {
    setDatabaseModal({ connection, databases: [], loading: true, error: "" });
    try {
      const databases = await api.databases(connection.id);
      setDatabaseModal({ connection, databases, loading: false, error: "" });
    } catch (error) {
      setDatabaseModal({ connection, databases: [], loading: false, error: error instanceof Error ? error.message : "数据库读取失败" });
    }
  }

  function openCreateDatabase(connection: DatabaseConnection) {
    setOpenMenu(null);
    setCreateDatabaseModal({ connection, name: "", submitting: false, error: "" });
  }

  async function openSeedData(connection: DatabaseConnection) {
    setOpenMenu(null);
    setSeedDataModal({ connection, database: "", rows: "100", mode: "append", template: "users", databases: [], loading: true, submitting: false, error: "", result: null });
    try {
      const databases = await api.databases(connection.id);
      setSeedDataModal({ connection, database: databases[0]?.name ?? "", rows: "100", mode: "append", template: "users", databases, loading: false, submitting: false, error: "", result: null });
    } catch (error) {
      setSeedDataModal({ connection, database: "", rows: "100", mode: "append", template: "users", databases: [], loading: false, submitting: false, error: error instanceof Error ? error.message : "加载数据库失败", result: null });
    }
  }

  function openEditConnection(connection: DatabaseConnection) {
    setOpenMenu(null);
    setEditModal({
      connection,
      form: {
        name: connection.name,
        note: connection.note ?? "",
        host: connection.host,
        port: String(connection.port),
        username: connection.username,
        password: "",
        sslMode: connection.sslMode,
        tlsConfig: emptyTLSConfig(),
      },
      submitting: false,
      error: "",
    });
  }

  function openNewConnection() {
    setForm(emptyConnectionForm("PostgreSQL"));
    setNameCustomized(false);
    setFormError("");
    setFormErrors({});
    setShowForm(true);
  }

  function openCopyConnection(connection: DatabaseConnection) {
    setOpenMenu(null);
    setDuplicateModal({
      connection,
      form: {
        name: duplicateConnectionName(connection.name, connections.map((item) => item.name)),
        note: connection.note ?? "",
        host: connection.host,
        port: String(connection.port),
        username: connection.username,
        password: "",
        sslMode: connection.sslMode,
        tlsConfig: emptyTLSConfig(),
      },
      submitting: false,
      error: "",
    });
  }

  async function submitDuplicateConnection(event: React.FormEvent) {
    event.preventDefault();
    if (!duplicateModal) return;
    setDuplicateModal({ ...duplicateModal, submitting: true, error: "" });
    try {
      const input: DuplicateConnectionInput = {
        ...duplicateModal.form,
        port: Number(duplicateModal.form.port),
      };
      const connection = await api.duplicateConnection(duplicateModal.connection.id, input);
      onAdd(connection);
      setDuplicateModal(null);
    } catch (error) {
      setDuplicateModal({ ...duplicateModal, submitting: false, error: error instanceof Error ? error.message : "复制连接失败" });
    }
  }

  function updateDuplicateForm(patch: Partial<DuplicateConnectionFormState>) {
    if (!duplicateModal) return;
    setDuplicateModal({ ...duplicateModal, form: { ...duplicateModal.form, ...patch }, error: "" });
  }

  function closeConnectionForm() {
    setShowForm(false);
    setForm(emptyConnectionForm("PostgreSQL"));
    setNameCustomized(false);
    setFormError("");
    setFormErrors({});
  }

  function openDeleteConnection(connection: DatabaseConnection) {
    setOpenMenu(null);
    setDeleteModal({ connection, submitting: false, error: "" });
  }

  async function submitEditConnection(event: React.FormEvent) {
    event.preventDefault();
    if (!editModal) return;
    setEditModal({ ...editModal, submitting: true, error: "" });
    try {
      const updated = await api.updateConnection(editModal.connection.id, {
        type: editModal.connection.type,
        ...editModal.form,
        port: Number(editModal.form.port),
      });
      onUpdate(updated);
      setEditModal(null);
    } catch (error) {
      setEditModal({ ...editModal, submitting: false, error: error instanceof Error ? error.message : "更新连接失败" });
    }
  }

  async function submitCreateDatabase(event: React.FormEvent) {
    event.preventDefault();
    if (!createDatabaseModal) return;
    const name = createDatabaseModal.name.trim();
    setCreateDatabaseModal({ ...createDatabaseModal, name, submitting: true, error: "" });
    try {
      const databases = await api.createDatabase(createDatabaseModal.connection.id, name);
      setCreateDatabaseModal(null);
      setDatabaseModal({ connection: createDatabaseModal.connection, databases, loading: false, error: "" });
    } catch (error) {
      setCreateDatabaseModal({ ...createDatabaseModal, name, submitting: false, error: error instanceof Error ? error.message : "数据库创建失败" });
    }
  }

  async function confirmDeleteConnection() {
    if (!deleteModal) return;
    setDeleteModal({ ...deleteModal, submitting: true, error: "" });
    try {
      await api.deleteConnection(deleteModal.connection.id);
      onDelete(deleteModal.connection.id);
      setDeleteModal(null);
    } catch (error) {
      setDeleteModal({ ...deleteModal, submitting: false, error: error instanceof Error ? error.message : "删除连接失败" });
    }
  }

  async function submitSeedData(event: React.FormEvent) {
    event.preventDefault();
    if (!seedDataModal) return;
    if (seedDataModal.mode === "replace" && !seedDataConfirmOpen) {
      setSeedDataConfirmOpen(true);
      return;
    }
    await executeSeedData();
  }

  async function executeSeedData() {
    if (!seedDataModal) return;
    setSeedDataModal({ ...seedDataModal, submitting: true, error: "", result: null });
    try {
      const result = await api.seedData(seedDataModal.connection.id, seedDataModal.database, Number(seedDataModal.rows), seedDataModal.mode, seedDataModal.template);
      setSeedDataModal({ ...seedDataModal, submitting: false, error: "", result });
      setSeedDataConfirmOpen(false);
    } catch (error) {
      setSeedDataModal({ ...seedDataModal, submitting: false, error: error instanceof Error ? error.message : "写入假数据失败", result: null });
    }
  }

  async function copyAddress(connection: DatabaseConnection) {
    setOpenMenu(null);
    setActionError("");
    setManualCopyValue("");
    const value = `${connection.host}:${connection.port}`;
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(value);
      } else {
        const copied = fallbackCopyText(value);
        if (!copied) {
          setManualCopyValue(value);
          setActionError(`当前浏览器不支持自动复制，请手动复制：${value}`);
          return;
        }
      }
      setCopied(connection.id);
      window.setTimeout(() => setCopied(null), 1400);
    } catch (error) {
      console.error("Copy address failed", error);
      const copied = fallbackCopyText(value);
      if (!copied) {
        setManualCopyValue(value);
        setActionError(`当前浏览器不支持自动复制，请手动复制：${value}`);
        return;
      }
      setCopied(connection.id);
      window.setTimeout(() => setCopied(null), 1400);
    }
  }

  function startMigration(connectionId?: string) {
    setOpenMenu(null);
    onCreateMigration(connectionId);
  }

  return (
    <div className="page-stack">
      <div className="page-heading">
        <div><h2>数据连接</h2><p>统一管理迁移源端和目标端。</p></div>
        <div className="page-heading-actions"><Button variant="secondary" onClick={() => setShowBatchImport(true)}><FileSpreadsheet size={17} />批量导入</Button><Button onClick={openNewConnection}><Plus size={17} />新建连接</Button></div>
      </div>
      <div className="toolbar">
        <label className="search-box"><Search size={17} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索连接名称、地址或类型" /></label>
        <span>共 {filtered.length} 个连接</span>
      </div>
      {actionError && <div className="error-banner">{actionError}</div>}
      {manualCopyValue && (
        <div className="error-banner">
          <strong>手动复制：</strong>
          <input
            readOnly
            value={manualCopyValue}
            onFocus={(event) => event.currentTarget.select()}
            aria-label="手动复制主机地址"
          />
        </div>
      )}
      <div className="connections-grid">
        {filtered.map((connection) => (
          <Card className="connection-card" key={connection.id}>
            <div className="connection-card-head">
              <DatabaseLogo type={connection.type} name={connection.name} />
              <div>
                <strong>{connection.name}</strong>
                <span className="connection-version-line"><DatabaseTypeIcon type={connection.type} />{connection.type}<em className="version-tag">{connection.version ? `v${connection.version}` : "待刷新"}</em></span>
              </div>
              <div className="connection-actions" onClick={(event) => event.stopPropagation()}>
                <button className="plain-icon action-trigger" aria-label={`${connection.name} 更多操作`} aria-expanded={openMenu === connection.id} onClick={() => setOpenMenu(openMenu === connection.id ? null : connection.id)}><MoreHorizontal size={18} /></button>
                {openMenu === connection.id && (
                  <div className="connection-action-menu">
                    <button onClick={() => openEditConnection(connection)}><Edit3 size={15} /><span>编辑连接信息</span></button>
                    <button onClick={() => openCopyConnection(connection)}><Copy size={15} /><span>复制连接</span></button>
                    <button onClick={() => testConnection(connection.id)} disabled={testing === connection.id}>{testing === connection.id ? <RefreshCw size={15} className="spin" /> : <RefreshCw size={15} />}<span>刷新状态</span></button>
                    <button onClick={() => showDatabases(connection)}><Database size={15} /><span>查看数据库</span></button>
                    {supportsDirectDatabaseCreate(connection.type) && <button onClick={() => openCreateDatabase(connection)}><Plus size={15} /><span>创建数据库</span></button>}
                    {supportsSeedData(connection.type) && <button onClick={() => openSeedData(connection)}><WandSparkles size={15} /><span>写入假数据</span></button>}
                    {supportsMigration(connection.type) && <button onClick={() => startMigration(connection.id)}><Play size={15} /><span>发起迁移</span></button>}
                    <button className="danger-action" onClick={() => openDeleteConnection(connection)}><Trash2 size={15} /><span>删除连接</span></button>
                  </div>
                )}
              </div>
            </div>
            <dl>
              <div><dt>主机地址</dt><dd className="host-address-value"><button type="button" className="copy-inline" onClick={() => copyAddress(connection)} aria-label={`复制 ${connection.host}:${connection.port}`} title={`${connection.host}:${connection.port}`}>{connection.host}:{connection.port}</button>{copied === connection.id && <span className="copy-inline-state">已复制</span>}</dd></div>
              <div><dt>连接用户</dt><dd>{connection.username}</dd></div>
              <div><dt>节点地区</dt><dd className="region-value"><RegionFlag region={connection.region} /><span>{connection.region || "未知"}</span></dd></div>
              <div><dt>备注</dt><dd>{connection.note || "未填写"}</dd></div>
            </dl>
            <div className="connection-card-foot">
              <Badge tone={connection.status === "connected" ? "green" : "red"}>{connection.status === "connected" ? "已连接" : "连接失败"}</Badge>
              <button className={connection.status === "connected" ? "health-action healthy" : "health-action failed"} onClick={() => testConnection(connection.id)} disabled={testing === connection.id}>
                {testing === connection.id ? <RefreshCw size={14} className="spin" /> : connection.status === "connected" ? <Check size={14} /> : <AlertTriangle size={14} />}
                {testing === connection.id ? "正在测试..." : connection.status === "connected" ? `${connection.latency}ms` : "网络不可达"}
              </button>
            </div>
          </Card>
        ))}
      </div>

      {showBatchImport && <BatchImportModal onClose={() => setShowBatchImport(false)} onImported={onBatchImported} />}

      {databaseModal && (
        <div className="modal-backdrop" onMouseDown={() => setDatabaseModal(null)}>
          <div className="modal database-modal" onMouseDown={(event) => event.stopPropagation()}>
            <div className="modal-head"><div><h3>{databaseModal.connection.name} 的数据库</h3><p>{databaseModal.connection.host}:{databaseModal.connection.port}</p></div><div className="modal-head-actions">{supportsDirectDatabaseCreate(databaseModal.connection.type) && <Button type="button" variant="secondary" onClick={() => openCreateDatabase(databaseModal.connection)}><Plus size={15} />创建数据库</Button>}<button type="button" className="plain-icon" onClick={() => setDatabaseModal(null)}><X /></button></div></div>
            {databaseModal.loading && <div className="loading-copy">正在加载数据库...</div>}
            {databaseModal.error && <div className="form-error left">{databaseModal.error}</div>}
            {!databaseModal.loading && !databaseModal.error && (
              <div className="connection-database-list">
                {databaseModal.databases.map((database) => <div key={database.name}><strong>{database.name}</strong><span>{formatDatabaseMetric(databaseModal.connection.type, database)}</span></div>)}
                {!databaseModal.databases.length && <div className="loading-copy">暂无可用数据库。</div>}
              </div>
            )}
          </div>
        </div>
      )}

      {createDatabaseModal && (
        <div className="modal-backdrop" onMouseDown={() => setCreateDatabaseModal(null)}>
          <form className="modal create-database-modal" onSubmit={submitCreateDatabase} onMouseDown={(event) => event.stopPropagation()}>
            <div className="modal-head"><div><h3>创建数据库</h3><p>{createDatabaseModal.connection.name} · {createDatabaseModal.connection.host}:{createDatabaseModal.connection.port}</p></div><button type="button" className="plain-icon" onClick={() => setCreateDatabaseModal(null)}><X /></button></div>
            <label className="field"><span>数据库名称</span><input required autoFocus value={createDatabaseModal.name} onChange={(event) => setCreateDatabaseModal({ ...createDatabaseModal, name: event.target.value, error: "" })} placeholder="例如 app_prod" /></label>
            <p className="field-hint">{createDatabaseModal.connection.type === "MongoDB" ? "MongoDB 会创建一个初始化集合，让数据库立即可见。数据库名不要包含空格以及 / \\ . \" $ 等字符。" : createDatabaseModal.connection.type === "Neo4j" ? "Neo4j Community 通常只支持默认库；如果实例不支持多数据库，创建时会返回权限或版本限制。" : "最多 63 个字符，使用字母或下划线开头，只包含字母、数字、下划线或短横线。"}</p>
            {createDatabaseModal.error && <div className="form-error left">{createDatabaseModal.error}</div>}
            <div className="modal-actions"><Button type="button" variant="secondary" onClick={() => setCreateDatabaseModal(null)}>取消</Button><Button type="submit" disabled={createDatabaseModal.submitting}>{createDatabaseModal.submitting ? "正在创建..." : "创建数据库"}</Button></div>
          </form>
        </div>
      )}

      {seedDataModal && (
        <div className="modal-backdrop" onMouseDown={() => setSeedDataModal(null)}>
          <form className="modal seed-data-modal" onSubmit={submitSeedData} onMouseDown={(event) => event.stopPropagation()}>
            <div className="modal-head"><div><h3>写入假数据</h3><p>{seedDataModal.connection.name} · {seedDataModal.connection.host}:{seedDataModal.connection.port}</p></div><button type="button" className="plain-icon" onClick={() => setSeedDataModal(null)}><X /></button></div>
            <p className="field-hint">{seedDataHint(seedDataModal.connection.type)}</p>
            {seedDataModal.loading ? <div className="loading-copy">正在加载数据库...</div> : <>
              <SelectField label="目标数据库" value={seedDataModal.database} onChange={(database) => setSeedDataModal({ ...seedDataModal, database, error: "", result: null })}>{seedDataModal.databases.map((item) => <option key={item.name} value={item.name}>{item.name}</option>)}</SelectField>
              <SelectField label="写入模板" value={seedDataModal.template} onChange={(template) => setSeedDataModal({ ...seedDataModal, template: template as "users" | "orders" | "products", error: "", result: null })}><option value="users">用户数据</option><option value="orders">订单数据</option><option value="products">商品数据</option></SelectField>
              <SelectField label="写入方式" value={seedDataModal.mode} onChange={(mode) => setSeedDataModal({ ...seedDataModal, mode: mode as "append" | "replace", error: "", result: null })}><option value="append">追加写入</option><option value="replace">覆盖写入</option></SelectField>
              <label className="field"><span>写入条数</span><input type="number" min={1} max={50000} value={seedDataModal.rows} onChange={(event) => setSeedDataModal({ ...seedDataModal, rows: event.target.value, error: "", result: null })} /></label>
            </>}
            {seedDataModal.error && <div className="form-error left">{seedDataModal.error}</div>}
            {seedDataModal.result && <div className="info-banner">已向 {seedDataModal.result.database} 的 {seedDataModal.result.table}{seedDataTargetSuffix(seedDataModal.connection.type)} {seedDataModal.result.mode === "replace" ? "覆盖写入" : "追加写入"} {seedDataModal.result.rows} 条{seedDataModal.result.template === "users" ? "用户" : seedDataModal.result.template === "orders" ? "订单" : "商品"}数据。</div>}
            <div className="modal-actions"><Button type="button" variant="secondary" onClick={() => setSeedDataModal(null)}>关闭</Button><Button type="submit" disabled={seedDataModal.loading || seedDataModal.submitting || !seedDataModal.database}>{seedDataModal.submitting ? "正在写入..." : "开始写入"}</Button></div>
          </form>
        </div>
      )}

      {deleteModal && (
        <div className="modal-backdrop" onMouseDown={() => setDeleteModal(null)}>
          <div className="modal delete-connection-modal" onMouseDown={(event) => event.stopPropagation()}>
            <div className="modal-head"><div><h3>删除连接</h3><p>{deleteModal.connection.name} · {deleteModal.connection.host}:{deleteModal.connection.port}</p></div><button type="button" className="plain-icon" onClick={() => setDeleteModal(null)}><X /></button></div>
            <p className="delete-warning">删除后将清除这条连接的已保存凭证，且不能再基于它查看数据库或发起迁移。</p>
            <p className="delete-warning">历史迁移记录会保留，但不会再关联这条连接继续操作。</p>
            {deleteModal.error && <div className="form-error left">{deleteModal.error}</div>}
            <div className="modal-actions"><Button type="button" variant="secondary" onClick={() => setDeleteModal(null)}>取消</Button><Button type="button" variant="danger" disabled={deleteModal.submitting} onClick={confirmDeleteConnection}>{deleteModal.submitting ? "正在删除..." : "删除连接"}</Button></div>
          </div>
        </div>
      )}

      {duplicateModal && (
        <div className="modal-backdrop" onMouseDown={() => setDuplicateModal(null)}>
          <form className="modal" onSubmit={submitDuplicateConnection} onMouseDown={(event) => event.stopPropagation()}>
            <div className="modal-head"><div><h3>复制连接</h3><p>从 {duplicateModal.connection.name} 复制配置，默认沿用已保存的密码和证书。</p></div><button type="button" className="plain-icon" onClick={() => setDuplicateModal(null)}><X /></button></div>
            <label className="field"><span>连接名称</span><input required value={duplicateModal.form.name} onChange={(e) => updateDuplicateForm({ name: e.target.value })} /></label>
            <SelectField label="数据库类型" value={duplicateModal.connection.type} disabled onChange={() => undefined}><option value={duplicateModal.connection.type}>{duplicateModal.connection.type}</option></SelectField>
            <div className="field-row"><label className="field grow"><span>主机地址</span><input required value={duplicateModal.form.host} onChange={(e) => updateDuplicateForm({ host: e.target.value })} /></label><label className="field port"><span>端口</span><input required type="number" value={duplicateModal.form.port} onChange={(e) => updateDuplicateForm({ port: e.target.value })} /></label></div>
            <label className="field"><span>用户名</span><input required value={duplicateModal.form.username} onChange={(e) => updateDuplicateForm({ username: e.target.value })} /></label>
            <label className="field"><span>密码</span><input type="password" value={duplicateModal.form.password} onChange={(e) => updateDuplicateForm({ password: e.target.value })} placeholder="留空则沿用原连接密码" /></label>
            <SelectField label={connectionSecurityConfig(duplicateModal.connection.type).label} value={duplicateModal.form.sslMode} onChange={(sslMode) => updateDuplicateForm({ sslMode })}>
              {connectionSecurityConfig(duplicateModal.connection.type).options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
            </SelectField>
            <p className="field-hint">{connectionSecurityConfig(duplicateModal.connection.type).hint}</p>
            {duplicateModal.form.sslMode === "verify-full" && (
              <TLSCertificateFields
                value={duplicateModal.form.tlsConfig}
                onChange={(tlsConfig) => updateDuplicateForm({ tlsConfig })}
                preserveExisting
              />
            )}
            <label className="field"><span>备注</span><input value={duplicateModal.form.note} onChange={(e) => updateDuplicateForm({ note: e.target.value })} placeholder="可选，记录用途、环境或负责人" /></label>
            {duplicateModal.error && <div className="form-error left">{duplicateModal.error}</div>}
            <div className="modal-actions"><Button type="button" variant="secondary" onClick={() => setDuplicateModal(null)}>取消</Button><Button type="submit" disabled={duplicateModal.submitting}>{duplicateModal.submitting ? "正在复制..." : "复制并测试"}</Button></div>
          </form>
        </div>
      )}

      {editModal && (
        <div className="modal-backdrop" onMouseDown={() => setEditModal(null)}>
          <form className="modal" onSubmit={submitEditConnection} onMouseDown={(event) => event.stopPropagation()}>
            <div className="modal-head"><div><h3>编辑连接信息</h3><p>{editModal.connection.name} · 保存后会重新测试连接。</p></div><button type="button" className="plain-icon" onClick={() => setEditModal(null)}><X /></button></div>
            <label className="field"><span>连接名称</span><input required value={editModal.form.name} onChange={(e) => setEditModal({ ...editModal, form: { ...editModal.form, name: e.target.value }, error: "" })} /></label>
            <SelectField label="数据库类型" value={editModal.connection.type} disabled onChange={() => undefined}><option value={editModal.connection.type}>{editModal.connection.type}</option></SelectField>
            <div className="field-row"><label className="field grow"><span>主机地址</span><input required value={editModal.form.host} onChange={(e) => setEditModal({ ...editModal, form: { ...editModal.form, host: e.target.value }, error: "" })} /></label><label className="field port"><span>端口</span><input required type="number" value={editModal.form.port} onChange={(e) => setEditModal({ ...editModal, form: { ...editModal.form, port: e.target.value }, error: "" })} /></label></div>
            <label className="field"><span>用户名</span><input required value={editModal.form.username} onChange={(e) => setEditModal({ ...editModal, form: { ...editModal.form, username: e.target.value }, error: "" })} /></label>
            <label className="field"><span>密码</span><input type="password" value={editModal.form.password} onChange={(e) => setEditModal({ ...editModal, form: { ...editModal.form, password: e.target.value }, error: "" })} placeholder="留空则保持当前密码不变" /></label>
            <SelectField label={connectionSecurityConfig(editModal.connection.type).label} value={editModal.form.sslMode} onChange={(sslMode) => setEditModal({ ...editModal, form: { ...editModal.form, sslMode }, error: "" })}>
              {connectionSecurityConfig(editModal.connection.type).options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
            </SelectField>
            <p className="field-hint">{connectionSecurityConfig(editModal.connection.type).hint}</p>
            {editModal.form.sslMode === "verify-full" && (
              <TLSCertificateFields
                value={editModal.form.tlsConfig}
                onChange={(tlsConfig) => setEditModal({ ...editModal, form: { ...editModal.form, tlsConfig }, error: "" })}
                preserveExisting
              />
            )}
            <label className="field"><span>备注</span><input value={editModal.form.note} onChange={(e) => setEditModal({ ...editModal, form: { ...editModal.form, note: e.target.value }, error: "" })} placeholder="可选，记录用途、环境或负责人" /></label>
            {editModal.error && <div className="form-error left">{editModal.error}</div>}
            <div className="modal-actions"><Button type="button" variant="secondary" onClick={() => setEditModal(null)}>取消</Button><Button type="submit" disabled={editModal.submitting}>{editModal.submitting ? "正在保存..." : "保存并测试"}</Button></div>
          </form>
        </div>
      )}

      {seedDataConfirmOpen && seedDataModal && <ConfirmDialog title="覆盖写入假数据" description="这是中危操作，目标演示表中的现有数据会被覆盖。" tone="warning" confirmText="确认覆盖写入" submitting={seedDataModal.submitting} onCancel={() => setSeedDataConfirmOpen(false)} onConfirm={() => void executeSeedData()} details={[`目标：${seedDataModal.connection.name} / ${seedDataModal.database}`, `模板：${seedDataModal.template}`, `写入条数：${seedDataModal.rows}`]} />}

      {showForm && (
        <div className="modal-backdrop" onMouseDown={closeConnectionForm}>
          <form className="modal" onSubmit={submit} onMouseDown={(event) => event.stopPropagation()}>
            <div className="modal-head"><div><h3>新建数据库连接</h3><p>凭证会加密保存，连接测试通过后即可使用。</p></div><button type="button" className="plain-icon" onClick={closeConnectionForm}><X /></button></div>
            <SelectField label={requiredLabel("数据库类型")} ariaLabel="数据库类型" invalid={Boolean(formErrors.type)} value={form.type} onChange={(type) => { const nextForm = { ...form, type: type as DatabaseType, port: defaultPort(type as DatabaseType), sslMode: defaultSecurityMode(type as DatabaseType), tlsConfig: emptyTLSConfig() }; setForm(nextForm); setFormErrors((current) => ({ ...current, type: undefined, port: undefined, sslMode: undefined })); }}><option value="PostgreSQL">PostgreSQL</option><option value="MySQL">MySQL</option><option value="MongoDB">MongoDB</option><option value="Neo4j">Neo4j</option></SelectField>
            {formErrors.type && <div className="form-error left field-error">{formErrors.type}</div>}
            <div className="compatibility-hint"><Info size={16} /><span>{formCompatibilityHint}</span></div>
            <label className="field"><span>{requiredLabel("连接名称")}</span><input value={form.name} className={formErrors.name ? "invalid" : ""} onChange={(e) => { updateFormField("name", e.target.value); setNameCustomized(e.target.value.trim().length > 0); setFormErrors((current) => ({ ...current, name: undefined })); }} onBlur={(e) => validateSingleField("name", { ...form, name: e.target.value })} placeholder="输入便于识别的连接名称" /></label>
            {formErrors.name && <div className="form-error left field-error">{formErrors.name}</div>}
            <div className="field-row"><label className="field grow"><span>{requiredLabel("主机地址")}</span><input value={form.host} className={formErrors.host ? "invalid" : ""} onChange={(e) => { const nextForm = { ...form, host: e.target.value }; setForm(nextForm); setFormErrors((current) => ({ ...current, host: validateConnectionForm(nextForm).host })); }} onBlur={(e) => validateSingleField("host", { ...form, host: e.target.value })} placeholder="10.24.8.16" /></label><label className="field port"><span>{requiredLabel("端口")}</span><input inputMode="numeric" pattern="[0-9]*" maxLength={5} value={form.port} className={formErrors.port ? "invalid" : ""} onBeforeInput={(e) => { const value = (e as unknown as { data?: string }).data ?? ""; if (value && /\D/.test(value)) e.preventDefault(); }} onChange={(e) => { const next = e.target.value.replace(/[^\d]/g, "").slice(0, 5); const nextForm = { ...form, port: next }; setForm(nextForm); setFormErrors((current) => ({ ...current, port: validateConnectionForm(nextForm).port })); }} onBlur={(e) => validateSingleField("port", { ...form, port: e.target.value })} /></label></div>
            {(formErrors.host || formErrors.port) && <div className="form-error left field-error">{formErrors.host ?? formErrors.port}</div>}
            <label className="field"><span>{requiredLabel("用户名")}</span><input value={form.username} className={formErrors.username ? "invalid" : ""} onChange={(e) => { updateFormField("username", e.target.value); setFormErrors((current) => ({ ...current, username: undefined })); }} onBlur={(e) => validateSingleField("username", { ...form, username: e.target.value })} placeholder="migration_user" /></label>
            {formErrors.username && <div className="form-error left field-error">{formErrors.username}</div>}
            <label className="field"><span>{requiredLabel("密码")}</span><input type="password" value={form.password} className={formErrors.password ? "invalid" : ""} onChange={(e) => { updateFormField("password", e.target.value); setFormErrors((current) => ({ ...current, password: undefined })); }} onBlur={(e) => validateSingleField("password", { ...form, password: e.target.value })} /></label>
            {formErrors.password && <div className="form-error left field-error">{formErrors.password}</div>}
            <SelectField label={requiredLabel(formSecurityConfig.label)} ariaLabel={formSecurityConfig.label} invalid={Boolean(formErrors.sslMode)} value={form.sslMode} onChange={(sslMode) => { setForm({ ...form, sslMode }); setFormErrors((current) => ({ ...current, sslMode: undefined })); }}>
              {formSecurityConfig.options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
            </SelectField>
            {formErrors.sslMode && <div className="form-error left field-error">{formErrors.sslMode}</div>}
            <p className="field-hint">{formSecurityConfig.hint}</p>
            {form.sslMode === "verify-full" && (
              <TLSCertificateFields value={form.tlsConfig} onChange={(tlsConfig) => setForm({ ...form, tlsConfig })} />
            )}
            <label className="field"><span>备注</span><input value={form.note} onChange={(e) => setForm({ ...form, note: e.target.value })} placeholder="可选，记录用途、环境或负责人" /></label>
            {formError && <div className="form-error left">{formError}</div>}
            <div className="modal-actions"><Button type="button" variant="secondary" onClick={closeConnectionForm}>取消</Button><Button type="submit" disabled={submitting}>{submitting ? "正在保存..." : "测试并保存"}</Button></div>
          </form>
        </div>
      )}
    </div>
  );
}

function formatBytes(value: number) {
  if (!value) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  return `${(value / 1024 ** index).toFixed(index === 0 ? 0 : 1)} ${units[index]}`;
}

function formatDatabaseMetric(type: DatabaseType, database: DatabaseInfo) {
  if (type === "Neo4j") {
    return `${formatNumber(database.nodes ?? 0)} 节点 · ${formatNumber(database.relationships ?? 0)} 关系`;
  }
  return formatBytes(database.sizeBytes);
}

function formatNumber(value: number) {
  return new Intl.NumberFormat("zh-CN").format(value);
}

function duplicateConnectionName(name: string, existingNames: string[]) {
  const base = `${name} 副本`;
  if (!existingNames.includes(base)) return base;
  for (let index = 2; index < 100; index += 1) {
    const candidate = `${base} ${index}`;
    if (!existingNames.includes(candidate)) return candidate;
  }
  return `${base} ${Date.now()}`;
}

function seedDataHint(type: DatabaseType) {
  if (type === "MongoDB") {
    return "可按模板生成演示数据。每个模板会写入自己的演示集合，默认追加写入，也可切换为覆盖写入。";
  }
  if (type === "Neo4j") {
    return "可按模板生成演示图数据。每个模板会写入自己的演示节点标签，默认追加写入，也可切换为覆盖写入。";
  }
  return "可按模板生成演示数据。每个模板会写入自己的演示表，默认追加写入，也可切换为覆盖写入。";
}

function seedDataTargetSuffix(type: DatabaseType) {
  if (type === "MongoDB") return " 集合";
  if (type === "Neo4j") return " 节点标签";
  return "";
}

function connectionCompatibilityHint(type: DatabaseType) {
  switch (type) {
    case "PostgreSQL":
      return "适用版本建议：PostgreSQL 13、14、15、16、18 已完成主组合兼容性测试，建议优先使用这些版本；跨大版本降级迁移请先演练验证。";
    case "MySQL":
      return "适用版本建议：MySQL 5.7、8.0 已完成主组合兼容性测试，建议优先使用这些版本；其他版本请先在测试环境完成连接、迁移、备份恢复演练。";
    case "MongoDB":
      return "适用版本建议：MongoDB 5.0、6.0、7.0、8.0 已完成主组合兼容性测试，建议优先使用这些版本；其他版本请先完成兼容性演练。";
    case "Neo4j":
      return "适用版本建议：Neo4j 4.4、5.x 已完成主组合兼容性测试，建议优先使用这些版本；跨大版本场景请先验证约束和索引兼容性。";
    default:
      return "适用版本建议：当前数据库类型暂无兼容性测试结论，请先在测试环境完成连接、迁移、备份恢复演练。";
  }
}

function requiredLabel(label: string) {
  return <><span className="required-mark" aria-hidden="true">* </span>{label}</>;
}

function connectionSecurityConfig(type: DatabaseType) {
  switch (type) {
    case "PostgreSQL":
      return {
        label: "SSL 模式",
        hint: "PostgreSQL 原生支持 sslmode。prefer 会优先尝试加密，require 强制加密，verify-full 会校验证书和主机名。",
        options: [
          { value: "prefer", label: "prefer · 优先加密" },
          { value: "require", label: "require · 强制加密" },
          { value: "disable", label: "disable · 不加密" },
          { value: "verify-full", label: "verify-full · 校验证书与主机名" },
        ],
      };
    case "MySQL":
      return {
        label: "SSL 模式",
        hint: "MySQL 这里映射为客户端 TLS 连接策略。prefer 会尽量协商 TLS，require 强制启用 TLS，verify-full 会启用严格校验。",
        options: [
          { value: "prefer", label: "prefer · 优先加密" },
          { value: "require", label: "require · 强制加密" },
          { value: "disable", label: "disable · 不加密" },
          { value: "verify-full", label: "verify-full · 严格校验" },
        ],
      };
    case "MongoDB":
      return {
        label: "TLS 模式",
        hint: "MongoDB 默认不启用 TLS；require 只强制加密，verify-full 会校验证书链和主机名。",
        options: [
          { value: "disable", label: "disable · 不启用 TLS" },
          { value: "require", label: "require · 启用 TLS" },
          { value: "verify-full", label: "verify-full · 校验证书与主机名" },
        ],
      };
    case "Neo4j":
      return {
        label: "TLS 模式",
        hint: "Neo4j 当前映射到底层 Bolt 协议：默认使用 bolt；require 使用 bolt+ssc，verify-full 使用 bolt+s。",
        options: [
          { value: "disable", label: "disable · 使用 bolt" },
          { value: "require", label: "require · 使用 bolt+ssc" },
          { value: "verify-full", label: "verify-full · 使用 bolt+s" },
        ],
      };
    default:
      return {
        label: "SSL 模式",
        hint: "当前数据库类型未定义单独的连接加密策略说明。",
        options: [
          { value: "prefer", label: "prefer" },
          { value: "require", label: "require" },
          { value: "disable", label: "disable" },
        ],
      };
  }
}

function defaultSecurityMode(type: DatabaseType) {
  return type === "MongoDB" || type === "Neo4j" ? "disable" : "prefer";
}

function defaultPort(type: DatabaseType) {
  if (type === "MySQL") return "3306";
  if (type === "MongoDB") return "27017";
  if (type === "Neo4j") return "7687";
  return "5432";
}

function emptyTLSConfig(): ConnectionTLSConfig {
  return { caCert: "", clientCert: "", clientKey: "" };
}

function emptyConnectionForm(type: DatabaseType): ConnectionFormState {
  return {
    type,
    name: suggestConnectionName(type, "", defaultPort(type)),
    note: "",
    host: "",
    port: defaultPort(type),
    username: "",
    password: "",
    sslMode: defaultSecurityMode(type),
    tlsConfig: emptyTLSConfig(),
  };
}

function suggestConnectionName(type: DatabaseType, host: string, port: string) {
  const hostValue = host.trim() || "主机地址";
  const portValue = port.trim() || defaultPort(type);
  return `${type}-${hostValue}-${portValue}`;
}

function validateConnectionForm(form: ConnectionFormState): ConnectionFormErrors {
  const errors: ConnectionFormErrors = {};
  if (!form.type) errors.type = "请选择数据库类型";
  if (!form.name.trim()) errors.name = "请输入连接名称";
  if (!form.host.trim()) errors.host = "请输入主机地址";
  else if (!isValidConnectionHost(form.host.trim())) errors.host = "请输入合法的 IPv4 地址或域名 / 主机名";

  if (!form.port.trim()) errors.port = "请输入端口";
  else if (!/^\d+$/.test(form.port.trim())) errors.port = "端口只能输入数字";
  else {
    const port = Number(form.port.trim());
    if (!Number.isInteger(port) || port < 1 || port > 65535) errors.port = "端口范围需在 1 到 65535 之间";
  }

  if (!form.username.trim()) errors.username = "请输入用户名";
  if (!form.password.trim()) errors.password = "请输入密码";
  if (!form.sslMode.trim()) errors.sslMode = "请选择连接加密模式";
  return errors;
}

function isValidConnectionHost(value: string) {
  return isIPv4Host(value) || isDomainOrHostname(value);
}

function isIPv4Host(value: string) {
  const segments = value.split(".");
  if (segments.length !== 4) return false;
  return segments.every((segment) => /^(0|[1-9]\d{0,2})$/.test(segment) && Number(segment) >= 0 && Number(segment) <= 255);
}

function isDomainOrHostname(value: string) {
  if (value.length > 253) return false;
  if (value === "localhost") return true;
  const labels = value.split(".");
  if (labels.some((label) => label.length === 0 || label.length > 63)) return false;
  if (!labels.every((label) => /^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?$/.test(label))) return false;
  if (labels.every((label) => /^\d+$/.test(label))) return false;
  const topLevelLabel = labels[labels.length - 1];
  if (/^\d+$/.test(topLevelLabel)) return false;
  return true;
}

function TLSCertificateFields({
  value,
  onChange,
  preserveExisting = false,
}: {
  value: ConnectionTLSConfig;
  onChange: (value: ConnectionTLSConfig) => void;
  preserveExisting?: boolean;
}) {
  const caPlaceholder = preserveExisting ? "留空则沿用已保存的 CA 证书" : "-----BEGIN CERTIFICATE-----";
  return (
    <div className="tls-panel">
      <div>
        <strong>证书校验</strong>
        <p>{preserveExisting ? "verify-full 会校验证书链和主机名；如果不修改证书，可以留空。" : "verify-full 需要 CA 证书来校验服务端证书链和主机名。"}</p>
      </div>
      <label className="field"><span>CA 证书 PEM</span><textarea value={value.caCert ?? ""} onChange={(event) => onChange({ ...value, caCert: event.target.value })} placeholder={caPlaceholder} rows={4} /></label>
      <label className="field"><span>客户端证书 PEM（可选）</span><textarea value={value.clientCert ?? ""} onChange={(event) => onChange({ ...value, clientCert: event.target.value })} placeholder="仅 mTLS 场景需要填写" rows={3} /></label>
      <label className="field"><span>客户端私钥 PEM（可选）</span><textarea value={value.clientKey ?? ""} onChange={(event) => onChange({ ...value, clientKey: event.target.value })} placeholder="填写客户端证书时必须同时填写私钥" rows={3} /></label>
    </div>
  );
}
