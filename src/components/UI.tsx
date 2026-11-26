import {
  Children,
  Fragment,
  isValidElement,
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ButtonHTMLAttributes,
  type HTMLAttributes,
  type ReactElement,
  type ReactNode,
} from "react";
import { createPortal } from "react-dom";
import { AlertTriangle, Check, ChevronDown, MoreHorizontal, X } from "lucide-react";
import mysqlIcon from "../assets/icons/devicon--mysql.svg";
import mongodbIcon from "../assets/icons/devicon--mongodb.svg";
import neo4jIcon from "../assets/icons/devicon--neo4j.svg";
import postgresqlIcon from "../assets/icons/devicon--postgresql.svg";
import redisIcon from "../assets/icons/devicon--redis.svg";
import etcdIcon from "../assets/icons/devicon--etcd.svg";
import type { DatabaseType } from "../types";

type SelectOptionItem = { value: string; label: string; disabled?: boolean };

export function requiredLabel(label: string) {
  return (
    <>
      <span className="required-mark" aria-hidden="true">
        *{" "}
      </span>
      {label}
    </>
  );
}

export type ThemeMode = "light" | "dark";

export function useTheme() {
  const [theme, setTheme] = useState<ThemeMode>(() => {
    try {
      const saved = window.localStorage.getItem("db-theme");
      if (saved === "dark" || saved === "light") return saved;
    } catch {
      // Ignore storage access errors.
    }
    return "light";
  });

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    try {
      window.localStorage.setItem("db-theme", theme);
    } catch {
      // Ignore storage access errors.
    }
  }, [theme]);

  const toggleTheme = useCallback(() => {
    setTheme((current) => (current === "dark" ? "light" : "dark"));
  }, []);

  return { theme, setTheme, toggleTheme };
}

export type CollectionViewPrefs = {
  conn: "comfortable" | "compact";
  storage: "cards" | "list";
  mig: "comfortable" | "compact";
  bkp: "comfortable" | "compact";
};

export function useCollectionView() {
  const [prefs, setPrefs] = useState<CollectionViewPrefs>(() => {
    const defaults: CollectionViewPrefs = { conn: "comfortable", storage: "cards", mig: "comfortable", bkp: "comfortable" };
    try {
      const parsed = JSON.parse(window.localStorage.getItem("db-collView") || "null");
      if (parsed && typeof parsed === "object") {
        if (parsed.conn === "comfortable" || parsed.conn === "compact") defaults.conn = parsed.conn;
        if (parsed.storage === "cards" || parsed.storage === "list") defaults.storage = parsed.storage;
        if (parsed.mig === "comfortable" || parsed.mig === "compact") defaults.mig = parsed.mig;
        if (parsed.bkp === "comfortable" || parsed.bkp === "compact") defaults.bkp = parsed.bkp;
      }
    } catch {
      // Use defaults.
    }
    return defaults;
  });

  const updatePref = useCallback(<K extends keyof CollectionViewPrefs>(key: K, value: CollectionViewPrefs[K]) => {
    setPrefs((current) => {
      const next = { ...current, [key]: value };
      try {
        window.localStorage.setItem("db-collView", JSON.stringify(next));
      } catch {
        // Ignore storage errors.
      }
      return next;
    });
  }, []);

  return { prefs, updatePref };
}

export function BridgeLogo({ size = 34 }: { size?: number }) {
  const uid = useId().replace(/:/g, "");
  const bgId = `db-bg-${uid}`;
  const bridgeId = `db-br-${uid}`;
  return (
    <svg width={size} height={size} viewBox="4 4 56 56" fill="none" aria-hidden="true">
      <defs>
        <linearGradient id={bgId} x1="8" y1="8" x2="56" y2="56" gradientUnits="userSpaceOnUse">
          <stop stopColor="#1D4ED8" />
          <stop offset=".54" stopColor="#2563EB" />
          <stop offset="1" stopColor="#0891B2" />
        </linearGradient>
        <linearGradient id={bridgeId} x1="18" y1="23" x2="46" y2="41" gradientUnits="userSpaceOnUse">
          <stop stopColor="#FFFFFF" />
          <stop offset="1" stopColor="#D8F3FF" />
        </linearGradient>
      </defs>
      <rect x="4" y="4" width="56" height="56" rx="16" fill={`url(#${bgId})`} />
      <path
        d="M18 40c4.1-8.7 8.8-13 14-13s9.9 4.3 14 13"
        fill="none"
        stroke={`url(#${bridgeId})`}
        strokeWidth="8"
        strokeLinecap="round"
      />
      <path d="M20 31h9m6 0h9" fill="none" stroke="#BEEBFF" strokeWidth="5" strokeLinecap="round" />
      <circle cx="18" cy="40" r="7" fill="#EAF7FF" />
      <circle cx="46" cy="40" r="7" fill="#EAF7FF" />
      <circle cx="18" cy="40" r="3" fill="#1D4ED8" />
      <circle cx="46" cy="40" r="3" fill="#0891B2" />
    </svg>
  );
}

