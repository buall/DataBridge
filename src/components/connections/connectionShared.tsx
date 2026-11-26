import { Info, ShieldCheck, X } from "lucide-react";
import { useId, useState } from "react";
import { SelectField, requiredLabel } from "../UI";
import { CONNECTION_ENVIRONMENT_OPTIONS } from "../../labels";
import type {
  BackupJob,
  ConnectionEnvironment,
  ConnectionInput,
  ConnectionTLSConfig,
  DatabaseConnection,
  DatabaseInfo,
  DatabaseType,
} from "../../types";

export type ConnectionFormState = {
  type: DatabaseType;
  name: string;
  note: string;
  project: string;
  environment: ConnectionEnvironment;
  tags: string[];
  host: string;
  port: string;
  username: string;
  password: string;
  sslMode: string;
  tlsConfig: ConnectionTLSConfig;
  mongoMode: "direct" | "replica_set";
  mongoSeeds: string;
};

export type EditConnectionFormState = Omit<ConnectionFormState, "type">;
export type DuplicateConnectionFormState = Omit<ConnectionFormState, "type" | "mongoMode" | "mongoSeeds">;
export type ConnectionFormErrors = Partial<Record<"type" | "name" | "host" | "port" | "username" | "password" | "sslMode", string>>;


export function EtcdTLSHint({ enabled }: { enabled: boolean }) {
  if (!enabled) return null;
  return (
    <div className="warn-callout" style={{ marginBottom: 14, flexDirection: "column", gap: 6 }} role="note">
      <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
        <Info size={15} />
        <strong>Kubernetes etcd 通常需要双向 TLS 证书</strong>
      </div>
      <span>请读取文件并粘贴完整 PEM 内容，不要只填写路径（如 `/etc/kubernetes/pki/etcd/ca.crt`、`healthcheck-client.crt`、`healthcheck-client.key`）。</span>
    </div>
  );
}

export function connectionStatusTone(status: DatabaseConnection["status"]) {
  if (status === "connected") return "green";
  if (status === "untested") return "orange";
  return "red";
}

export function connectionStatusLabel(status: DatabaseConnection["status"]) {
  if (status === "connected") return "已连接";
  if (status === "untested") return "未验证";
  return "连接失败";
}

export function connectionHealthLabel(connection: DatabaseConnection) {
  if (connection.status === "connected") return `${connection.latency}ms`;
  if (connection.status === "untested") return "点击测试";
  return "网络不可达";
}

export function mongoBackupReadinessLabel(connection: DatabaseConnection) {
  const readiness = connection.mongoBackupReadiness;
  if (!readiness) return "未检查";
  if (readiness.status === "ready") return `可启用 · ${readiness.oplogWindowHours?.toFixed(1) ?? "-"}h`;
  if (readiness.status === "warning") return `需扩容 · ${readiness.oplogWindowHours?.toFixed(1) ?? "-"}h`;
  return "不可用";
}

export function EtcdTLSModeGuide({ enabled }: { enabled: boolean }) {
  if (!enabled) return null;
  return (
    <div className="info-callout" style={{ marginBottom: 14, flexDirection: "column", gap: 4 }} role="note">
      <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
        <ShieldCheck size={15} />
        <strong>Kubernetes 集群请选择 verify-full</strong>
      </div>
      <span>
        <b>verify-full</b>（推荐）：校验 CA 与节点身份；<b>require</b>：HTTPS 忽略服务端证书；<b>disable</b>：仅本地无 TLS 测试环境。
      </span>
    </div>
  );
}

export function formatBytes(value: number) {
  if (!value) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  return `${(value / 1024 ** index).toFixed(index === 0 ? 0 : 1)} ${units[index]}`;
}

export function formatDatabaseMetric(type: DatabaseType, database: DatabaseInfo) {
  if (type === "Neo4j") {
    return `${formatNumber(database.nodes ?? 0)} 节点 · ${formatNumber(database.relationships ?? 0)} 关系`;
  }
  return formatBytes(database.sizeBytes);
}

export function formatNumber(value: number) {
  return new Intl.NumberFormat("zh-CN").format(value);
}

type ScheduledBackupGroups = {
  bySourceId: Map<string, BackupJob[]>;
  bySourceName: Map<string, BackupJob[]>;
};

