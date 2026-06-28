import { Mail, Webhook } from "lucide-react";
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

export function NotificationRecipientTypeIcon({ type, size = 18 }: { type: NotificationRecipientType; size?: number }) {
  const brandIcon = brandIcons[type];
  if (brandIcon) return <img className="recipient-brand-icon" src={brandIcon} width={size} height={size} alt="" aria-hidden="true" />;
  if (type === "email") return <Mail size={size} aria-hidden="true" />;
  return <Webhook size={size} aria-hidden="true" />;
}