export function ProtectionRing({ done, total = 4, size = 60 }: { done: number; total?: number; size?: number }) {
  const circumference = 2 * Math.PI * 26;
  const segment = circumference / Math.max(1, total);
  return (
    <svg width={size} height={size} viewBox="0 0 64 64" aria-hidden="true">
      {Array.from({ length: total }, (_, index) => {
        const offset = -index * segment;
        return (
          <circle
            key={index}
            cx="32"
            cy="32"
            r="26"
            fill="none"
            stroke={index < done ? "var(--indigo)" : "var(--hair)"}
            strokeWidth="5"
            strokeLinecap="round"
            strokeDasharray={`${segment - 5} ${circumference - segment + 5}`}
            strokeDashoffset={offset}
            transform="rotate(-90 32 32)"
          />
        );
      })}
      <text x="32" y="30" textAnchor="middle" fontSize="15" fontWeight="700" fill="var(--ink)">
        {done}/{total}
      </text>
      <text x="32" y="44" textAnchor="middle" fontSize="8.5" fill="var(--muted)">
        就绪
      </text>
    </svg>
  );
}

export function Button({
  variant = "primary",
  size = "md",
  children,
  className = "",
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: "primary" | "secondary" | "danger" | "ghost";
  size?: "sm" | "md";
}) {
  return (
    <button className={`button ${variant} ${size === "sm" ? "sm" : ""} ${className}`.trim()} {...props}>
      {children}
    </button>
  );
}

export function Card({
  children,
  className = "",
  ...props
}: HTMLAttributes<HTMLElement> & { children: ReactNode; className?: string }) {
  return (
    <section className={`card ${className}`.trim()} {...props}>
      {children}
    </section>
  );
}

export function ConfirmDialog({
  title,
  description,
  details = [],
  warnings = [],
  confirmText = "确认",
  tone = "danger",
  confirmVariant,
  submitting = false,
  onCancel,
  onConfirm,
}: {
  title: string;
  description: string;
  details?: ReactNode[];
  warnings?: ReactNode[];
  confirmText?: string;
  tone?: "danger" | "warning";
  confirmVariant?: "primary" | "danger";
  submitting?: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const actionVariant = confirmVariant ?? (tone === "danger" ? "danger" : "primary");
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !submitting) onCancel();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onCancel, submitting]);

  return createPortal(
    <div className="modal-backdrop" onMouseDown={() => !submitting && onCancel()}>
      <div className="modal" role="dialog" aria-modal="true" onMouseDown={(event) => event.stopPropagation()}>
        <div className="modal-h">
          <div style={{ display: "flex", gap: 11, alignItems: "flex-start", flex: 1 }}>
            <span
              style={{
                width: 36,
                height: 36,
                borderRadius: 10,
                display: "grid",
                placeItems: "center",
                flex: "none",
                background: tone === "danger" ? "var(--danger-soft)" : "var(--warn-soft)",
                color: tone === "danger" ? "var(--danger)" : "var(--warn)",
              }}
            >
              <AlertTriangle size={18} />
            </span>
            <div>
              <h3>{title}</h3>
              <div className="ms">{description}</div>
            </div>
          </div>
          <button type="button" className="icon-btn ghost" aria-label="关闭" disabled={submitting} onClick={onCancel}>
            <X size={17} />
          </button>
        </div>
        <div className="modal-b" style={{ display: "flex", flexDirection: "column", gap: 10 }}>
          {warnings.length > 0 && (
            <div className="warn-callout" style={{ flexDirection: "column", gap: 4 }}>
              {warnings.map((item, index) => (
                <div key={index}>{item}</div>
              ))}
            </div>
          )}
          {details.map((item, index) => (
            <div key={index} style={{ fontSize: 13.5, color: "var(--ink-2)", lineHeight: 1.55 }}>
              {item}
            </div>
          ))}
        </div>
        <div className="modal-f">
          <Button type="button" variant="secondary" disabled={submitting} onClick={onCancel}>
            取消
          </Button>
          <Button type="button" variant={actionVariant} disabled={submitting} onClick={onConfirm}>
            {submitting ? "处理中..." : confirmText}
          </Button>
        </div>
      </div>
    </div>,
    document.body,
  );
}