export function groupScheduledBackupsByConnection(backups: BackupJob[]): ScheduledBackupGroups {
  const bySourceId = new Map<string, BackupJob[]>();
  const bySourceName = new Map<string, BackupJob[]>();
  for (const job of backups) {
    if (!isScheduledBackupJob(job)) continue;
    if (job.sourceId) {
      const items = bySourceId.get(job.sourceId) ?? [];
      items.push(job);
      bySourceId.set(job.sourceId, items);
    }
    const sourceName = job.sourceName.trim();
    if (sourceName) {
      const items = bySourceName.get(sourceName) ?? [];
      items.push(job);
      bySourceName.set(sourceName, items);
    }
  }
  return { bySourceId, bySourceName };
}

export function scheduledBackupsForConnection(connection: DatabaseConnection, groups: ScheduledBackupGroups) {
  const byId = groups.bySourceId.get(connection.id) ?? [];
  if (byId.length > 0) return sortScheduledBackups(byId);
  return sortScheduledBackups(groups.bySourceName.get(connection.name) ?? []);
}

export function isScheduledBackupJob(job: BackupJob) {
  return job.destinationType !== "restore" && (job.scheduleMode === "scheduled" || job.status === "scheduled");
}

export function sortScheduledBackups(backups: BackupJob[]) {
  return [...backups].sort((left, right) => scheduledBackupTime(left) - scheduledBackupTime(right));
}

export function scheduledBackupTime(job: BackupJob) {
  return job.nextRunAt ? new Date(job.nextRunAt).getTime() : Number.MAX_SAFE_INTEGER;
}

export function scheduleLabel(job: BackupJob) {
  if (job.scheduleFrequency === "hourly") return `每小时第 ${job.scheduleTime?.slice(3) ?? "00:00"}`;
  const time = normalizeScheduleClock(job.scheduleTime);
  if (job.scheduleFrequency === "weekly") return `每周${weekdays[job.scheduleWeekday ?? 1] ?? "一"} ${time}`;
  if (job.scheduleFrequency === "monthly") return `每月 ${job.scheduleMonthDay ?? 1} 日 ${time}`;
  return `每天 ${time}`;
}

export function normalizeScheduleClock(value?: string) {
  if (!value) return "待设置";
  return value.split(":").slice(0, 2).join(":");
}

export const weekdays = ["日", "一", "二", "三", "四", "五", "六"];

export function duplicateConnectionName(name: string, existingNames: string[]) {
  const base = `${name} 副本`;
  if (!existingNames.includes(base)) return base;
  for (let index = 2; index < 100; index += 1) {
    const candidate = `${base} ${index}`;
    if (!existingNames.includes(candidate)) return candidate;
  }
  return `${base} ${Date.now()}`;
}

export function seedDataHint(type: DatabaseType) {
  if (type === "MongoDB") return "可按模板生成演示数据。每个模板会写入自己的演示集合，默认追加写入，也可切换为覆盖写入。";
  if (type === "Neo4j") return "可按模板生成演示图数据。每个模板会写入自己的演示节点标签，默认追加写入，也可切换为覆盖写入。";
  return "可按模板生成演示数据。每个模板会写入自己的演示表，默认追加写入，也可切换为覆盖写入。";
}

export function seedDataTargetSuffix(type: DatabaseType) {
  if (type === "MongoDB") return " 集合";
  if (type === "Neo4j") return " 节点标签";
  return "";
}

export function connectionCompatibilityHint(type: DatabaseType) {
  switch (type) {
    case "PostgreSQL":
      return "适用版本建议：PostgreSQL 13、14、15、16、18 已完成主组合兼容性测试；跨大版本降级迁移请先演练验证。";
    case "MySQL":
      return "适用版本建议：MySQL 5.7、8.0 已完成主组合兼容性测试；复杂对象请先在测试环境验证。";
    case "MongoDB":
      return "适用版本建议：MongoDB 5.0、6.0、7.0、8.0 已完成主组合兼容性测试；oplog 增量需副本集。";
    case "Neo4j":
      return "适用版本建议：Neo4j 4.4、5.x 已完成主组合兼容性测试；跨大版本场景请先验证约束和索引兼容性。";
    case "etcd":
      return "当前支持 etcd v3 客户端端点连接与整集群快照备份；一个连接代表一个集群。";
    default:
      return "请先在测试环境完成连接、迁移、备份恢复演练。";
  }
}

