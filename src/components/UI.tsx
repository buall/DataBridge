import { Children, Fragment, isValidElement, useEffect, useMemo, useRef, useState, type ButtonHTMLAttributes, type ReactElement, type ReactNode } from "react";
import { AlertTriangle, ChevronDown, X } from "lucide-react";
import mysqlIcon from "../assets/icons/devicon--mysql.svg";
import postgresqlIcon from "../assets/icons/devicon--postgresql.svg";
import redisIcon from "../assets/icons/devicon--redis.svg";
import type { DatabaseType } from "../types";

type SelectOptionItem = { value: string; label: string };

export function Button({
  variant = "primary",
  children,
  className = "",
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: "primary" | "secondary" | "danger" | "ghost";
}) {
  return (
    <button className={`button ${variant} ${className}`} {...props}>
      {children}
    </button>
  );
}

export function Card({ children, className = "" }: { children: ReactNode; className?: string }) {
  return <section className={`card ${className}`}>{children}</section>;
}

export function ConfirmDialog({
  title,
  description,
  details = [],
  confirmText = "确认",
  tone = "danger",
  submitting = false,
  onCancel,
  onConfirm,
}: {
  title: string;
  description: string;
  details?: ReactNode[];
  confirmText?: string;
  tone?: "danger" | "warning";
  submitting?: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <div className="modal-backdrop confirm-dialog-backdrop" onMouseDown={onCancel}>
      <div className={`modal confirm-dialog ${tone}`} onMouseDown={(event) => event.stopPropagation()}>
        <div className="modal-head">
          <div className="confirm-dialog-title">
            <span><AlertTriangle size={18} /></span>
            <div><h3>{title}</h3><p>{description}</p></div>
          </div>
          <button type="button" className="plain-icon" onClick={onCancel}><X size={18} /></button>
        </div>
        {details.map((item, index) => <p className="delete-warning" key={index}>{item}</p>)}
        <div className="modal-actions"><Button type="button" variant="secondary" onClick={onCancel}>取消</Button><Button type="button" variant={tone === "danger" ? "danger" : "primary"} disabled={submitting} onClick={onConfirm}>{submitting ? "处理中..." : confirmText}</Button></div>
      </div>
    </div>
  );
}

export function Badge({
  children,
  tone = "blue",
}: {
  children: ReactNode;
  tone?: "blue" | "green" | "orange" | "red" | "gray";
}) {
  return <span className={`badge ${tone}`}>{children}</span>;
}

export function DatabaseLogo({ type, name, size = "md" }: { type: DatabaseType; name?: string; size?: "sm" | "md" }) {
  const letters = connectionInitials(name) || (type === "PostgreSQL" ? "PG" : type === "MySQL" ? "MY" : "RD");
  return <span className={`database-logo ${type.toLowerCase()} ${size}`}>{letters}</span>;
}

export function DatabaseTypeIcon({ type }: { type: DatabaseType }) {
  const icon = type === "PostgreSQL" ? postgresqlIcon : type === "MySQL" ? mysqlIcon : redisIcon;
  return <span className="database-type-icon" aria-hidden="true"><img src={icon} alt="" /></span>;
}

export function RegionFlag({ region }: { region?: string }) {
  const flag = regionFlagClass(region);
  if (!flag) return null;
  return <span className={`fi ${flag} region-flag`} aria-hidden="true" />;
}

function connectionInitials(name?: string) {
  const trimmed = name?.trim();
  if (!trimmed) return "";
  const first = Array.from(trimmed)[0];
  if (/[\u4e00-\u9fff]/.test(first)) return first;
  return Array.from(trimmed.replace(/\s+/g, "")).slice(0, 2).join("").toUpperCase();
}

function regionFlagClass(region?: string) {
  const value = region?.trim();
  if (!value) return "";
  if (value.includes("新加坡")) return "fi-sg";
  if (value.includes("美国")) return "fi-us";
  if (value.includes("香港")) return "fi-hk";
  if (value.includes("台湾")) return "fi-tw";
  if (value.includes("中国")) return "fi-cn";
  if (value.includes("日本")) return "fi-jp";
  if (value.includes("韩国")) return "fi-kr";
  if (value.includes("德国")) return "fi-de";
  if (value.includes("英国")) return "fi-gb";
  if (value.includes("法国")) return "fi-fr";
  if (value.includes("加拿大")) return "fi-ca";
  if (value.includes("澳大利亚")) return "fi-au";
  if (value.includes("荷兰")) return "fi-nl";
  if (value.includes("印度")) return "fi-in";
  if (value.includes("印度尼西亚")) return "fi-id";
  if (value.includes("泰国")) return "fi-th";
  if (value.includes("马来西亚")) return "fi-my";
  if (value.includes("越南")) return "fi-vn";
  if (value.includes("澳门")) return "fi-mo";
  return "";
}

export function SelectField({
  label,
  value,
  onChange,
  children,
  disabled = false,
}: {
  label: string;
  value: string | number;
  onChange: (value: string) => void;
  children: ReactNode;
  disabled?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement | null>(null);
  const options = useMemo(() => extractOptions(children), [children]);
  const selected = options.find((item: SelectOptionItem) => item.value === String(value)) ?? options[0];

  useEffect(() => {
    if (!open) return;
    const close = (event: MouseEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    window.addEventListener("mousedown", close);
    window.addEventListener("keydown", closeOnEscape);
    return () => {
      window.removeEventListener("mousedown", close);
      window.removeEventListener("keydown", closeOnEscape);
    };
  }, [open]);

  return (
    <div className="field select-field" ref={rootRef}>
      <span>{label}</span>
      <button type="button" className="select-trigger" disabled={disabled} aria-haspopup="listbox" aria-expanded={open} onClick={() => setOpen((current) => !current)}>
        <span>{selected?.label ?? "请选择"}</span>
        <ChevronDown size={16} />
      </button>
      {open && !disabled && (
        <div className="select-menu" role="listbox" aria-label={label}>
          {options.map((item) => (
            <button
              type="button"
              key={item.value}
              className={item.value === String(value) ? "active" : ""}
              role="option"
              aria-selected={item.value === String(value)}
              onClick={() => {
                onChange(item.value);
                setOpen(false);
              }}
            >
              {item.label}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

function extractOptions(children: ReactNode): SelectOptionItem[] {
  return Children.toArray(children).flatMap((child) => {
    if (!isValidElement(child)) return [];
    if (child.type === "option") return [optionFromElement(child as ReactElement<{ value?: string | number; children?: ReactNode }>)];
    if (child.type === Fragment) return extractOptions((child as ReactElement<{ children?: ReactNode }>).props.children);
    return [];
  });
}

function optionFromElement(element: ReactElement<{ value?: string | number; children?: ReactNode }>): SelectOptionItem {
  const label = Children.toArray(element.props.children).join("");
  return { value: String(element.props.value ?? label), label };
}
