import { ArrowLeft, ArrowRight, Archive, Check, ChevronDown, Clock, Database } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { api } from "../api";
import type { BackupConfig, DatabaseConnection, DatabaseInfo, NotificationRecipient, RemoteStorage } from "../types";
import { Button, Card, DatabaseLogo, DatabaseTypeIcon, RegionFlag, SelectField } from "../components/UI";
import { NotificationRecipientPicker, useNotificationRecipients } from "../components/NotificationRecipientPicker";

const steps = ["源数据库", "备份目标", "确认执行"];

export function BackupWizard({
  connections,
  storages,
  onStart,
}: {
  connections: DatabaseConnection[];
  storages: RemoteStorage[];
  onStart: (config: BackupConfig) => Promise<void>;
}) {
  const writableStorages = useMemo(() => storages.filter((item) => item.type === "object" || item.type === "local"), [storages]);
  const backupableConnections = useMemo(() => connections.filter((item) => item.type === "PostgreSQL" || item.type === "MySQL" || item.type === "MongoDB" || item.type === "Neo4j"), [connections]);
  const [step, setStep] = useState(0);
  const [databases, setDatabases] = useState<DatabaseInfo[]>([]);
  const [databaseConnectionId, setDatabaseConnectionId] = useState("");
  const [loading, setLoading] = useState(false);
  const [starting, setStarting] = useState(false);
  const [error, setError] = useState("");
  const { recipients, loadingRecipients } = useNotificationRecipients();
  const [config, setConfig] = useState<BackupConfig>({
    name: "",
    sourceId: backupableConnections[0]?.id ?? "",
    sourceDatabase: "",
    sourceDatabases: [],
    destinationType: "storage",
    destination: writableStorages[0]?.id ?? "",
    scheduleMode: "once",
    scheduleFrequency: "daily",
    scheduleTime: "02:00:00",
    scheduleWeekday: 1,
    scheduleMonthDay: 1,
    retentionDays: 30,
    notificationEnabled: false,
    notificationRecipientIds: [],
  });

  useEffect(() => {
    setConfig((current) => ({
      ...current,
      sourceId: backupableConnections.some((item) => item.id === current.sourceId) ? current.sourceId : (backupableConnections[0]?.id ?? ""),
      destination: writableStorages.some((item) => item.id === current.destination) ? current.destination : (writableStorages[0]?.id ?? ""),
    }));
  }, [backupableConnections, writableStorages]);

  useEffect(() => {
    const sourceId = config.sourceId;
    setDatabases([]);
    setDatabaseConnectionId("");
    setConfig((current) => current.sourceId === sourceId ? { ...current, sourceDatabase: "", sourceDatabases: [] } : current);
    if (!sourceId) {
      setLoading(false);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError("");
    api.databases(sourceId)
      .then((items) => {
        if (cancelled) return;
        setDatabases(items);
        setDatabaseConnectionId(sourceId);
        setConfig((current) => ({
          ...current,
          sourceDatabase: items.some((item) => item.name === current.sourceDatabase) ? current.sourceDatabase : (items[0]?.name ?? ""),
          sourceDatabases: current.sourceDatabases.filter((name) => items.some((item) => item.name === name)).length
            ? current.sourceDatabases.filter((name) => items.some((item) => item.name === name))
            : (items[0]?.name ? [items[0].name] : []),
        }));
      })
      .catch((reason) => {
        if (!cancelled) setError(reason.message);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => { cancelled = true; };
  }, [config.sourceId]);

  const source = backupableConnections.find((item) => item.id === config.sourceId);
  const destination = writableStorages.find((item) => item.id === config.destination);
  const activeDatabases = databaseConnectionId === config.sourceId ? databases : [];
  const selectedDatabases = activeDatabases.filter((item) => config.sourceDatabases.includes(item.name));
  const databaseLabel = selectedDatabases.length === 1 ? selectedDatabases[0].name : `${selectedDatabases.length} 个数据库`;

  async function start() {
    const validDatabaseNames = new Set(activeDatabases.map((item) => item.name));
    const sourceDatabases = config.sourceDatabases.filter((name) => validDatabaseNames.has(name));
    if (loading || databaseConnectionId !== config.sourceId || !sourceDatabases.length) {
      setError("请等待当前源端数据库列表加载完成，并选择要备份的数据库");
      return;
    }
    if (config.scheduleMode === "scheduled" && config.notificationEnabled && !(config.notificationRecipientIds?.length)) {
      setError("请至少选择一个通知接收者，或关闭备份通知");
      return;
    }
    setStarting(true);
    setError("");
    try {
      const scheduled = config.scheduleMode === "scheduled";
      await onStart({
        ...config,
        notificationEnabled: scheduled ? config.notificationEnabled : false,
        notificationRecipientIds: scheduled ? config.notificationRecipientIds : [],
        sourceDatabase: sourceDatabases.join(", "),
        sourceDatabases,
        name: config.name || `${source?.name ?? databaseLabel} / ${databaseLabel} 备份`,
      });
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "创建备份任务失败");
      setStarting(false);
    }
  }

  if (!backupableConnections.length) {
    return <div className="empty-state"><div>DB</div><h2>暂无可备份连接</h2><p>请先添加 PostgreSQL、MySQL、MongoDB 或 Neo4j 连接，再创建备份任务。</p></div>;
  }
  if (!writableStorages.length) {
    return <div className="empty-state"><div>DB</div><h2>暂无备份目标</h2><p>请先在远端存储中添加对象存储或本地目录。</p></div>;
  }

  return (
    <div className="wizard-page">
      <div className="stepper">
        {steps.map((label, index) => <div className={`step ${index === step ? "current" : ""} ${index < step ? "done" : ""}`} key={label}><span>{index < step ? <Check size={14} /> : index + 1}</span><strong>{label}</strong>{index < steps.length - 1 && <i />}</div>)}
      </div>
      {error && <div className="error-banner">{error}</div>}

      {step === 0 && (
        <Card className="wizard-card connection-step">
          <div className="card-heading"><h3>选择要备份的数据库</h3><p>当前支持 PostgreSQL、MySQL、MongoDB 和 Neo4j 全库导出。</p></div>
          <div className="config-grid backup-source-grid">
            <label className="field"><span>任务名称</span><input value={config.name} onChange={(event) => setConfig({ ...config, name: event.target.value })} placeholder={`${config.sourceDatabase || "数据库"} 备份`} /></label>
            <SourceConnectionSelect connectionId={config.sourceId} connections={connections} onChange={(sourceId) => setConfig({ ...config, sourceId, sourceDatabase: "", sourceDatabases: [] })} />
            <DatabaseMultiSelect type={source?.type} databases={activeDatabases} loading={loading} selected={config.sourceDatabases} onChange={(sourceDatabases) => setConfig({ ...config, sourceDatabases, sourceDatabase: sourceDatabases.join(", ") })} />
          </div>
          {loading && <p className="loading-copy">正在加载数据库...</p>}
        </Card>
      )}

      {step === 1 && (
        <Card className="wizard-card config-step">
          <div className="card-heading"><h3>选择备份目标</h3><p>对象存储会上传到配置的 Bucket 和默认目录。</p></div>
          <div className="config-grid">
            <SelectField label="备份目标" value={config.destination} onChange={(destination) => setConfig({ ...config, destination })}>{writableStorages.map((item) => <option key={item.id} value={item.id}>{item.name} · {storageTypeLabel(item)}</option>)}</SelectField>
            <div className="endpoint-summary"><Archive size={22} /><div><span>目标位置</span><strong>{destination?.bucket || destination?.path || "未设置"}</strong><small>{destination?.endpoint || destination?.provider}</small></div></div>
          </div>
          <div className="schedule-panel">
            <span>执行方式</span>
            <div className="segmented-control">
              <button type="button" className={config.scheduleMode === "once" ? "active" : ""} onClick={() => setConfig({ ...config, scheduleMode: "once", notificationEnabled: false, notificationRecipientIds: [] })}>一次性</button>
              <button type="button" className={config.scheduleMode === "scheduled" ? "active" : ""} onClick={() => setConfig({ ...config, scheduleMode: "scheduled" })}>定时</button>
            </div>
            {config.scheduleMode === "scheduled" && (
              <div className="scheduled-options">
                <div className="schedule-grid">
                  <SelectField label="频率" value={config.scheduleFrequency ?? "daily"} onChange={(scheduleFrequency) => setConfig({ ...config, scheduleFrequency: scheduleFrequency as BackupConfig["scheduleFrequency"] })}><option value="daily">每天</option><option value="weekly">每周</option><option value="monthly">每月</option></SelectField>
                  <TimeSelect value={config.scheduleTime ?? "02:00:00"} onChange={(scheduleTime) => setConfig({ ...config, scheduleTime })} />
                  {config.scheduleFrequency === "weekly" && <SelectField label="每周" value={config.scheduleWeekday ?? 1} onChange={(scheduleWeekday) => setConfig({ ...config, scheduleWeekday: Number(scheduleWeekday) })}>{weekdays.map((item, index) => <option key={item} value={index}>{item}</option>)}</SelectField>}
                  {config.scheduleFrequency === "monthly" && <SelectField label="每月" value={config.scheduleMonthDay ?? 1} onChange={(scheduleMonthDay) => setConfig({ ...config, scheduleMonthDay: Number(scheduleMonthDay) })}>{Array.from({ length: 28 }, (_, index) => index + 1).map((day) => <option key={day} value={day}>{day} 日</option>)}</SelectField>}
                  <RetentionDaysField value={config.retentionDays ?? 30} onChange={(retentionDays) => setConfig({ ...config, retentionDays })} />
                </div>
                <div className="notification-panel">
                  <button type="button" className="toggle-row" onClick={() => setConfig({ ...config, notificationEnabled: !config.notificationEnabled })}><span className={`switch ${config.notificationEnabled ? "on" : ""}`}><i /></span><div><strong>备份通知</strong><small>定时任务执行结束后发送成功或失败结果</small></div></button>
                  {config.notificationEnabled && (
                    <div className="notification-recipient-field"><span>通知接收者</span><NotificationRecipientPicker recipients={recipients} loading={loadingRecipients} selected={config.notificationRecipientIds ?? []} onChange={(notificationRecipientIds) => setConfig({ ...config, notificationRecipientIds })} /></div>
                  )}
                </div>
              </div>
            )}
          </div>
        </Card>
      )}

      {step === 2 && (
        <Card className="wizard-card confirm-step">
          <div className="confirm-icon"><Check /></div><div className="card-heading centered"><h3>确认执行备份</h3><p>任务启动后会导出源数据库并写入目标存储。</p></div>
          <div className="route-summary"><Endpoint connection={source} database={databaseLabel} /><div className="route-line"><ArrowRight /></div><div className="endpoint-summary"><Archive size={22} /><div><span>备份目标</span><strong>{destination?.name}</strong><small>{destination?.bucket || destination?.path}</small></div></div></div>
<div className="confirmation-grid"><div><span>数据库类型</span><strong>{source?.type}</strong></div><div><span>源数据库</span><strong>{config.sourceDatabases.join(", ")}</strong></div><div><span>目标类型</span><strong>{storageTypeLabel(destination)}</strong></div><div><span>执行方式</span><strong>{scheduleLabel(config)}</strong></div><div><span>保留策略</span><strong>{retentionPolicyLabel(config)}</strong></div><div><span>备份通知</span><strong>{notificationLabel(config, recipients)}</strong></div><div><span>文件格式</span><strong>{backupFormatLabel(source?.type)}</strong></div></div>
        </Card>
      )}

      <div className="wizard-actions">
        <span>备份文件会写入本地任务记录，密钥仍仅在后端加密保存</span>
        <div>{step > 0 && <Button variant="secondary" onClick={() => setStep(step - 1)}><ArrowLeft size={16} />上一步</Button>}<Button disabled={loading || starting || databaseConnectionId !== config.sourceId || !config.sourceDatabases.length || !config.destination} onClick={() => step === 2 ? start() : setStep(step + 1)}>{starting ? "正在创建..." : step === 2 ? config.scheduleMode === "scheduled" ? "创建计划" : "启动备份" : "下一步"}<ArrowRight size={16} /></Button></div>
      </div>
    </div>
  );
}

const weekdays = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];
export const retentionDayOptions = [30, 60, 90, 180, 365, 730, 1095];

function scheduleLabel(config: BackupConfig) {
  if (config.scheduleMode !== "scheduled") return "一次性立即执行";
  if (config.scheduleFrequency === "weekly") return `每周${weekdays[config.scheduleWeekday ?? 1]} ${config.scheduleTime}`;
  if (config.scheduleFrequency === "monthly") return `每月 ${config.scheduleMonthDay ?? 1} 日 ${config.scheduleTime}`;
  return `每天 ${config.scheduleTime}`;
}

function notificationLabel(config: BackupConfig, recipients?: NotificationRecipient[]) {
  if (config.scheduleMode !== "scheduled" || !config.notificationEnabled) return "未开启";
  const names = (config.notificationRecipientIds ?? []).map((id) => recipients?.find((item) => item.id === id)?.name).filter(Boolean);
  return names.length ? names.join("、") : "未选择接收者";
}

function retentionPolicyLabel(config: BackupConfig) {
  if (config.scheduleMode !== "scheduled") return "不适用";
  return retentionLabel(config.retentionDays ?? 30);
}

function retentionLabel(days: number) {
  return `${days} 天`;
}

export function RetentionDaysField({ value, onChange }: { value: number; onChange: (value: number) => void }) {
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState(String(value || 30));
  const rootRef = useRef<HTMLLabelElement | null>(null);

  useEffect(() => setDraft(String(value || 30)), [value]);

  useEffect(() => {
    if (!open) return;
    const close = (event: MouseEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) {
        commitRetentionDays(draft, onChange, setDraft);
        setOpen(false);
      }
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setDraft(String(value || 30));
        setOpen(false);
      }
    };
    window.addEventListener("mousedown", close);
    window.addEventListener("keydown", closeOnEscape);
    return () => {
      window.removeEventListener("mousedown", close);
      window.removeEventListener("keydown", closeOnEscape);
    };
  }, [draft, onChange, open, value]);

  return (
    <label className="field retention-combobox-field" ref={rootRef}>
      <span>保留期限（天）</span>
      <div className={`retention-combobox ${open ? "open" : ""}`}>
        <input
          inputMode="numeric"
          value={draft}
          onChange={(event) => setDraft(event.target.value.replace(/[^\d]/g, ""))}
          onBlur={() => commitRetentionDays(draft, onChange, setDraft)}
          onFocus={() => setOpen(true)}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.preventDefault();
              commitRetentionDays(draft, onChange, setDraft);
              setOpen(false);
            }
          }}
        />
        <button type="button" aria-label="展开保留期限选项" aria-expanded={open} onMouseDown={(event) => event.preventDefault()} onClick={() => setOpen((current) => !current)}><ChevronDown size={16} /></button>
      </div>
      {open && (
        <div className="select-menu retention-combobox-menu" role="listbox" aria-label="保留期限">
          {retentionDayOptions.map((days) => (
            <button
              type="button"
              key={days}
              className={Number(draft) === days ? "active" : ""}
              role="option"
              aria-selected={Number(draft) === days}
              onMouseDown={(event) => event.preventDefault()}
              onClick={() => {
                setDraft(String(days));
                onChange(days);
                setOpen(false);
              }}
            >
              {retentionLabel(days)}
            </button>
          ))}
        </div>
      )}
    </label>
  );
}