export function connectionSecurityConfig(type: DatabaseType) {
  switch (type) {
    case "PostgreSQL":
      return {
        label: "SSL 模式",
        hint: "PostgreSQL 原生支持 sslmode。prefer 优先尝试加密，require 强制加密，verify-full 校验证书和主机名。",
        options: [
          { value: "prefer", label: "prefer · 优先加密" },
          { value: "require", label: "require · 强制加密" },
          { value: "disable", label: "disable · 不加密" },
          { value: "verify-full", label: "verify-full · 校验证书与主机名" },
        ],
      };
    case "MySQL":
      return {
        label: "SSL 模式",
        hint: "MySQL 映射为客户端 TLS 连接策略。prefer 尽量协商 TLS，require 强制 TLS，verify-full 严格校验。",
        options: [
          { value: "prefer", label: "prefer · 优先加密" },
          { value: "require", label: "require · 强制加密" },
          { value: "disable", label: "disable · 不加密" },
          { value: "verify-full", label: "verify-full · 严格校验" },
        ],
      };
    case "MongoDB":
      return {
        label: "TLS 模式",
        hint: "MongoDB 默认不启用 TLS；require 只强制加密，verify-full 会校验证书链和主机名。",
        options: [
          { value: "disable", label: "disable · 不启用 TLS" },
          { value: "require", label: "require · 启用 TLS" },
          { value: "verify-full", label: "verify-full · 校验证书与主机名" },
        ],
      };
    case "Neo4j":
      return {
        label: "TLS 模式",
        hint: "Neo4j 映射到底层 Bolt 协议：disable=bolt，require=bolt+ssc，verify-full=bolt+s。",
        options: [
          { value: "disable", label: "disable · 使用 bolt" },
          { value: "require", label: "require · 使用 bolt+ssc" },
          { value: "verify-full", label: "verify-full · 使用 bolt+s" },
        ],
      };
    case "etcd":
      return {
        label: "TLS 模式",
        hint: "disable=HTTP；require=HTTPS 不校验证书；verify-full=CA 校验 + 可选 mTLS 客户端证书。",
        options: [
          { value: "disable", label: "disable · HTTP" },
          { value: "require", label: "require · HTTPS" },
          { value: "verify-full", label: "verify-full · mTLS / 严格校验" },
        ],
      };
    default:
      return {
        label: "SSL 模式",
        hint: "",
        options: [
          { value: "prefer", label: "prefer" },
          { value: "require", label: "require" },
          { value: "disable", label: "disable" },
        ],
      };
  }
}

export function defaultSecurityMode(type: DatabaseType) {
  if (type === "etcd") return "verify-full";
  return type === "MongoDB" || type === "Neo4j" ? "disable" : "prefer";
}

export function defaultPort(type: DatabaseType) {
  if (type === "MySQL") return "3306";
  if (type === "MongoDB") return "27017";
  if (type === "Neo4j") return "7687";
  if (type === "etcd") return "2379";
  return "5432";
}

export function emptyTLSConfig(): ConnectionTLSConfig {
  return { caCert: "", clientCert: "", clientKey: "" };
}

export function emptyConnectionForm(type: DatabaseType): ConnectionFormState {
  return {
    type,
    name: suggestConnectionName(type, "", defaultPort(type)),
    note: "",
    project: "",
    environment: "",
    tags: [],
    host: "",
    port: defaultPort(type),
    username: "",
    password: "",
    sslMode: defaultSecurityMode(type),
    tlsConfig: emptyTLSConfig(),
    mongoMode: "direct",
    mongoSeeds: "",
  };
}

