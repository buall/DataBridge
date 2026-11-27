import dingdingIcon from "../assets/icons/dingding.svg";
import larkIcon from "../assets/icons/lark.svg";
import telegramIcon from "../assets/icons/telegram.svg";
import wechatIcon from "../assets/icons/wechat.svg";
import type { NotificationRecipientType } from "../types";

const brandIcons: Partial<Record<NotificationRecipientType, string>> = {
  feishu: larkIcon,
  dingtalk: dingdingIcon,
  wechat_work: wechatIcon,
  telegram: telegramIcon,
};

function EmailLogo({ size }: { size: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" aria-hidden="true">
      <rect x="2.2" y="4.5" width="19.6" height="15" rx="2.6" fill="#0ea5e9" />
      <path d="M2.6 5.6 12 13l9.4-7.4a2.4 2.4 0 0 0-1.8-.9H4.4c-.7 0-1.3.3-1.8.9Z" fill="#38bdf8" />
      <path d="M2.2 17.2v-.2l6.2-5.4L12 14.5l3.6-2.9 6.2 5.4v.2a2.6 2.6 0 0 1-2.6 2.3H4.8a2.6 2.6 0 0 1-2.6-2.3Z" fill="#0284c7" />
    </svg>
  );
}

function WebhookLogo({ size }: { size: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" aria-hidden="true">
      <circle cx="12" cy="6.2" r="3.2" fill="#8b5cf6" />
      <circle cx="6.5" cy="16.5" r="3.2" fill="#6366f1" />
      <circle cx="17.5" cy="16.5" r="3.2" fill="#a855f7" />
      <path
        d="M10.4 8.9 7.8 13.7M13.6 8.9l2.6 4.8M9.7 16.5h4.6"
        stroke="#7c3aed"
        strokeWidth="2.1"
        strokeLinecap="round"
      />
      <circle cx="12" cy="6.2" r="1.2" fill="#ede9fe" />
      <circle cx="6.5" cy="16.5" r="1.2" fill="#e0e7ff" />
      <circle cx="17.5" cy="16.5" r="1.2" fill="#f3e8ff" />
    </svg>
  );
}

export function NotificationRecipientTypeIcon({ type, size = 24 }: { type: NotificationRecipientType; size?: number }) {
  const brandIcon = brandIcons[type];
  if (brandIcon) {
    return (
      <img
        className="recipient-brand-icon"
        src={brandIcon}
        width={size}
        height={size}
        style={{ width: size, height: size, display: "block" }}
        alt=""
        aria-hidden="true"
      />
    );
  }
  if (type === "email") return <EmailLogo size={size} />;
  return <WebhookLogo size={size} />;
}
