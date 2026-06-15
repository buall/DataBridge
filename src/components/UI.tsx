import type { ButtonHTMLAttributes, ReactNode } from "react";
import mysqlIcon from "../assets/icons/devicon--mysql.svg";
import postgresqlIcon from "../assets/icons/devicon--postgresql.svg";
import redisIcon from "../assets/icons/devicon--redis.svg";
import type { DatabaseType } from "../types";

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
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  children: ReactNode;
}) {
  return (
    <label className="field">
      <span>{label}</span>
      <select value={value} onChange={(event) => onChange(event.target.value)}>
        {children}
      </select>
    </label>
  );
}