export function ConnectionMetadataFields({
  project,
  environment,
  tags,
  projectSuggestions,
  onProjectChange,
  onEnvironmentChange,
  onTagsChange,
}: {
  project: string;
  environment: ConnectionEnvironment;
  tags: string[];
  projectSuggestions: string[];
  onProjectChange: (value: string) => void;
  onEnvironmentChange: (value: ConnectionEnvironment) => void;
  onTagsChange: (value: string[]) => void;
}) {
  const datalistId = useId();
  const [tagInput, setTagInput] = useState("");

  function commitTags(raw: string) {
    const parts = raw
      .split(/[,，\n]/)
      .map((item) => item.trim())
      .filter(Boolean);
    if (parts.length === 0) {
      setTagInput("");
      return;
    }
    const next = [...tags];
    for (const part of parts) {
      if (!next.includes(part)) {
        next.push(part);
      }
    }
    onTagsChange(next);
    setTagInput("");
  }

  return (
    <>
      <div className="grid2">
        <label className="field">
          <span>所属项目</span>
          <input
            list={datalistId}
            value={project}
            onChange={(e) => onProjectChange(e.target.value)}
            placeholder="可选，如 电商核心、支付中心"
          />
          <datalist id={datalistId}>
            {projectSuggestions.map((item) => (
              <option key={item} value={item} />
            ))}
          </datalist>
        </label>
        <SelectField
          label="环境"
          value={environment}
          onChange={(value) => onEnvironmentChange(value as ConnectionEnvironment)}
        >
          <option value="">未设置</option>
          {CONNECTION_ENVIRONMENT_OPTIONS.map((option) => (
            <option key={option.value} value={option.value}>
              {option.label}
            </option>
          ))}
        </SelectField>
      </div>
      <div className="field">
        <span>标签</span>
        <div className="tag-input-box">
          {tags.map((tag) => (
            <span
              key={tag}
              className="dbchip"
              style={{ display: "inline-flex", alignItems: "center", gap: 4, paddingRight: 4 }}
            >
              <span># {tag}</span>
              <button
                type="button"
                aria-label={`移除标签 ${tag}`}
                onClick={() => onTagsChange(tags.filter((item) => item !== tag))}
                style={{
                  border: "none",
                  background: "none",
                  padding: 0,
                  cursor: "pointer",
                  color: "var(--muted)",
                  display: "inline-flex",
                }}
              >
                <X size={12} />
              </button>
            </span>
          ))}
          <input
            value={tagInput}
            onChange={(e) => {
              const val = e.target.value;
              if (val.includes(",") || val.includes("，")) {
                commitTags(val);
              } else {
                setTagInput(val);
              }
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                commitTags(tagInput);
              } else if (e.key === "Backspace" && !tagInput && tags.length > 0) {
                onTagsChange(tags.slice(0, -1));
              }
            }}
            onBlur={() => {
              if (tagInput.trim()) commitTags(tagInput);
            }}
            placeholder={tags.length ? "继续输入并回车添加…" : "输入标签后按回车或逗号添加，如 主库、只读"}
          />
        </div>
      </div>
    </>
  );
}

export function connectionInputFromForm(form: ConnectionFormState): Omit<ConnectionInput, "port" | "validate"> {
  const { mongoMode, mongoSeeds, ...input } = form;
  return {
    ...input,
    endpoints:
      form.type === "MongoDB" && mongoMode === "replica_set"
        ? mongoSeeds
            .split(/[\n,]/)
            .map((value) => value.trim())
            .filter(Boolean)
        : [],
  };
}

export function MongoConnectionModeFields({
  mode,
  seeds,
  onModeChange,
  onSeedsChange,
}: {
  mode: "direct" | "replica_set";
  seeds: string;
  onModeChange: (mode: "direct" | "replica_set") => void;
  onSeedsChange: (seeds: string) => void;
}) {
  return (
    <>
      <SelectField label="连接模式" value={mode} onChange={(value) => onModeChange(value as "direct" | "replica_set")}>
        <option value="direct">直连节点</option>
        <option value="replica_set">副本集自动发现 Primary</option>
      </SelectField>
      {mode === "replica_set" ? (
        <label className="field">
          <span>副本集种子节点</span>
          <textarea
            value={seeds}
            onChange={(event) => onSeedsChange(event.target.value)}
            placeholder={"192.168.2.113:27017\n192.168.2.114:27017"}
          />
          <small className="field-hint">每行一个节点，也可用逗号分隔；系统会自动发现并连接当前 Primary。</small>
        </label>
      ) : null}
    </>
  );
}

export function suggestConnectionName(type: DatabaseType, host: string, port: string) {
  const hostValue = host.trim() || "主机地址";
  const portValue = port.trim() || defaultPort(type);
  return `${type}-${hostValue}-${portValue}`;
}