function commitRetentionDays(input: string, onChange: (value: number) => void, setDraft: (value: string) => void) {
  const parsed = Number(input);
  const next = Number.isFinite(parsed) ? Math.max(1, Math.min(1095, Math.floor(parsed))) : 30;
  setDraft(String(next));
  onChange(next);
}

export function TimeSelect({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState(value);
  const rootRef = useRef<HTMLDivElement | null>(null);
  const normalized = normalizeScheduleTime(draft || value);
  const [hour, minute, second] = normalized.split(":");

  useEffect(() => setDraft(value), [value]);

  useEffect(() => {
    if (!open) return;
    const close = (event: MouseEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) {
        const nextValue = normalizeScheduleTime(draft || value);
        setDraft(nextValue);
        onChange(nextValue);
        setOpen(false);
      }
    };
    window.addEventListener("mousedown", close);
    return () => window.removeEventListener("mousedown", close);
  }, [draft, onChange, open, value]);

  function commit(input: string) {
    const nextValue = normalizeScheduleTime(input);
    setDraft(nextValue);
    onChange(nextValue);
  }

  function updatePart(index: number, part: string) {
    const parts = normalized.split(":");
    parts[index] = part;
    commit(parts.join(":"));
  }

  return (
    <div className="field time-select-field" ref={rootRef}>
      <span>执行时间</span>
      <div className={`time-select-control ${open ? "open" : ""}`}>
        <Clock size={16} className="time-picker-icon" />
        <input
          inputMode="numeric"
          value={draft}
          placeholder="02:00:00"
          onFocus={() => setOpen(true)}
          onKeyDown={(event) => {
            if (event.key === "Enter" || event.key === "Tab") commit(event.currentTarget.value);
            if (event.key === "Escape") setOpen(false);
          }}
          onChange={(event) => setDraft(event.target.value.replace(/[^\d:]/g, "").slice(0, 8))}
          aria-label="执行时间"
        />
        <button type="button" className="time-picker-toggle" aria-label="选择执行时间" aria-expanded={open} onMouseDown={(event) => event.preventDefault()} onClick={() => setOpen((current) => !current)}>
          <ChevronDown size={16} />
        </button>
        {open && (
          <div className="time-picker-menu">
            <TimeColumn label="时" value={hour} options={hourOptions} onPick={(part) => updatePart(0, part)} />
            <TimeColumn label="分" value={minute} options={minuteSecondOptions} onPick={(part) => updatePart(1, part)} />
            <TimeColumn label="秒" value={second} options={minuteSecondOptions} onPick={(part) => updatePart(2, part)} />
          </div>
        )}
      </div>
    </div>
  );
}