export function Badge({
  children,
  tone = "blue",
}: {
  children: ReactNode;
  tone?: "blue" | "green" | "orange" | "red" | "gray" | "indigo";
}) {
  return <span className={`badge ${tone}`}>{children}</span>;
}

export function latencyTier(latency: number): {
  key: "great" | "good" | "mild" | "high" | "critical";
  label: string;
  range: string;
} {
  if (latency <= 60) return { key: "great", label: "极佳", range: "<= 60ms" };
  if (latency <= 150) return { key: "good", label: "良好", range: "61–150ms" };
  if (latency <= 300) return { key: "mild", label: "轻度", range: "151–300ms" };
  if (latency <= 500) return { key: "high", label: "偏高", range: "301–500ms" };
  return { key: "critical", label: "极高", range: "> 500ms" };
}

export function LatencyIndicator({
  latency,
  showDot = true,
}: {
  latency: number;
  showDot?: boolean;
}) {
  const tier = latencyTier(latency);
  return (
    <span
      className={`lat ${tier.key}`}
      data-tooltip-content={`网络延迟 ${latency} ms · ${tier.label}（${tier.range}）`}
    >
      {showDot ? <span className="lat-dot" aria-hidden="true" /> : null}
      <span className="lat-txt">
        <span className="lat-val num">{latency}</span>
        <span className="lat-unit">ms</span>
      </span>
    </span>
  );
}

export function DensitySwitch({
  value,
  onChange,
  options = [
    { value: "comfortable", label: "舒适" },
    { value: "compact", label: "紧凑" },
  ],
  label = "密度",
}: {
  value: string;
  onChange: (value: string) => void;
  options?: Array<{ value: string; label: string; icon?: ReactNode }>;
  label?: string;
}) {
  return (
    <span className="view-switch" role="group" aria-label={label}>
      {label ? <span className="vs-label">{label}</span> : null}
      {options.map((opt) => (
        <button
          key={opt.value}
          type="button"
          className={value === opt.value ? "on" : ""}
          onClick={() => onChange(opt.value)}
        >
          {opt.icon}
          {opt.label}
        </button>
      ))}
    </span>
  );
}

export function DatabaseLogo({ type, name, size = "md" }: { type: DatabaseType | string; name?: string; size?: "sm" | "md" }) {
  const icon = databaseIconAsset(type as DatabaseType);
  const fallback = connectionInitials(name) || databaseShortName(type as DatabaseType);
  return (
    <span className={`tglyph ${type.toLowerCase()} ${size === "sm" ? "sm" : "lg"}`}>
      {icon ? <img src={icon} alt="" /> : fallback}
    </span>
  );
}

