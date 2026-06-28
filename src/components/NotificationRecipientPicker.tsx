import { useEffect, useState } from "react";
import { api } from "../api";
import type { NotificationRecipient } from "../types";
import { NotificationRecipientTypeIcon } from "./NotificationRecipientTypeIcon";

export function useNotificationRecipients() {
  const [recipients, setRecipients] = useState<NotificationRecipient[]>([]);
  const [loadingRecipients, setLoadingRecipients] = useState(true);
  useEffect(() => {
    let cancelled = false;
    api.notificationRecipients()
      .then((items) => { if (!cancelled) setRecipients(items); })
      .finally(() => { if (!cancelled) setLoadingRecipients(false); });
    return () => { cancelled = true; };
  }, []);
  return { recipients, loadingRecipients };
}

export function NotificationRecipientPicker({ recipients, selected, onChange, loading = false }: { recipients: NotificationRecipient[]; selected: string[]; onChange: (ids: string[]) => void; loading?: boolean }) {
  const available = recipients.filter((item) => item.enabled);
  function toggle(id: string) {
    onChange(selected.includes(id) ? selected.filter((item) => item !== id) : [...selected, id]);
  }
  if (loading) return <div className="recipient-picker-empty">正在加载接收者...</div>;
  if (!available.length) return <div className="recipient-picker-empty">尚未配置可用接收者。<a href="/settings/recipients">前往系统设置添加</a></div>;
  return (
    <div className="recipient-picker" role="group" aria-label="通知接收者">
      {available.map((item) => (
        <button type="button" className={selected.includes(item.id) ? "recipient-option selected" : "recipient-option"} key={item.id} aria-pressed={selected.includes(item.id)} onClick={() => toggle(item.id)}>
          <span className={`recipient-option-icon ${item.type}`}><NotificationRecipientTypeIcon type={item.type} size={16} /></span>
          <span><strong>{item.name}</strong><small>{item.targetMasked}</small></span>
          <i>{selected.includes(item.id) ? "✓" : ""}</i>
        </button>
      ))}
    </div>
  );
}
