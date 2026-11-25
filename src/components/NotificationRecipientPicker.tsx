import { Check } from "lucide-react";
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
      .then((items) => {
        if (!cancelled) setRecipients(items);
      })
      .finally(() => {
        if (!cancelled) setLoadingRecipients(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);
  return { recipients, loadingRecipients };
}

export function NotificationRecipientPicker({
  recipients,
  selected,
  onChange,
  loading = false,
}: {
  recipients: NotificationRecipient[];
  selected: string[];
  onChange: (ids: string[]) => void;
  loading?: boolean;
}) {
  const available = recipients.filter((item) => item.enabled);

  function toggle(id: string) {
    onChange(selected.includes(id) ? selected.filter((item) => item !== id) : [...selected, id]);
  }

  if (loading) {
    return (
      <div className="info-callout" style={{ padding: "12px 14px", fontSize: 12.5 }}>
        正在加载通知接收者...
      </div>
    );
  }

  if (!available.length) {
    return (
      <div className="info-callout" style={{ padding: "12px 14px", fontSize: 12.5 }}>
        尚未配置可用接收者。
        <a href="/settings/recipients" style={{ marginLeft: 6, color: "var(--blue)", fontWeight: 600 }}>
          前往系统设置添加 →
        </a>
      </div>
    );
  }

  return (
    <div className="radio-cards" role="group" aria-label="通知接收者">
      {available.map((item) => {
        const isSelected = selected.includes(item.id);
        return (
          <button
            type="button"
            key={item.id}
            className={isSelected ? "radio-card on" : "radio-card"}
            aria-pressed={isSelected}
            onClick={() => toggle(item.id)}
            style={{
              display: "flex",
              alignItems: "center",
              gap: 10,
              flex: "1 1 210px",
              minWidth: 190,
              padding: "10px 12px",
            }}
          >
            <span
              style={{
                width: 30,
                height: 30,
                borderRadius: 8,
                background: "var(--surface)",
                border: "1px solid var(--hair)",
                display: "inline-flex",
                alignItems: "center",
                justifyContent: "center",
                flexShrink: 0,
              }}
            >
              <NotificationRecipientTypeIcon type={item.type} size={15} />
            </span>
            <span style={{ flex: 1, minWidth: 0 }}>
              <span
                className="rc-h"
                style={{ fontSize: 13, display: "block", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
              >
                {item.name}
              </span>
              <span
                className="rc-d mono"
                style={{ fontSize: 11.5, display: "block", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
              >
                {item.targetMasked}
              </span>
            </span>
            <span
              style={{
                width: 18,
                height: 18,
                borderRadius: 99,
                border: isSelected ? "1px solid var(--indigo)" : "1px solid var(--line)",
                background: isSelected ? "var(--indigo)" : "transparent",
                color: "#fff",
                display: "inline-flex",
                alignItems: "center",
                justifyContent: "center",
                flexShrink: 0,
              }}
            >
              {isSelected ? <Check size={11} strokeWidth={2.6} /> : null}
            </span>
          </button>
        );
      })}
    </div>
  );
}