export function DatabaseTypeIcon({ type }: { type: DatabaseType }) {
  const icon = databaseIconAsset(type);
  return (
    <span className={`database-type-icon ${type.toLowerCase()}`} aria-hidden="true">
      {icon ? <img src={icon} alt="" /> : <strong>{databaseShortName(type)}</strong>}
    </span>
  );
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

function databaseIconAsset(type: DatabaseType) {
  return type === "PostgreSQL"
    ? postgresqlIcon
    : type === "MySQL"
      ? mysqlIcon
      : type === "MongoDB"
        ? mongodbIcon
        : type === "Neo4j"
          ? neo4jIcon
          : type === "etcd"
            ? etcdIcon
            : redisIcon;
}

function databaseShortName(type: DatabaseType) {
  return type === "PostgreSQL" ? "PG" : type === "MySQL" ? "My" : type === "MongoDB" ? "Mo" : type === "Neo4j" ? "Ne" : type === "etcd" ? "et" : "Rd";
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
  if (value.includes("印度尼西亚")) return "fi-id";
  if (value.includes("印度")) return "fi-in";
  if (value.includes("泰国")) return "fi-th";
  if (value.includes("马来西亚")) return "fi-my";
  if (value.includes("越南")) return "fi-vn";
  if (value.includes("澳门")) return "fi-mo";
  return "";
}

/* ============ Portal SelectField (replaces inline clipped select) ============ */
export function SelectField({
  label,
  ariaLabel,
  value,
  onChange,
  children,
  disabled = false,
  invalid = false,
  inline = false,
  width,
}: {
  label?: ReactNode;
  ariaLabel?: string;
  value: string | number;
  onChange: (value: string) => void;
  children: ReactNode;
  disabled?: boolean;
  invalid?: boolean;
  inline?: boolean;
  width?: string | number;
}) {
  const [open, setOpen] = useState(false);
  const triggerRef = useRef<HTMLButtonElement | null>(null);
  const menuRef = useRef<HTMLDivElement | null>(null);
  const [pos, setPos] = useState<{ left: number; top: number; minWidth: number }>({ left: 0, top: 0, minWidth: 160 });
  const options = useMemo(() => extractOptions(children), [children]);
  const selected = options.find((item) => item.value === String(value)) ?? options[0];

  const updatePos = useCallback(() => {
    const trigger = triggerRef.current;
    if (!trigger) return;
    const rect = trigger.getBoundingClientRect();
    const menuH = menuRef.current?.offsetHeight || Math.min(288, options.length * 36 + 12);
    const menuW = Math.max(rect.width, menuRef.current?.offsetWidth || rect.width);
    const left = Math.max(8, Math.min(rect.left, window.innerWidth - menuW - 8));
    let top = rect.bottom + 6;
    if (top + menuH > window.innerHeight - 8) {
      top = rect.top - 6 - menuH;
    }
    top = Math.max(8, Math.min(top, window.innerHeight - menuH - 8));
    setPos({ left, top, minWidth: rect.width });
  }, [options.length]);

  useLayoutEffect(() => {
    if (open) updatePos();
  }, [open, updatePos]);

  useEffect(() => {
    if (!open) return;
    const onPointerDown = (event: MouseEvent) => {
      const target = event.target as Node;
      if (triggerRef.current?.contains(target) || menuRef.current?.contains(target)) return;
      setOpen(false);
    };
    const onEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setOpen(false);
        triggerRef.current?.focus();
      }
    };
    const onReflow = () => updatePos();
    window.addEventListener("mousedown", onPointerDown);
    window.addEventListener("keydown", onEscape);
    window.addEventListener("resize", onReflow);
    window.addEventListener("scroll", onReflow, true);
    return () => {
      window.removeEventListener("mousedown", onPointerDown);
      window.removeEventListener("keydown", onEscape);
      window.removeEventListener("resize", onReflow);
      window.removeEventListener("scroll", onReflow, true);
    };
  }, [open, updatePos]);

  const resolvedAriaLabel = ariaLabel ?? (typeof label === "string" ? label : undefined);

  return (
    <div
      className={`field ${invalid ? "invalid" : ""}`.trim()}
      style={inline ? { marginBottom: 0, display: "inline-block", width: width ?? "auto" } : width ? { width } : undefined}
    >
      {label ? <span>{label}</span> : null}
      <div className={`csel ${inline ? "csel-inline" : ""} ${open ? "is-open" : ""}`.trim()} style={width ? { width } : undefined}>
        <button
          ref={triggerRef}
          type="button"
          className="csel-trigger"
          disabled={disabled}
          aria-label={resolvedAriaLabel}
          aria-haspopup="listbox"
          aria-expanded={open}
          style={inline ? { height: 36 } : undefined}
          onClick={() => setOpen((current) => !current)}
        >
          <span className="csel-val">
            <span className="csel-txt">{selected?.label ?? "请选择"}</span>
          </span>
          <span className="csel-chev">
            <ChevronDown size={14} />
          </span>
        </button>
      </div>
      {open &&
        !disabled &&
        createPortal(
          <div
            ref={menuRef}
            className="csel-list"
            role="listbox"
            aria-label={resolvedAriaLabel}
            style={{ left: pos.left, top: pos.top, minWidth: pos.minWidth, maxWidth: "min(380px, calc(100vw - 16px))" }}
          >
            {options.length ? (
              options.map((item) => {
                const isSel = item.value === String(value);
                return (
                  <button
                    type="button"
                    key={item.value}
                    className="csel-opt"
                    role="option"
                    aria-selected={isSel}
                    disabled={item.disabled}
                    onClick={() => {
                      onChange(item.value);
                      setOpen(false);
                      triggerRef.current?.focus();
                    }}
                  >
                    <span className="csel-txt">{item.label}</span>
                    {isSel ? (
                      <span className="csel-check">
                        <Check size={14} />
                      </span>
                    ) : null}
                  </button>
                );
              })
            ) : (
              <div style={{ padding: "10px 12px", fontSize: 13, color: "var(--muted)" }}>暂无选项</div>
            )}
          </div>,
          document.body,
        )}
    </div>
  );
}

