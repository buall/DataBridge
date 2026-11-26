import { Bell, CheckCircle2, Mail, Pencil, Plus, Save, Send, Trash2, Type, X } from "lucide-react";
import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { api } from "../api";
import { NotificationRecipientTypeIcon } from "../components/NotificationRecipientTypeIcon";
import { Badge, Button, Card, ConfirmDialog, requiredLabel, SelectField } from "../components/UI";
import type {
  NotificationRecipient,
  NotificationRecipientInput,
  NotificationRecipientType,
  NotificationSettings,
  SMTPSettings,
} from "../types";

const emptySMTP: SMTPSettings = { enabled: false, host: "", port: 587, username: "", password: "", from: "" };
const emptyNotificationSettings: NotificationSettings = { messageTitle: "" };
const emptyRecipient: NotificationRecipientInput = {
  name: "",
  type: "feishu",
  target: "",
  messageTitle: "",
  enabled: true,
};

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
  const [notificationSettings, setNotificationSettings] = useState<NotificationSettings>(emptyNotificationSettings);
  const [loading, setLoading] = useState(true);
  const [savingSMTP, setSavingSMTP] = useState(false);
  const [smtpSaved, setSMTPSaved] = useState(false);
  const [savingNotificationSettings, setSavingNotificationSettings] = useState(false);
  const [notificationSettingsSaved, setNotificationSettingsSaved] = useState(false);
  const [editing, setEditing] = useState<NotificationRecipient | null | "new">(null);
  const [recipientForm, setRecipientForm] = useState<NotificationRecipientInput>(emptyRecipient);
  const [savingRecipient, setSavingRecipient] = useState(false);
  const [testingRecipientForm, setTestingRecipientForm] = useState(false);
  const [recipientTestResult, setRecipientTestResult] = useState<{
    tone: "success" | "error";
    message: string;
  } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<NotificationRecipient | null>(null);
  const [deletingRecipient, setDeletingRecipient] = useState(false);
  const [testingId, setTestingId] = useState("");
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    Promise.all([api.notificationRecipients(), api.smtpSettings(), api.notificationSettings()])
      .then(([items, settings, notification]) => {
        setRecipients(items);
        setSMTP({ ...emptySMTP, ...settings, password: "" });
        setNotificationSettings({ ...emptyNotificationSettings, ...notification });
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
    setRecipientForm(
      item
        ? {
            name: item.name,
            type: item.type,
            target: "",
            messageTitle: item.messageTitle ?? "",
            botToken: "",
            chatId: "",
            enabled: item.enabled,
          }
        : emptyRecipient,
    );
  }

  async function saveRecipient(event: React.FormEvent) {
    event.preventDefault();
    setSavingRecipient(true);
    setError("");
    try {
      const input = {
        ...recipientForm,
        name: recipientForm.name.trim(),
        messageTitle: recipientForm.messageTitle?.trim(),
        target: recipientForm.target?.trim(),
        botToken: recipientForm.botToken?.trim(),
        chatId: recipientForm.chatId?.trim(),
      };
      const saved =
        editing === "new"
          ? await api.createNotificationRecipient(input)
          : await api.updateNotificationRecipient((editing as NotificationRecipient).id, input);
      setRecipients((current) =>
        editing === "new" ? [saved, ...current] : current.map((item) => (item.id === saved.id ? saved : item)),
      );
      setEditing(null);
      setMessage(editing === "new" ? "接收者已创建" : "接收者已更新");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "保存接收者失败");
    } finally {
      setSavingRecipient(false);
    }
  }

  async function confirmRemoveRecipient() {
    if (!deleteTarget) return;
    setDeletingRecipient(true);
    setError("");
    try {
      await api.deleteNotificationRecipient(deleteTarget.id);
      setRecipients((current) => current.filter((recipient) => recipient.id !== deleteTarget.id));
      setDeleteTarget(null);
      setMessage("接收者已删除");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "删除接收者失败");
    } finally {
      setDeletingRecipient(false);
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
        messageTitle: recipientForm.messageTitle?.trim(),
        target: recipientForm.target?.trim(),
        botToken: recipientForm.botToken?.trim(),
        chatId: recipientForm.chatId?.trim(),
      });
      setRecipientTestResult({ tone: "success", message: result.message });
    } catch (reason) {
      setRecipientTestResult({
        tone: "error",
        message: reason instanceof Error ? reason.message : "测试通知发送失败",
      });
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
      const updated = await api.updateSMTPSettings({
        ...smtp,
        host: smtp.host.trim(),
        username: smtp.username?.trim(),
        from: smtp.from.trim(),
      });
      setSMTP({ ...emptySMTP, ...updated, password: "" });
      setSMTPSaved(true);
      setMessage("邮件发送服务配置已保存");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "保存 SMTP 配置失败");
    } finally {
      setSavingSMTP(false);
    }
  }

  async function submitNotificationSettings(event: React.FormEvent) {
    event.preventDefault();
    setSavingNotificationSettings(true);
    setNotificationSettingsSaved(false);
    setError("");
    try {
      const updated = await api.updateNotificationSettings({
        messageTitle: notificationSettings.messageTitle?.trim(),
      });
      setNotificationSettings({ ...emptyNotificationSettings, ...updated });
      setNotificationSettingsSaved(true);
      setMessage("通知默认标题已保存");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "保存通知配置失败");
    } finally {
      setSavingNotificationSettings(false);
    }
  }

  function jumpTo(id: string) {
    document.getElementById(id)?.scrollIntoView({ behavior: "smooth", block: "start" });
  }

  const recipientTypeChanged = editing !== null && editing !== "new" && recipientForm.type !== editing.type;
  const editingSameType = editing !== null && editing !== "new" && !recipientTypeChanged;
  const telegramCredentialsReady =
    recipientForm.type === "telegram" &&
    ((recipientForm.botToken?.trim() !== "" && recipientForm.chatId?.trim() !== "") || editingSameType);
  const targetReady = recipientForm.type !== "telegram" && (recipientForm.target?.trim() !== "" || editingSameType);
  const canTestRecipientForm = recipientForm.name.trim() !== "" && (telegramCredentialsReady || targetReady);

  return (
    <div className="page-stack">
      <div className="h-row">
        <div className="page-h">
          <h2>系统设置</h2>
          <p>集中管理通知接收者、SMTP 邮件发送服务与消息标题模板</p>
        </div>
      </div>

      {error ? <div className="error-banner">{error}</div> : null}
      {message ? <div className="success-banner">{message}</div> : null}

      <div className="settings-layout">
        <nav className="set-nav" aria-label="设置分组">
          <button type="button" onClick={() => jumpTo("set-recipients")}>
            <Bell size={15} />
            通知接收者
          </button>
          <button type="button" onClick={() => jumpTo("set-smtp")}>
            <Mail size={15} />
            邮件发送服务
          </button>
          <button type="button" onClick={() => jumpTo("set-title")}>
            <Type size={15} />
            通知默认标题
          </button>
        </nav>

        <div style={{ display: "grid", gap: 16, minWidth: 0 }}>
          <Card id="set-recipients" className="pad set-panel">
            <div className="h-row" style={{ marginBottom: 14 }}>
              <div>
                <div className="sec-title">通知接收者</div>
                <div className="sec-sub">集中配置机器人和邮箱，在备份或迁移任务中直接勾选引用</div>
              </div>
              <div className="sp" />
              <Button onClick={() => openRecipient()}>
                <Plus size={15} />
                新增接收者
              </Button>
            </div>

            {loading ? (
              <div className="info-callout">正在加载通知接收者...</div>
            ) : recipients.length === 0 ? (
              <div className="empty" style={{ padding: "28px 16px" }}>
                <Bell size={26} style={{ color: "var(--muted)", marginBottom: 8 }} />
                <div className="e-t">还没有通知接收者</div>
                <div className="e-s">添加飞书、钉钉、企业微信、Telegram、邮箱或通用 Webhook。</div>
                <Button variant="secondary" onClick={() => openRecipient()}>
                  <Plus size={15} />
                  添加第一个接收者
                </Button>
              </div>
            ) : (
              <div className="ledger">
                {recipients.map((item) => (
                  <div className="lrow" key={item.id}>
                    <span
                      style={{
                        width: 34,
                        height: 34,
                        borderRadius: 9,
                        background: "var(--surface)",
                        border: "1px solid var(--hair)",
                        display: "inline-flex",
                        alignItems: "center",
                        justifyContent: "center",
                        flexShrink: 0,
                      }}
                    >
                      <NotificationRecipientTypeIcon type={item.type} />
                    </span>
                    <div className="lr-main">
                      <div className="lr-t">
                        <span>{item.name}</span>
                        <Badge tone={item.enabled ? "green" : "gray"}>
                          {item.enabled ? "已启用" : "已停用"}
                        </Badge>
                      </div>
                      <div className="lr-s mono">
                        {recipientTypeLabel(item.type)} · {item.targetMasked}
                        {item.messageTitle ? ` · 标题：${item.messageTitle}` : ""}
                      </div>
                    </div>
                    <div style={{ display: "flex", gap: 6, flexWrap: "wrap", alignItems: "center" }}>
                      <Button
                        variant="secondary"
                        size="sm"
                        data-tooltip-content="发送测试通知"
                        disabled={testingId === item.id}
                        onClick={() => void testRecipient(item)}
                      >
                        <Send size={13} />
                        {testingId === item.id ? "测试中" : "测试"}
                      </Button>
                      <Button
                        variant="secondary"
                        size="sm"
                        data-tooltip-content="编辑接收者"
                        onClick={() => openRecipient(item)}
                      >
                        <Pencil size={13} />
                        编辑
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        data-tooltip-content="删除接收者"
                        style={{ color: "var(--danger)" }}
                        onClick={() => setDeleteTarget(item)}
                      >
                        <Trash2 size={13} />
                        删除
                      </Button>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </Card>

          <Card id="set-smtp" className="pad set-panel">
            <div style={{ marginBottom: 14 }}>
              <div className="sec-title">邮件发送服务</div>
              <div className="sec-sub">邮箱接收者通过这里配置的 SMTP 服务发送任务通知</div>
            </div>

            {loading ? (
              <div className="info-callout">正在加载 SMTP 配置...</div>
            ) : (
              <form onSubmit={submitSMTP} style={{ display: "grid", gap: 14 }}>
                <button
                  type="button"
                  className="toggle-row"
                  onClick={() => setSMTP({ ...smtp, enabled: !smtp.enabled })}
                >
                  <span className={`switch ${smtp.enabled ? "on" : ""}`}>
                    <i />
                  </span>
                  <div>
                    <strong>启用 SMTP</strong>
                    <small>启用后可以向邮箱类型的接收者发送通知</small>
                  </div>
                </button>

                <div className="grid2">
                  <label className="field">
                    <span>SMTP 主机</span>
                    <input
                      value={smtp.host}
                      onChange={(event) => setSMTP({ ...smtp, host: event.target.value })}
                      placeholder="smtp.example.com"
                    />
                  </label>
                  <label className="field">
                    <span>端口</span>
                    <input
                      type="number"
                      min={1}
                      max={65535}
                      value={smtp.port}
                      onChange={(event) => setSMTP({ ...smtp, port: Number(event.target.value) })}
                    />
                  </label>
                  <label className="field">
                    <span>用户名</span>
                    <input
                      value={smtp.username ?? ""}
                      onChange={(event) => setSMTP({ ...smtp, username: event.target.value })}
                      placeholder="smtp-user"
                    />
                  </label>
                  <label className="field">
                    <span>密码</span>
                    <input
                      type="password"
                      value={smtp.password ?? ""}
                      onChange={(event) => setSMTP({ ...smtp, password: event.target.value })}
                      placeholder={smtp.passwordSet ? "留空则保留已保存密码" : "SMTP 密码"}
                    />
                  </label>
                </div>

                <label className="field" style={{ marginBottom: 0 }}>
                  <span>发件人</span>
                  <input
                    value={smtp.from}
                    onChange={(event) => setSMTP({ ...smtp, from: event.target.value })}
                    placeholder="DataBridge <noreply@example.com>"
                  />
                </label>

                <div className="form-actions">
                  <div className="fa-meta">
                    <span>{smtp.passwordSet ? "已保存 SMTP 密码" : "尚未保存 SMTP 密码"}</span>
                    {smtpSaved ? (
                      <Badge tone="green">
                        <CheckCircle2 size={12} />
                        已保存
                      </Badge>
                    ) : null}
                  </div>
                  <Button disabled={savingSMTP} type="submit">
                    <Save size={14} />
                    {savingSMTP ? "保存中..." : "保存邮件配置"}
                  </Button>
                </div>
              </form>
            )}
          </Card>

          <Card id="set-title" className="pad set-panel">
            <div style={{ marginBottom: 14 }}>
              <div className="sec-title">通知默认标题</div>
              <div className="sec-sub">所有备份通知默认使用这个项目名，单个接收者可在编辑时单独覆盖</div>
            </div>

            {loading ? (
              <div className="info-callout">正在加载通知配置...</div>
            ) : (
              <form onSubmit={submitNotificationSettings} style={{ display: "grid", gap: 12 }}>
                <label className="field" style={{ marginBottom: 0 }}>
                  <span>默认消息标题</span>
                  <input
                    value={notificationSettings.messageTitle ?? ""}
                    onChange={(event) =>
                      setNotificationSettings({ ...notificationSettings, messageTitle: event.target.value })
                    }
                    placeholder="留空默认 DataBridge，例如：业务系统名称"
                  />
                </label>

                <div
                  className="info-callout"
                  style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 10 }}
                >
                  <span style={{ fontSize: 12.5 }}>发送标题将显示为</span>
                  <strong>{`${notificationSettings.messageTitle?.trim() || "DataBridge"} 备份成功`}</strong>
                </div>

                <div className="form-actions">
                  {notificationSettingsSaved ? (
                    <div className="fa-meta">
                      <Badge tone="green">
                        <CheckCircle2 size={12} />
                        已保存
                      </Badge>
                    </div>
                  ) : null}
                  <Button disabled={savingNotificationSettings} type="submit">
                    <Save size={14} />
                    {savingNotificationSettings ? "保存中..." : "保存默认标题"}
                  </Button>
                </div>
              </form>
            )}
          </Card>
        </div>
      </div>

      {editing
        ? createPortal(
            <div className="modal-backdrop" onMouseDown={() => setEditing(null)}>
              <form
                className="modal"
                onSubmit={saveRecipient}
                onMouseDown={(event) => event.stopPropagation()}
              >
                <div className="modal-h">
                  <div>
                    <h3>{editing === "new" ? "新增通知接收者" : "编辑通知接收者"}</h3>
                    <div className="ms">凭证和 Webhook 地址会加密保存在服务端</div>
                  </div>
                  <button type="button" className="icon-btn ghost" aria-label="关闭" onClick={() => setEditing(null)}>
                    <X size={17} />
                  </button>
                </div>

                <div className="modal-b">
                  <label className="field">
                    <span>{requiredLabel("接收者名称")}</span>
                    <input
                      required
                      value={recipientForm.name}
                      onChange={(event) => setRecipientForm({ ...recipientForm, name: event.target.value })}
                      placeholder="例如：运维群-钉钉"
                    />
                  </label>
                  <label className="field">
                    <span>消息标题</span>
                    <input
                      value={recipientForm.messageTitle ?? ""}
                      onChange={(event) => setRecipientForm({ ...recipientForm, messageTitle: event.target.value })}
                      placeholder="留空使用全局默认标题，例如：业务系统名称"
                    />
                  </label>
                  <SelectField
                    label={requiredLabel("接收者类型")}
                    value={recipientForm.type}
                    onChange={(type) =>
                      setRecipientForm({
                        ...recipientForm,
                        type: type as NotificationRecipientType,
                        target: "",
                        botToken: "",
                        chatId: "",
                      })
                    }
                  >
                    {recipientTypes.map((item) => (
                      <option key={item.value} value={item.value}>
                        {item.label}
                      </option>
                    ))}
                  </SelectField>

                  {recipientForm.type === "telegram" ? (
                    <div className="grid2">
                      <label className="field">
                        <span>{editing === "new" || recipientTypeChanged ? requiredLabel("Bot Token") : "Bot Token"}</span>
                        <input
                          required={editing === "new" || recipientTypeChanged}
                          type="password"
                          autoComplete="new-password"
                          value={recipientForm.botToken ?? ""}
                          onChange={(event) => setRecipientForm({ ...recipientForm, botToken: event.target.value })}
                          placeholder={editingSameType ? "留空则保留当前 Token" : "例如：123456789:AA..."}
                        />
                      </label>
                      <label className="field">
                        <span>{editing === "new" || recipientTypeChanged ? requiredLabel("Chat ID") : "Chat ID"}</span>
                        <input
                          required={editing === "new" || recipientTypeChanged}
                          value={recipientForm.chatId ?? ""}
                          onChange={(event) => setRecipientForm({ ...recipientForm, chatId: event.target.value })}
                          placeholder={editingSameType ? "留空则保留当前 Chat ID" : "例如：-1001234567890 或 @channel"}
                        />
                      </label>
                    </div>
                  ) : (
                    <label className="field">
                      <span>
                        {editing === "new" || recipientTypeChanged
                          ? requiredLabel(recipientForm.type === "email" ? "收件邮箱" : "Webhook URL")
                          : recipientForm.type === "email"
                            ? "收件邮箱"
                            : "Webhook URL"}
                      </span>
                      <input
                        required={editing === "new" || recipientTypeChanged}
                        type={recipientForm.type === "email" ? "email" : "url"}
                        value={recipientForm.target ?? ""}
                        onChange={(event) => setRecipientForm({ ...recipientForm, target: event.target.value })}
                        placeholder={
                          editing === "new"
                            ? recipientTargetPlaceholder(recipientForm.type)
                            : recipientTypeChanged
                              ? "切换类型后需要填写新的通知目标"
                              : "留空则保留当前配置"
                        }
                      />
                    </label>
                  )}

                  <button
                    type="button"
                    className="toggle-row"
                    style={{ marginBottom: recipientTestResult ? 12 : 0 }}
                    onClick={() => setRecipientForm({ ...recipientForm, enabled: !recipientForm.enabled })}
                  >
                    <span className={`switch ${recipientForm.enabled ? "on" : ""}`}>
                      <i />
                    </span>
                    <div>
                      <strong>启用接收者</strong>
                      <small>停用后任务不会向该接收者发送通知</small>
                    </div>
                  </button>

                  {recipientTestResult ? (
                    <div className={recipientTestResult.tone === "success" ? "success-banner" : "error-banner"}>
                      {recipientTestResult.tone === "success" ? (
                        <CheckCircle2 size={14} style={{ verticalAlign: "-2px", marginRight: 6 }} />
                      ) : null}
                      {recipientTestResult.message}
                    </div>
                  ) : null}
                </div>

                <div className="modal-f">
                  <Button
                    type="button"
                    variant="secondary"
                    disabled={!canTestRecipientForm || testingRecipientForm || savingRecipient}
                    onClick={() => void testRecipientForm()}
                    style={{ marginRight: "auto" }}
                  >
                    <Send size={14} />
                    {testingRecipientForm ? "测试中..." : "测试发送"}
                  </Button>
                  <Button type="button" variant="secondary" onClick={() => setEditing(null)}>
                    取消
                  </Button>
                  <Button disabled={savingRecipient || testingRecipientForm} type="submit">
                    {savingRecipient ? "保存中..." : "保存接收者"}
                  </Button>
                </div>
              </form>
            </div>,
            document.body,
          )
        : null}

      {deleteTarget ? (
        <ConfirmDialog
          title="删除通知接收者"
          description={`确定删除接收者“${deleteTarget.name}”吗？`}
          tone="danger"
          confirmText="删除接收者"
          submitting={deletingRecipient}
          onCancel={() => setDeleteTarget(null)}
          onConfirm={confirmRemoveRecipient}
          details={[
            `类型：${recipientTypeLabel(deleteTarget.type)}`,
            "删除后已配置该接收者的任务将不再向其发送通知。",
          ]}
        />
      ) : null}
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
