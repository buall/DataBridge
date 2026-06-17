import { CheckCircle2, Mail, Save } from "lucide-react";
import { useEffect, useState } from "react";
import { api } from "../api";
import type { SMTPSettings } from "../types";
import { Button, Card } from "../components/UI";

const emptySMTP: SMTPSettings = {
  enabled: false,
  host: "",
  port: 587,
  username: "",
  password: "",
  from: "",
};

export function Settings() {
  const [smtp, setSMTP] = useState<SMTPSettings>(emptySMTP);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    api.smtpSettings()
      .then((settings) => setSMTP({ ...emptySMTP, ...settings, password: "" }))
      .catch((reason) => setError(reason instanceof Error ? reason.message : "读取 SMTP 配置失败"))
      .finally(() => setLoading(false));
  }, []);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setSaving(true);
    setSaved(false);
    setError("");
    try {
      const updated = await api.updateSMTPSettings({
        ...smtp,
        host: smtp.host.trim(),
        username: smtp.username?.trim(),
        from: smtp.from.trim(),
      });
      setSMTP({ ...emptySMTP, ...updated, password: "" });
      setSaved(true);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "保存 SMTP 配置失败");
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="page-stack">
      {error && <div className="error-banner">{error}</div>}
      <Card className="settings-card">
        <div className="section-title compact"><div><h3>邮件服务</h3><p>定时备份选择 Email 通知时会使用这里的 SMTP 配置。</p></div></div>
        {loading ? <div className="loading-copy">正在加载 SMTP 配置...</div> : (
          <form className="settings-form" onSubmit={submit}>
            <button type="button" className="toggle-row" onClick={() => setSMTP({ ...smtp, enabled: !smtp.enabled })}><span className={`switch ${smtp.enabled ? "on" : ""}`}><i /></span><div><strong>启用 SMTP</strong><small>启用后可向备份任务配置的收件邮箱发送结果通知</small></div></button>
            <div className="settings-grid">
              <label className="field"><span>SMTP 主机</span><input value={smtp.host} onChange={(event) => setSMTP({ ...smtp, host: event.target.value })} placeholder="smtp.example.com" /></label>
              <label className="field"><span>端口</span><input type="number" min={1} max={65535} value={smtp.port} onChange={(event) => setSMTP({ ...smtp, port: Number(event.target.value) })} /></label>
              <label className="field"><span>用户名</span><input value={smtp.username ?? ""} onChange={(event) => setSMTP({ ...smtp, username: event.target.value })} placeholder="smtp-user" /></label>
              <label className="field"><span>密码</span><input type="password" value={smtp.password ?? ""} onChange={(event) => setSMTP({ ...smtp, password: event.target.value })} placeholder={smtp.passwordSet ? "留空则保留已保存密码" : "SMTP 密码"} /></label>
              <label className="field settings-wide"><span>发件人</span><input value={smtp.from} onChange={(event) => setSMTP({ ...smtp, from: event.target.value })} placeholder="DataBridge <noreply@example.com>" /></label>
            </div>
            <div className="settings-actions">
              <span>{smtp.passwordSet ? "已保存 SMTP 密码" : "尚未保存 SMTP 密码"}</span>
              {saved && <em><CheckCircle2 size={14} />已保存</em>}
              <Button disabled={saving} type="submit"><Save size={15} />{saving ? "保存中..." : "保存配置"}</Button>
            </div>
          </form>
        )}
      </Card>
      <Card className="settings-card helper-card">
        <Mail size={22} />
        <div><strong>Email 通知说明</strong><span>Webhook 通知不依赖 SMTP。Email 通知只会在定时备份执行完成后发送，发送失败会记录到任务事件中。</span></div>
      </Card>
    </div>
  );
}