/* ============ Portal ActionMenu ("···" menus) ============ */
export interface ActionMenuItem {
  key: string;
  label: string;
  icon?: ReactNode;
  danger?: boolean;
  disabled?: boolean;
  dividerBefore?: boolean;
  tooltip?: string;
  onClick: () => void;
}

export function ActionMenu({
  items,
  ariaLabel,
}: {
  items: ActionMenuItem[];
  ariaLabel: string;
}) {
  const [open, setOpen] = useState(false);
  const btnRef = useRef<HTMLButtonElement | null>(null);
  const menuRef = useRef<HTMLDivElement | null>(null);
  const [pos, setPos] = useState<{ left: number; top: number }>({ left: 0, top: 0 });

  const updatePos = useCallback(() => {
    const btn = btnRef.current;
    if (!btn) return;
    const r = btn.getBoundingClientRect();
    const w = menuRef.current?.offsetWidth || 200;
    const h = menuRef.current?.offsetHeight || items.length * 36 + 12;
    const left = Math.max(8, Math.min(r.right - w, window.innerWidth - w - 8));
    let top = r.bottom + 6;
    if (top + h > window.innerHeight - 8) top = r.top - 6 - h;
    top = Math.max(8, Math.min(top, window.innerHeight - h - 8));
    setPos({ left, top });
  }, [items.length]);

  useLayoutEffect(() => {
    if (open) updatePos();
  }, [open, updatePos]);

  useEffect(() => {
    if (!open) return;
    const close = (event: MouseEvent) => {
      const target = event.target as Node;
      if (btnRef.current?.contains(target) || menuRef.current?.contains(target)) return;
      setOpen(false);
    };
    const onEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    window.addEventListener("mousedown", close);
    window.addEventListener("keydown", onEscape);
    window.addEventListener("resize", updatePos);
    window.addEventListener("scroll", updatePos, true);
    return () => {
      window.removeEventListener("mousedown", close);
      window.removeEventListener("keydown", onEscape);
      window.removeEventListener("resize", updatePos);
      window.removeEventListener("scroll", updatePos, true);
    };
  }, [open, updatePos]);

  return (
    <>
      <button
        ref={btnRef}
        type="button"
        className="icon-btn ghost"
        aria-label={ariaLabel}
        aria-expanded={open}
        onClick={(event) => {
          event.stopPropagation();
          setOpen((current) => !current);
        }}
      >
        <MoreHorizontal size={17} />
      </button>
      {open &&
        createPortal(
          <div
            ref={menuRef}
            className="portal-menu"
            style={{ left: pos.left, top: pos.top }}
            onClick={(event) => event.stopPropagation()}
          >
            {items.map((item) => (
              <Fragment key={item.key}>
                {item.dividerBefore ? <div className="msep" /> : null}
                <button
                  type="button"
                  className={item.danger ? "dng" : undefined}
                  disabled={item.disabled}
                  data-tooltip-content={item.tooltip}
                  onClick={() => {
                    setOpen(false);
                    item.onClick();
                  }}
                >
                  {item.icon}
                  <span>{item.label}</span>
                </button>
              </Fragment>
            ))}
          </div>,
          document.body,
        )}
    </>
  );
}

function extractOptions(children: ReactNode): SelectOptionItem[] {
  return Children.toArray(children).flatMap((child) => {
    if (!isValidElement(child)) return [];
    if (child.type === "option") {
      return [optionFromElement(child as ReactElement<{ value?: string | number; disabled?: boolean; children?: ReactNode }>)];
    }
    if (child.type === Fragment) {
      return extractOptions((child as ReactElement<{ children?: ReactNode }>).props.children);
    }
    return [];
  });
}

function optionFromElement(element: ReactElement<{ value?: string | number; disabled?: boolean; children?: ReactNode }>): SelectOptionItem {
  const label = Children.toArray(element.props.children).join("");
  return {
    value: String(element.props.value ?? label),
    label,
    disabled: Boolean(element.props.disabled),
  };
}