function TimeColumn({ label, value, options, onPick }: { label: string; value: string; options: string[]; onPick: (value: string) => void }) {
  return (
    <div className="time-picker-column">
      <span>{label}</span>
      <div>
        {options.map((item) => <button type="button" key={item} className={item === value ? "active" : ""} onMouseDown={(event) => event.preventDefault()} onClick={() => onPick(item)}>{item}</button>)}
      </div>
    </div>
  );
}

const hourOptions = Array.from({ length: 24 }, (_, index) => String(index).padStart(2, "0"));
const minuteSecondOptions = Array.from({ length: 60 }, (_, index) => String(index).padStart(2, "0"));

function normalizeScheduleTime(value: string) {
  const parts = value.split(":");
  const hour = normalizeTimePart(parts[0], 23);
  const minute = normalizeTimePart(parts[1], 59);
  const second = normalizeTimePart(parts[2], 59);
  return `${hour}:${minute}:${second}`;
}

function normalizeTimePart(value = "", max: number) {
  const numeric = Number(value.replace(/\D/g, ""));
  if (!Number.isFinite(numeric)) return "00";
  return String(Math.max(0, Math.min(max, numeric))).padStart(2, "0");
}

function SourceConnectionSelect({ connectionId, connections, onChange }: { connectionId: string; connections: DatabaseConnection[]; onChange: (value: string) => void }) {
  const connection = connections.find((item) => item.id === connectionId);
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!open) return;
    const close = (event: MouseEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    };
    window.addEventListener("mousedown", close);
    return () => window.removeEventListener("mousedown", close);
  }, [open]);

  return (
    <div className="endpoint-select backup-source-select" ref={rootRef}>
      <span>源端连接</span>
      <div className="endpoint-control stacked compact">
        {connection && <DatabaseLogo type={connection.type} name={connection.name} />}
        <div className="endpoint-instance-picker">
          <button type="button" className="endpoint-instance-trigger" aria-label="源端连接" aria-haspopup="listbox" aria-expanded={open} onClick={() => setOpen((value) => !value)}>
            <span className="endpoint-instance-trigger-content">{connection && <DatabaseTypeIcon type={connection.type} />}{connection?.name} · {connection?.host}{connection?.region && connection.region !== "内网" && <RegionFlag region={connection.region} />}</span><ChevronDown size={16} />
          </button>
          {open && <div className="endpoint-instance-menu" role="listbox" aria-label="源端连接列表">{connections.map((item) => <button type="button" key={item.id} className={`endpoint-instance-option ${item.id === connectionId ? "active" : ""}`} onClick={() => { onChange(item.id); setOpen(false); }}><div><strong><DatabaseTypeIcon type={item.type} />{item.name} · {item.host}{item.region && item.region !== "内网" && <RegionFlag region={item.region} />}</strong></div></button>)}</div>}
        </div>
      </div>
    </div>
  );
}

