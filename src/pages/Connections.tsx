import { Check, MoreHorizontal, Plus, RefreshCw, Search, X } from "lucide-react";
import { useState } from "react";
import { api } from "../api";
import type { ConnectionInput, DatabaseConnection } from "../types";
import { Badge, Button, Card, DatabaseLogo } from "../components/UI";

export function Connections({
  connections,
  onAdd,
}: {
  connections: DatabaseConnection[];
  onAdd: (connection: DatabaseConnection) => void;
}) {
  const [showForm, setShowForm] = useState(false);
  const [testing, setTesting] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [form, setForm] = useState({ name: "", host: "", port: "5432", username: "", password: "", sslMode: "prefer" });
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState("");
  const filtered = connections.filter((item) => `${item.name} ${item.host} ${item.type}`.toLowerCase().includes(query.toLowerCase()));

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setSubmitting(true);
    setFormError("");
    try {
      const input: ConnectionInput = { ...form, port: Number(form.port) };
      const connection = await api.createConnection(input);
      onAdd(connection);
      setShowForm(false);
      setForm({ name: "", host: "", port: "5432", username: "", password: "", sslMode: "prefer" });
    } catch (error) {
      setFormError(error instanceof Error ? error.message : "连接失败");
    } finally {
      setSubmitting(false);
    }
  }

  function testConnection(id: string) {
    setTesting(id);
    window.setTimeout(() => setTesting(null), 900);
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
      <div className="connections-grid">
        {filtered.map((connection) => (
          <Card className="connection-card" key={connection.id}>
            <div className="connection-card-head">
              <DatabaseLogo type={connection.type} />
              <div><strong>{connection.name}</strong><span>{connection.type}</span></div>
              <button className="plain-icon"><MoreHorizontal /></button>
            </div>
            <dl><div><dt>主机地址</dt><dd>{connection.host}:{connection.port}</dd></div><div><dt>连接用户</dt><dd>{connection.username}</dd></div></dl>
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
            {formError && <div className="form-error left">{formError}</div>}
            <div className="modal-actions"><Button type="button" variant="secondary" onClick={() => setShowForm(false)}>取消</Button><Button type="submit" disabled={submitting}>{submitting ? "正在测试连接..." : "测试并保存"}</Button></div>
          </form>
        </div>
      )}
    </div>
  );
}