export function validateConnectionForm(form: ConnectionFormState): ConnectionFormErrors {
  const errors: ConnectionFormErrors = {};
  if (!form.type) errors.type = "请选择数据库类型";
  if (!form.name.trim()) errors.name = "请输入连接名称";
  if (!form.host.trim()) errors.host = "请输入主机地址";
  else if (!isValidConnectionHost(form.host.trim())) errors.host = "请输入合法的 IPv4 地址或域名 / 主机名";

  if (!form.port.trim()) errors.port = "请输入端口";
  else if (!/^\d+$/.test(form.port.trim())) errors.port = "端口只能输入数字";
  else {
    const port = Number(form.port.trim());
    if (!Number.isInteger(port) || port < 1 || port > 65535) errors.port = "端口范围需在 1 到 65535 之间";
  }

  if (form.type !== "etcd" && !form.username.trim()) errors.username = "请输入用户名";
  if (form.type !== "etcd" && !form.password.trim()) errors.password = "请输入密码";
  if (!form.sslMode.trim()) errors.sslMode = "请选择连接加密模式";
  return errors;
}

export function isValidConnectionHost(value: string) {
  return isIPv4Host(value) || isDomainOrHostname(value);
}

export function isIPv4Host(value: string) {
  const segments = value.split(".");
  if (segments.length !== 4) return false;
  return segments.every((segment) => /^(0|[1-9]\d{0,2})$/.test(segment) && Number(segment) >= 0 && Number(segment) <= 255);
}

export function isDomainOrHostname(value: string) {
  if (value.length > 253) return false;
  if (value === "localhost") return true;
  const labels = value.split(".");
  if (labels.some((label) => label.length === 0 || label.length > 63)) return false;
  if (!labels.every((label) => /^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?$/.test(label))) return false;
  if (labels.every((label) => /^\d+$/.test(label))) return false;
  const topLevelLabel = labels[labels.length - 1];
  if (/^\d+$/.test(topLevelLabel)) return false;
  return true;
}

export function compactVersionLabel(version?: string) {
  if (!version) return "待刷新";
  const normalized = version.trim();
  const core = normalized.split(/\s|\(/)[0] || normalized;
  return `v${core}`;
}

export function TLSCertificateFields({
  value,
  onChange,
  preserveExisting = false,
}: {
  value: ConnectionTLSConfig;
  onChange: (value: ConnectionTLSConfig) => void;
  preserveExisting?: boolean;
}) {
  const caPlaceholder = preserveExisting ? "留空则沿用已保存的 CA 证书" : "-----BEGIN CERTIFICATE-----";
  return (
    <div className="card pad" style={{ background: "var(--surface-2)", marginBottom: 14 }}>
      <div style={{ marginBottom: 10 }}>
        <strong style={{ fontSize: 13.5 }}>证书校验</strong>
        <p style={{ fontSize: 12, color: "var(--muted)", marginTop: 2 }}>
          {preserveExisting
            ? "verify-full 会校验证书链和主机名；如果不修改证书，可以留空。"
            : "verify-full 需要 CA 证书来校验服务端证书链和主机名。"}
        </p>
      </div>
      <label className="field">
        <span>CA 证书 PEM</span>
        <textarea
          className="mono"
          value={value.caCert ?? ""}
          onChange={(event) => onChange({ ...value, caCert: event.target.value })}
          placeholder={caPlaceholder}
          rows={3}
        />
      </label>
      <div className="grid2">
        <label className="field" style={{ marginBottom: 0 }}>
          <span>客户端证书 PEM（可选）</span>
          <textarea
            className="mono"
            value={value.clientCert ?? ""}
            onChange={(event) => onChange({ ...value, clientCert: event.target.value })}
            placeholder="仅 mTLS 场景需要填写"
            rows={3}
          />
        </label>
        <label className="field" style={{ marginBottom: 0 }}>
          <span>客户端私钥 PEM（可选）</span>
          <textarea
            className="mono"
            value={value.clientKey ?? ""}
            onChange={(event) => onChange({ ...value, clientKey: event.target.value })}
            placeholder="填写客户端证书时必须同时填写私钥"
            rows={3}
          />
        </label>
      </div>
    </div>
  );
}
