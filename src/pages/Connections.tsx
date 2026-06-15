import { Check, Copy, Database, Edit3, MoreHorizontal, Play, Plus, RefreshCw, Search, X } from "lucide-react";
import { useEffect, useState } from "react";
import { api } from "../api";
import type { ConnectionInput, DatabaseConnection, DatabaseInfo } from "../types";
import { Badge, Button, Card, DatabaseLogo, DatabaseTypeIcon, RegionFlag } from "../components/UI";

export function Connections({
  connections,
  onAdd,
  onUpdate,
  onCreateMigration,
}: {
  connections: DatabaseConnection[];
  onAdd: (connection: DatabaseConnection) => void;
  onUpdate: (connection: DatabaseConnection) => void;
  onCreateMigration: () => void;
}) {
  const [showForm, setShowForm] = useState(false);
  const [editModal, setEditModal] = useState<{ connection: DatabaseConnection; form: { name: string; note: string; host: string; port: string; username: string; password: string; sslMode: string }; submitting: boolean; error: string } | null>(null);
  const [testing, setTesting] = useState<string | null>(null);
  const [openMenu, setOpenMenu] = useState<string | null>(null);
  const [databaseModal, setDatabaseModal] = useState<{ connection: DatabaseConnection; databases: DatabaseInfo[]; loading: boolean; error: string } | null>(null);
  const [createDatabaseModal, setCreateDatabaseModal] = useState<{ connection: DatabaseConnection; name: string; submitting: boolean; error: string } | null>(null);
  const [copied, setCopied] = useState<string | null>(null);
  const [actionError, setActionError] = useState("");
  const [query, setQuery] = useState("");
  const [form, setForm] = useState({ name: "", note: "", host: "", port: "5432", username: "", password: "", sslMode: "prefer" });
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState("");
  const filtered = connections.filter((item) => `${item.name} ${item.note ?? ""} ${item.host} ${item.type} ${item.region ?? ""}`.toLowerCase().includes(query.toLowerCase()));

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
    setSubmitting(true);
    setFormError("");
    try {
      const input: ConnectionInput = { ...form, port: Number(form.port) };
      const connection = await api.createConnection(input);
      onAdd(connection);
      setShowForm(false);
      setForm({ name: "", note: "", host: "", port: "5432", username: "", password: "", sslMode: "prefer" });
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
      },
      submitting: false,
      error: "",
    });
  }

  async function submitEditConnection(event: React.FormEvent) {
    event.preventDefault();
    if (!editModal) return;
    setEditModal({ ...editModal, submitting: true, error: "" });
    try {
      const updated = await api.updateConnection(editModal.connection.id, {
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

  async function copyAddress(connection: DatabaseConnection) {
    setOpenMenu(null);
    const value = `${connection.host}:${connection.port}`;
    try {
      const clipboard = navigator.clipboard?.writeText(value);
      if (clipboard) {
        await clipboard;
      } else {
        throw new Error("clipboard unavailable");
      }
      setCopied(connection.id);
      window.setTimeout(() => setCopied(null), 1400);
    } catch {
      setActionError("复制失败，请检查浏览器剪贴板权限。");
    }
  }

  function startMigration() {
    setOpenMenu(null);
    onCreateMigration();
  }

  return (
    <div className="page-stack">
      <div className="page-heading">
        <div><h2>数据连接</h2><p>统一管理迁移源端和目标端实例。</p></div>
        <Button onClick={() => setShowForm(true)}><Plus size={17} />新建连接</Button>
      </div>
      <div className="toolbar">
        <label className="search-box"><Search size={17} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索连接名称、地址或类型" /></label>
        <span>共 {filtered.length} 个连接</span>
      </div>
      {actionError && <div className="error-banner">{actionError}</div>}
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
                    <button onClick={() => testConnection(connection.id)} disabled={testing === connection.id}>{testing === connection.id ? <RefreshCw size={15} className="spin" /> : <RefreshCw size={15} />}<span>刷新状态</span></button>
                    <button onClick={() => showDatabases(connection)}><Database size={15} /><span>查看数据库</span></button>
                    <button onClick={() => openCreateDatabase(connection)}><Plus size={15} /><span>创建数据库</span></button>
                    <button onClick={startMigration}><Play size={15} /><span>发起迁移</span></button>
                    <button onClick={() => copyAddress(connection)}><Copy size={15} /><span>{copied === connection.id ? "已复制" : "复制地址"}</span></button>
                  </div>
                )}
              </div>
            </div>
            <dl>
              <div><dt>主机地址</dt><dd><button type="button" className="copy-inline" onClick={() => copyAddress(connection)} aria-label={`复制 ${connection.host}:${connection.port}`}>{connection.host}:{connection.port}</button>{copied === connection.id && <span className="copy-inline-state">已复制</span>}</dd></div>
              <div><dt>连接用户</dt><dd>{connection.username}</dd></div>
              <div><dt>节点地区</dt><dd className="region-value"><RegionFlag region={connection.region} /><span>{connection.region || "未知"}</span></dd></div>
              <div><dt>备注</dt><dd>{connection.note || "未填写"}</dd></div>
            </dl>
            <div className="connection-card-foot">
              <Badge tone={connection.status === "connected" ? "green" : "orange"}>{connection.status === "connected" ? "已连接" : "需验证"}</Badge>
              <button onClick={() => testConnection(connection.id)} disabled={testing === connection.id}>
                {testing === connection.id ? <RefreshCw size={14} className="spin" /> : <Check size={14} />}
                {testing === connection.id ? "测试中" : `${connection.latency}ms`}
              </button>
            </div>
          </Card>
        ))}
      </div>

      {databaseModal && (
        <div className="modal-backdrop" onMouseDown={() => setDatabaseModal(null)}>
          <div className="modal database-modal" onMouseDown={(event) => event.stopPropagation()}>
            <div className="modal-head"><div><h3>{databaseModal.connection.name} 的数据库</h3><p>{databaseModal.connection.host}:{databaseModal.connection.port}</p></div><div className="modal-head-actions"><Button type="button" variant="secondary" onClick={() => openCreateDatabase(databaseModal.connection)}><Plus size={15} />创建数据库</Button><button type="button" className="plain-icon" onClick={() => setDatabaseModal(null)}><X /></button></div></div>
            {databaseModal.loading && <div className="loading-copy">正在读取数据库...</div>}
            {databaseModal.error && <div className="form-error left">{databaseModal.error}</div>}
            {!databaseModal.loading && !databaseModal.error && (
              <div className="connection-database-list">
                {databaseModal.databases.map((database) => <div key={database.name}><strong>{database.name}</strong><span>{formatBytes(database.sizeBytes)}</span></div>)}
                {!databaseModal.databases.length && <div className="loading-copy">没有可连接的数据库。</div>}
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
            <p className="field-hint">名称最多 63 个字符，支持字母、数字、下划线和短横线，首字符必须是字母或下划线。</p>
            {createDatabaseModal.error && <div className="form-error left">{createDatabaseModal.error}</div>}
            <div className="modal-actions"><Button type="button" variant="secondary" onClick={() => setCreateDatabaseModal(null)}>取消</Button><Button type="submit" disabled={createDatabaseModal.submitting}>{createDatabaseModal.submitting ? "正在创建..." : "创建数据库"}</Button></div>
          </form>
        </div>
      )}

      {editModal && (
        <div className="modal-backdrop" onMouseDown={() => setEditModal(null)}>
          <form className="modal" onSubmit={submitEditConnection} onMouseDown={(event) => event.stopPropagation()}>
            <div className="modal-head"><div><h3>编辑连接信息</h3><p>{editModal.connection.name} · 更新后会重新测试连接。</p></div><button type="button" className="plain-icon" onClick={() => setEditModal(null)}><X /></button></div>
            <label className="field"><span>连接名称</span><input required value={editModal.form.name} onChange={(e) => setEditModal({ ...editModal, form: { ...editModal.form, name: e.target.value }, error: "" })} /></label>
            <label className="field"><span>数据库类型</span><select disabled><option>PostgreSQL</option></select></label>
            <div className="field-row"><label className="field grow"><span>主机地址</span><input required value={editModal.form.host} onChange={(e) => setEditModal({ ...editModal, form: { ...editModal.form, host: e.target.value }, error: "" })} /></label><label className="field port"><span>端口</span><input required type="number" value={editModal.form.port} onChange={(e) => setEditModal({ ...editModal, form: { ...editModal.form, port: e.target.value }, error: "" })} /></label></div>
            <label className="field"><span>用户名</span><input required value={editModal.form.username} onChange={(e) => setEditModal({ ...editModal, form: { ...editModal.form, username: e.target.value }, error: "" })} /></label>
            <label className="field"><span>密码</span><input type="password" value={editModal.form.password} onChange={(e) => setEditModal({ ...editModal, form: { ...editModal.form, password: e.target.value }, error: "" })} placeholder="留空则保持当前密码不变" /></label>
            <label className="field"><span>SSL 模式</span><select value={editModal.form.sslMode} onChange={(e) => setEditModal({ ...editModal, form: { ...editModal.form, sslMode: e.target.value }, error: "" })}><option value="prefer">prefer</option><option value="require">require</option><option value="disable">disable</option><option value="verify-full">verify-full</option></select></label>
            <label className="field"><span>备注</span><input value={editModal.form.note} onChange={(e) => setEditModal({ ...editModal, form: { ...editModal.form, note: e.target.value }, error: "" })} placeholder="可选，记录用途、环境或负责人" /></label>
            {editModal.error && <div className="form-error left">{editModal.error}</div>}
            <div className="modal-actions"><Button type="button" variant="secondary" onClick={() => setEditModal(null)}>取消</Button><Button type="submit" disabled={editModal.submitting}>{editModal.submitting ? "正在更新..." : "保存并测试"}</Button></div>
          </form>
        </div>
      )}

      {showForm && (
        <div className="modal-backdrop" onMouseDown={() => setShowForm(false)}>
          <form className="modal" onSubmit={submit} onMouseDown={(event) => event.stopPropagation()}>
            <div className="modal-head"><div><h3>新建数据库连接</h3><p>凭证将加密存储，连接测试通过后即可用于迁移。</p></div><button type="button" className="plain-icon" onClick={() => setShowForm(false)}><X /></button></div>
            <label className="field"><span>连接名称</span><input required value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="输入便于识别的连接名称" /></label>
            <label className="field"><span>数据库类型</span><select disabled><option>PostgreSQL</option></select></label>
            <div className="field-row"><label className="field grow"><span>主机地址</span><input required value={form.host} onChange={(e) => setForm({ ...form, host: e.target.value })} placeholder="10.24.8.16" /></label><label className="field port"><span>端口</span><input required type="number" value={form.port} onChange={(e) => setForm({ ...form, port: e.target.value })} /></label></div>
            <label className="field"><span>用户名</span><input required value={form.username} onChange={(e) => setForm({ ...form, username: e.target.value })} placeholder="migration_user" /></label>
            <label className="field"><span>密码</span><input required type="password" value={form.password} onChange={(e) => setForm({ ...form, password: e.target.value })} /></label>
            <label className="field"><span>SSL 模式</span><select value={form.sslMode} onChange={(e) => setForm({ ...form, sslMode: e.target.value })}><option value="prefer">prefer</option><option value="require">require</option><option value="disable">disable</option><option value="verify-full">verify-full</option></select></label>
            <label className="field"><span>备注</span><input value={form.note} onChange={(e) => setForm({ ...form, note: e.target.value })} placeholder="可选，记录用途、环境或负责人" /></label>
            {formError && <div className="form-error left">{formError}</div>}
            <div className="modal-actions"><Button type="button" variant="secondary" onClick={() => setShowForm(false)}>取消</Button><Button type="submit" disabled={submitting}>{submitting ? "正在测试连接..." : "测试并保存"}</Button></div>
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