function DatabaseMultiSelect({ type, databases, loading, selected, onChange }: { type?: DatabaseConnection["type"]; databases: DatabaseInfo[]; loading: boolean; selected: string[]; onChange: (value: string[]) => void }) {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement | null>(null);
  const selectedSet = new Set(selected);
  const selectedItems = databases.filter((item) => selectedSet.has(item.name));

  useEffect(() => {
    if (loading) setOpen(false);
  }, [loading]);

  useEffect(() => {
    if (!open) return;
    const close = (event: MouseEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    };
    window.addEventListener("mousedown", close);
    return () => window.removeEventListener("mousedown", close);
  }, [open]);

  function toggle(name: string) {
    onChange(selected.includes(name) ? selected.filter((item) => item !== name) : [...selected, name]);
  }

  return (
    <div className="field backup-database-picker" ref={rootRef}>
      <span>源数据库</span>
      <button type="button" className="multi-select-trigger" aria-expanded={open} disabled={loading} onClick={() => setOpen((value) => !value)}>
        <span>{loading ? "正在加载数据库..." : selectedItems.length ? selectedItems.map((item) => item.name).join(", ") : "请选择数据库"}</span>
        <ChevronDown size={16} />
      </button>
      {open && (
        <div className="multi-select-menu">
          <div className="multi-select-actions">
            <button type="button" onClick={() => onChange(databases.map((item) => item.name))}>全选</button>
            <button type="button" onClick={() => onChange([])}>清空</button>
          </div>
          {databases.length ? databases.map((item) => <button type="button" key={item.name} className={selected.includes(item.name) ? "active" : ""} onClick={() => toggle(item.name)}><span className={`checkbox ${selected.includes(item.name) ? "checked" : ""}`}>{selected.includes(item.name) && <Check size={13} />}</span><strong>{item.name}</strong><small>{formatDatabaseMetric(type, item)}</small></button>) : <div className="multi-select-empty">当前连接未发现可备份数据库</div>}
        </div>
      )}
    </div>
  );
}

