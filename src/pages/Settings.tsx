import { Bell, CheckCircle2, Pencil, Plus, Save, Send, Trash2, X } from "lucide-react";
import { useEffect, useState } from "react";
import { api } from "../api";
import type { NotificationRecipient, NotificationRecipientInput, NotificationRecipientType, SMTPSettings } from "../types";
import { Button, Card, SelectField } from "../components/UI";
import { NotificationRecipientTypeIcon } from "../components/NotificationRecipientTypeIcon";

const emptySMTP: SMTPSettings = { enabled: false, host: "", port: 587, username: "", password: "", from: "" };
const emptyRecipient: NotificationRecipientInput = { name: "", type: "feishu", target: "", enabled: true };

const recipientTypes: Array<{ value: NotificationRecipientType; label: string }> = [
  { value: "feishu", label: "飞书机器人" },
  { value: "dingtalk", label: "钉钉机器人" },
  { value: "wechat_work", label: "企业微信机器人" },
  { value: "telegram", label: "Telegram 机器人" },
  { value: "email", label: "邮箱" },
  { value: "webhook", label: "通用 Webhook" },
];

export function Settings() {
  const [recipients, setRecipients] = useState<NotificationRecipient[]>([]);
  const [smtp, setSMTP] = useState<SMTPSettings>(emptySMTP);
  const [loading, setLoading] = useState(true);
  const [savingSMTP, setSavingSMTP] = useState(false);
  const [smtpSaved, setSMTPSaved] = useState(false);
  const [editing, setEditing] = useState<NotificationRecipient | null | "new">(null);
  const [recipientForm, setRecipientForm] = useState<NotificationRecipientInput>(emptyRecipient);
  const [savingRecipient, setSavingRecipient] = useState(false);
  const [testingRecipientForm, setTestingRecipientForm] = useState(false);
  const [recipientTestResult, setRecipientTestResult] = useState<{ tone: "success" | "error"; message: string } | null>(null);
  const [testingId, setTestingId] = useState("");
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    Promise.all([api.notificationRecipients(), api.smtpSettings()])
      .then(([items, settings]) => {
        setRecipients(items);
        setSMTP({ ...emptySMTP, ...settings, password: "" });
      })
      .catch((reason) => setError(reason instanceof Error ? reason.message : "读取通知配置失败"))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    if (message || error) window.scrollTo({ top: 0 });
  }, [message, error]);

  function openRecipient(item?: NotificationRecipient) {
    setError("");
    setMessage("");
    setRecipientTestResult(null);
    setEditing(item ?? "new");
    setRecipientForm(item ? { name: item.name, type: item.type, target: "", botToken: "", chatId: "", enabled: item.enabled } : emptyRecipient);
  }

  async function saveRecipient(event: React.FormEvent) {
    event.preventDefault();
    setSavingRecipient(true);
    setError("");
    try {
      const input = { ...recipientForm, name: recipientForm.name.trim(), target: recipientForm.target?.trim(), botToken: recipientForm.botToken?.trim(), chatId: recipientForm.chatId?.trim() };
      const saved = editing === "new"
        ? await api.createNotificationRecipient(input)
        : await api.updateNotificationRecipient((editing as NotificationRecipient).id, input);
      setRecipients((current) => editing === "new" ? [saved, ...current] : current.map((item) => item.id === saved.id ? saved : item));
      setEditing(null);
      setMessage(editing === "new" ? "接收者已创建" : "接收者已更新");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "保存接收者失败");
    } finally {
      setSavingRecipient(false);
    }
  }

  async function removeRecipient(item: NotificationRecipient) {
    if (!window.confirm(`确定删除接收者“${item.name}”吗？`)) return;
    setError("");
    try {
      await api.deleteNotificationRecipient(item.id);
      setRecipients((current) => current.filter((recipient) => recipient.id !== item.id));
      setMessage("接收者已删除");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "删除接收者失败");
    }
  }

  async function testRecipient(item: NotificationRecipient) {
    setTestingId(item.id);
    setError("");
    setMessage("");
    try {
      const result = await api.testNotificationRecipient(item.id);
      setMessage(`${item.name}：${result.message}`);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "测试通知发送失败");
    } finally {
      setTestingId("");
    }
  }

  async function testRecipientForm() {
    setTestingRecipientForm(true);
    setRecipientTestResult(null);
    try {
      const result = await api.testNotificationRecipientInput({
        ...recipientForm,
        id: editing !== null && editing !== "new" ? editing.id : undefined,
        name: recipientForm.name.trim(),
        target: recipientForm.target?.trim(),
        botToken: recipientForm.botToken?.trim(),
        chatId: recipientForm.chatId?.trim(),
      });
      setRecipientTestResult({ tone: "success", message: result.message });
    } catch (reason) {
      setRecipientTestResult({ tone: "error", message: reason instanceof Error ? reason.message : "测试通知发送失败" });
    } finally {
      setTestingRecipientForm(false);
    }
  }

  async function submitSMTP(event: React.FormEvent) {
    event.preventDefault();
    setSavingSMTP(true);
    setSMTPSaved(false);
    setError("");
    try {
      const updated = await api.updateSMTPSettings({ ...smtp, host: smtp.host.trim(), username: smtp.username?.trim(), from: smtp.from.trim() });
      setSMTP({ ...emptySMTP, ...updated, password: "" });
      setSMTPSaved(true);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "保存 SMTP 配置失败");
    } finally {
      setSavingSMTP(false);
    }
  }

  const recipientTypeChanged = editing !== null && editing !== "new" && recipientForm.type !== editing.type;
  const editingSameType = editing !== null && editing !== "new" && !recipientTypeChanged;
  const telegramCredentialsReady = recipientForm.type === "telegram" && ((recipientForm.botToken?.trim() !== "" && recipientForm.chatId?.trim() !== "") || editingSameType);
  const targetReady = recipientForm.type !== "telegram" && (recipientForm.target?.trim() !== "" || editingSameType);
  const canTestRecipientForm = recipientForm.name.trim() !== "" && (telegramCredentialsReady || targetReady);

  return (
    <div className="page-stack settings-page">
      {error && <div className="error-banner">{error}</div>}
      {message && <div className="success-banner">{message}</div>}
      <Card className="settings-card recipients-card">
        <div className="section-title compact recipients-heading">
          <div><h3>通知接收者</h3><p>集中配置机器人和邮箱，在备份任务中直接选择。</p></div>
          <Button onClick={() => openRecipient()}><Plus size={15} />新增接收者</Button>
        </div>
        {loading ? <div className="loading-copy">正在加载通知接收者...</div> : recipients.length === 0 ? (
          <div className="recipient-empty"><Bell size={26} /><strong>还没有通知接收者</strong><span>添加飞书、钉钉、企业微信、Telegram、邮箱或通用 Webhook。</span><Button variant="secondary" onClick={() => openRecipient()}><Plus size={15} />添加第一个接收者</Button></div>
        ) : (
          <div className="recipient-list">
            {recipients.map((item) => (
              <div className="recipient-row" key={item.id}>
                <div className={`recipient-icon ${item.type}`}><NotificationRecipientTypeIcon type={item.type} /></div>
                <div className="recipient-copy"><strong>{item.name}</strong><span>{recipientTypeLabel(item.type)} · {item.targetMasked}</span></div>
                <span className={item.enabled ? "recipient-status enabled" : "recipient-status"}>{item.enabled ? "已启用" : "已停用"}</span>
                <div className="recipient-actions">
                  <button type="button" title="发送测试通知" disabled={testingId === item.id} onClick={() => testRecipient(item)}><Send size={15} /><span>{testingId === item.id ? "测试中" : "测试"}</span></button>
                  <button type="button" title="编辑接收者" onClick={() => openRecipient(item)}><Pencil size={15} /><span>编辑</span></button>
                  <button type="button" className="danger" title="删除接收者" onClick={() => removeRecipient(item)}><Trash2 size={15} /><span>删除</span></button>
                </div>
              </div>
            ))}
          </div>
        )}
      </Card>

      <Card className="settings-card">
        <div className="section-title compact"><div><h3>邮件发送服务</h3><p>邮箱接收者通过这里配置的 SMTP 服务发送通知。</p></div></div>
        {loading ? <div className="loading-copy">正在加载 SMTP 配置...</div> : (
          <form className="settings-form" onSubmit={submitSMTP}>
            <button type="button" className="toggle-row" onClick={() => setSMTP({ ...smtp, enabled: !smtp.enabled })}><span className={`switch ${smtp.enabled ? "on" : ""}`}><i /></span><div><strong>启用 SMTP</strong><small>启用后可以向邮箱类型的接收者发送通知</small></div></button>
            <div className="settings-grid">
              <label className="field"><span>SMTP 主机</span><input value={smtp.host} onChange={(event) => setSMTP({ ...smtp, host: event.target.value })} placeholder="smtp.example.com" /></label>
              <label className="field"><span>端口</span><input type="number" min={1} max={65535} value={smtp.port} onChange={(event) => setSMTP({ ...smtp, port: Number(event.target.value) })} /></label>
              <label className="field"><span>用户名</span><input value={smtp.username ?? ""} onChange={(event) => setSMTP({ ...smtp, username: event.target.value })} placeholder="smtp-user" /></label>
              <label className="field"><span>密码</span><input type="password" value={smtp.password ?? ""} onChange={(event) => setSMTP({ ...smtp, password: event.target.value })} placeholder={smtp.passwordSet ? "留空则保留已保存密码" : "SMTP 密码"} /></label>
              <label className="field settings-wide"><span>发件人</span><input value={smtp.from} onChange={(event) => setSMTP({ ...smtp, from: event.target.value })} placeholder="DataBridge <noreply@example.com>" /></label>
            </div>
            <div className="settings-actions"><span>{smtp.passwordSet ? "已保存 SMTP 密码" : "尚未保存 SMTP 密码"}</span>{smtpSaved && <em><CheckCircle2 size={14} />已保存</em>}<Button disabled={savingSMTP} type="submit"><Save size={15} />{savingSMTP ? "保存中..." : "保存邮件配置"}</Button></div>
          </form>
        )}
      </Card>

      {editing && (
        <div className="modal-backdrop">
          <form className="modal recipient-editor-modal" onSubmit={saveRecipient}>
            <div className="modal-title"><div><h3>{editing === "new" ? "新增通知接收者" : "编辑通知接收者"}</h3><p>凭证和 Webhook 地址会加密保存在服务端。</p></div><button type="button" className="icon-button" aria-label="关闭" onClick={() => setEditing(null)}><X size={17} /></button></div>
            <label className="field"><span>接收者名称</span><input required value={recipientForm.name} onChange={(event) => setRecipientForm({ ...recipientForm, name: event.target.value })} placeholder="例如：运维群-钉钉" /></label>
            <SelectField label="接收者类型" value={recipientForm.type} onChange={(type) => setRecipientForm({ ...recipientForm, type: type as NotificationRecipientType, target: "", botToken: "", chatId: "" })}>{recipientTypes.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</SelectField>
            {recipientForm.type === "telegram" ? (
              <div className="telegram-recipient-fields">
                <label className="field"><span>Bot Token</span><input required={editing === "new" || recipientTypeChanged} type="password" autoComplete="new-password" value={recipientForm.botToken ?? ""} onChange={(event) => setRecipientForm({ ...recipientForm, botToken: event.target.value })} placeholder={editingSameType ? "留空则保留当前 Token" : "例如：123456789:AA..."} /></label>
                <label className="field"><span>Chat ID</span><input required={editing === "new" || recipientTypeChanged} value={recipientForm.chatId ?? ""} onChange={(event) => setRecipientForm({ ...recipientForm, chatId: event.target.value })} placeholder={editingSameType ? "留空则保留当前 Chat ID" : "例如：-1001234567890 或 @channel"} /></label>
              </div>
            ) : (
              <label className="field"><span>{recipientForm.type === "email" ? "收件邮箱" : "Webhook URL"}</span><input required={editing === "new" || recipientTypeChanged} type={recipientForm.type === "email" ? "email" : "url"} value={recipientForm.target ?? ""} onChange={(event) => setRecipientForm({ ...recipientForm, target: event.target.value })} placeholder={editing === "new" ? recipientTargetPlaceholder(recipientForm.type) : recipientTypeChanged ? "切换类型后需要填写新的通知目标" : "留空则保留当前配置"} /></label>
            )}
            <button type="button" className="toggle-row compact-toggle" onClick={() => setRecipientForm({ ...recipientForm, enabled: !recipientForm.enabled })}><span className={`switch ${recipientForm.enabled ? "on" : ""}`}><i /></span><div><strong>启用接收者</strong><small>停用后任务不会向该接收者发送通知</small></div></button>
            {recipientTestResult && <div className={`recipient-test-feedback ${recipientTestResult.tone}`}>{recipientTestResult.tone === "success" && <CheckCircle2 size={15} />}{recipientTestResult.message}</div>}
            <div className="modal-actions recipient-modal-actions"><Button className="recipient-test-button" type="button" variant="secondary" disabled={!canTestRecipientForm || testingRecipientForm || savingRecipient} onClick={testRecipientForm}><Send size={15} />{testingRecipientForm ? "测试中..." : "测试发送"}</Button><Button type="button" variant="secondary" onClick={() => setEditing(null)}>取消</Button><Button disabled={savingRecipient || testingRecipientForm} type="submit">{savingRecipient ? "保存中..." : "保存接收者"}</Button></div>
          </form>
        </div>
      )}
    </div>
  );
}

export function recipientTypeLabel(type: NotificationRecipientType) {
  return recipientTypes.find((item) => item.value === type)?.label ?? type;
}

function recipientTargetPlaceholder(type: NotificationRecipientType) {
  if (type === "email") return "ops@example.com";
  if (type === "dingtalk") return "https://oapi.dingtalk.com/robot/send?...";
  if (type === "wechat_work") return "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?...";
  if (type === "feishu") return "https://open.feishu.cn/open-apis/bot/v2/hook/...";
  return "https://example.com/webhook";
}