function Endpoint({ connection, database }: { connection?: DatabaseConnection; database: string }) {
  return <div className="endpoint-summary">{connection && <DatabaseLogo type={connection.type} name={connection.name} />}<div><span>源端</span><strong>{connection?.name} / {database}</strong><small>{connection?.host}:{connection?.port}</small></div></div>;
}

function storageTypeLabel(storage?: RemoteStorage) {
  if (!storage) return "未选择";
  if (storage.type === "object") return "对象存储";
  if (storage.type === "local") return "本地目录";
  return storage.provider;
}

function formatBytes(value: number) {
  if (!value) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  return `${(value / 1024 ** index).toFixed(index > 2 ? 1 : 0)} ${units[index]}`;
}

function formatDatabaseMetric(type: DatabaseConnection["type"] | undefined, database: DatabaseInfo) {
  if (type === "Neo4j") {
    return `${formatNumber(database.nodes ?? 0)} 节点 · ${formatNumber(database.relationships ?? 0)} 关系`;
  }
  return formatBytes(database.sizeBytes);
}

function formatNumber(value: number) {
  return new Intl.NumberFormat("zh-CN").format(value);
}

function backupFormatLabel(type: DatabaseConnection["type"] | undefined) {
  if (type === "MongoDB") return "mongodump + tar.gz";
  if (type === "Neo4j") return "cypher-shell + APOC + tar.gz";
  return "tar.gz";
}
