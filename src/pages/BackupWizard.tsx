import { Archive, ArrowLeft, ArrowRight, Check, ChevronDown, Clock, Info, ShieldCheck, X } from "lucide-react";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { api } from "../api";
import { NotificationRecipientPicker, useNotificationRecipients } from "../components/NotificationRecipientPicker";
import { Button, Card, DatabaseLogo, DatabaseTypeIcon, RegionFlag, SelectField } from "../components/UI";
import type {
  BackupConfig,
  BackupNotificationTrigger,
  DatabaseConnection,
  DatabaseInfo,
  NotificationRecipient,
  RemoteStorage,
} from "../types";

const steps = ["源数据库", "备份目标", "确认执行"];
const weekdays = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];
export const retentionDayOptions = [30, 60, 90, 180, 365, 730, 1095];

export function BackupWizard({
  connections,
  storages,
  onStart,
}: {
  connections: DatabaseConnection[];
  storages: RemoteStorage[];
  onStart: (config: BackupConfig) => Promise<void>;
}) {
  const writableStorages = useMemo(
    () =>
      storages.filter(
        (item) =>
          item.type === "object" ||
          item.type === "webdav" ||
          item.type === "local" ||
          (item.type === "one_drive" && item.status === "connected"),
      ),
    [storages],
  );
  const backupableConnections = useMemo(
    () =>
      connections.filter(
        (item) =>
          item.type === "PostgreSQL" ||
          item.type === "MySQL" ||
          item.type === "MongoDB" ||
          item.type === "Neo4j" ||
          item.type === "etcd",
      ),
    [connections],
  );

  const [step, setStep] = useState(0);
  const [databases, setDatabases] = useState<DatabaseInfo[]>([]);
  const [databaseConnectionId, setDatabaseConnectionId] = useState("");
  const [loading, setLoading] = useState(false);
  const [starting, setStarting] = useState(false);
  const [error, setError] = useState("");
  const [mongoGuideOpen, setMongoGuideOpen] = useState(false);
  const { recipients, loadingRecipients } = useNotificationRecipients();

  const [config, setConfig] = useState<BackupConfig>({
    name: "",
    sourceId: backupableConnections[0]?.id ?? "",
    sourceDatabase: "",
    sourceDatabases: [],
    destinationType: "storage",
    destination: writableStorages[0]?.id ?? "",
    primaryDestinationId: writableStorages[0]?.id ?? "",
    backupTargets: writableStorages[0]?.id ? [{ storageId: writableStorages[0].id, role: "primary", order: 0 }] : [],
    scheduleMode: "once",
    scheduleFrequency: "daily",
    scheduleTime: "02:00:00",
    scheduleWeekday: 1,
    scheduleMonthDay: 1,
    backupMode: "full",
    fullBackupIntervalDays: 7,
    restoreDrillEnabled: false,
    restoreDrillTargetId: "",
    retentionDays: 30,
    notificationEnabled: false,
    notificationRecipientIds: [],
    notificationTrigger: "all",
  });

  useEffect(() => {
    setConfig((current) => ({
      ...current,
      sourceId: backupableConnections.some((item) => item.id === current.sourceId)
        ? current.sourceId
        : (backupableConnections[0]?.id ?? ""),
      ...normalizeTargetSelection(current, writableStorages),
    }));
  }, [backupableConnections, writableStorages]);

  useEffect(() => {
    const sourceId = config.sourceId;
    setDatabases([]);
    setDatabaseConnectionId("");
    setConfig((current) => (current.sourceId === sourceId ? { ...current, sourceDatabase: "", sourceDatabases: [] } : current));
    if (!sourceId) {
      setLoading(false);
      return;
    }
    const selectedSource = backupableConnections.find((item) => item.id === sourceId);
    if (selectedSource?.type === "etcd") {
      const cluster = [{ name: "整个集群", sizeBytes: 0 }];
      setDatabases(cluster);
      setDatabaseConnectionId(sourceId);
      setConfig((current) =>
        current.sourceId === sourceId ? { ...current, sourceDatabase: "整个集群", sourceDatabases: ["整个集群"] } : current,
      );
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
          sourceDatabase: items.some((item) => item.name === current.sourceDatabase)
            ? current.sourceDatabase
            : (items[0]?.name ?? ""),
          sourceDatabases: current.sourceDatabases.filter((name) => items.some((item) => item.name === name)).length
            ? current.sourceDatabases.filter((name) => items.some((item) => item.name === name))
            : items[0]?.name
              ? [items[0].name]
              : [],
        }));
      })
      .catch((reason) => {
        if (!cancelled) setError(reason.message);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [config.sourceId]);

  const source = backupableConnections.find((item) => item.id === config.sourceId);
  const mongoOplogMode = source?.type === "MongoDB" && config.backupMode === "mongo_oplog";
  const mongoRestoreDrillTargets = backupableConnections.filter((item) => item.type === "MongoDB" && item.id !== config.sourceId);
  const selectedTargetIds = selectedBackupTargetIds(config, writableStorages);
  const primaryDestinationId = selectedTargetIds.includes(config.primaryDestinationId ?? "")
    ? (config.primaryDestinationId ?? "")
    : (selectedTargetIds[0] ?? "");
  const selectedTargets = selectedTargetIds
    .map((id) => writableStorages.find((item) => item.id === id))
    .filter(Boolean) as RemoteStorage[];
  const primaryDestination = writableStorages.find((item) => item.id === primaryDestinationId);
  const activeDatabases = databaseConnectionId === config.sourceId ? databases : [];
  const selectedDatabases = activeDatabases.filter((item) => config.sourceDatabases.includes(item.name));
  const databaseLabel = selectedDatabases.length === 1 ? selectedDatabases[0].name : `${selectedDatabases.length} 个数据库`;

  async function start() {
    const validDatabaseNames = new Set(activeDatabases.map((item) => item.name));
    const sourceDatabases = mongoOplogMode
      ? ["整个副本集"]
      : config.sourceDatabases.filter((name) => validDatabaseNames.has(name));
    if (loading || databaseConnectionId !== config.sourceId || !sourceDatabases.length) {
      setError("请等待当前源端数据库列表加载完成，并选择要备份的数据库");
      return;
    }
    if (mongoOplogMode && config.scheduleMode !== "scheduled") {
      setError("MongoDB oplog 增量备份必须使用定时任务");
      return;
    }
    if (mongoOplogMode && config.restoreDrillEnabled && !config.restoreDrillTargetId) {
      setError("请为全量后恢复演练选择独立的目标副本集");
      return;
    }
    if (config.scheduleMode === "scheduled" && config.notificationEnabled && !config.notificationRecipientIds?.length) {
      setError("请至少选择一个通知接收者，或关闭备份通知");
      return;
    }
    setStarting(true);
    setError("");
    try {
      const scheduled = config.scheduleMode === "scheduled";
      const targets = buildBackupTargets(selectedTargetIds, primaryDestinationId);
      await onStart({
        ...config,
        destinationType: targets.length > 1 ? "multi" : "storage",
        destination: primaryDestinationId,
        primaryDestinationId,
        backupTargets: targets,
        notificationEnabled: scheduled ? config.notificationEnabled : false,
        notificationRecipientIds: scheduled ? config.notificationRecipientIds : [],
        notificationTrigger: scheduled ? (config.notificationTrigger ?? "all") : undefined,
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
    return (
      <div className="empty">
        <div className="e-t">暂无可备份连接</div>
        <div className="e-s">请先添加 PostgreSQL、MySQL、MongoDB、Neo4j 或 etcd 连接，再创建备份任务。</div>
      </div>
    );
  }
  if (!writableStorages.length) {
    return (
      <div className="empty">
        <div className="e-t">暂无备份目标</div>
        <div className="e-s">请先在远端存储中添加对象存储、WebDAV 或本地目录。</div>
      </div>
    );
  }

  return (
    <div className="page-stack constrained">
      <div>
        <h1 className="page-h">创建备份任务</h1>
        <div className="sec-sub">配置源数据库与多目标同步存储 · 支持定时调度与 MongoDB oplog 增量</div>
      </div>

      <div className="steps">
        {steps.map((label, index) => (
          <div className={`step ${index === step ? "on" : index < step ? "done" : ""}`} key={label}>
            <span className="sn">{index < step ? <Check size={12} /> : index + 1}</span>
            <span>{label}</span>
          </div>
        ))}
      </div>

      {error ? <div className="error-banner">{error}</div> : null}

      {step === 0 ? (
        <Card className="pad">
          <h3 className="sec-title">{source?.type === "etcd" ? "选择要备份的 etcd 集群" : "选择要备份的数据库"}</h3>
          <div className="sec-sub" style={{ marginBottom: 16 }}>
            {source?.type === "etcd"
              ? "etcd 备份固定包含整个集群，不支持按 key 前缀拆分。"
              : "当前支持 PostgreSQL、MySQL、MongoDB 和 Neo4j 全库导出。"}
          </div>

          <div className="grid2">
            <label className="field">
              <span>任务名称</span>
              <input
                value={config.name}
                onChange={(event) => setConfig({ ...config, name: event.target.value })}
                placeholder={`${config.sourceDatabase || "数据库"} 备份`}
              />
            </label>
            <SourceConnectionSelect
              connectionId={config.sourceId}
              connections={backupableConnections}
              onChange={(sourceId) =>
                setConfig({
                  ...config,
                  sourceId,
                  sourceDatabase: "",
                  sourceDatabases: [],
                  backupMode: "full",
                  fullBackupIntervalDays: 7,
                })
              }
            />
          </div>

          {source?.type === "MongoDB" ? (
            <SelectField
              label="备份策略"
              value={config.backupMode ?? "full"}
              onChange={(backupMode) =>
                setConfig({
                  ...config,
                  backupMode: backupMode as BackupConfig["backupMode"],
                  sourceDatabase: backupMode === "mongo_oplog" ? "整个副本集" : "",
                  sourceDatabases: backupMode === "mongo_oplog" ? ["整个副本集"] : [],
                })
              }
            >
              <option value="full">全量备份</option>
              <option value="mongo_oplog">全量基线 + oplog 增量</option>
            </SelectField>
          ) : null}

          {source?.type === "etcd" || mongoOplogMode ? (
            <div className="info-callout">
              <Info size={15} style={{ flex: "none", marginTop: 2 }} />
              <span>备份范围：{source?.type === "etcd" ? "整个集群" : "整个副本集"}</span>
            </div>
          ) : (
            <DatabaseMultiSelect
              type={source?.type}
              databases={activeDatabases}
              loading={loading}
              selected={config.sourceDatabases}
              onChange={(sourceDatabases) => setConfig({ ...config, sourceDatabases, sourceDatabase: sourceDatabases.join(", ") })}
            />
          )}
        </Card>
      ) : null}

      {step === 1 ? (
        <Card className="pad">
          <h3 className="sec-title">选择备份目标</h3>
          <div className="sec-sub" style={{ marginBottom: 14 }}>
            备份产物会同步到选中的所有目标，恢复和下载优先使用主备份节点。
          </div>

          <BackupTargetSelector
            storages={writableStorages}
            selectedIds={selectedTargetIds}
            primaryId={primaryDestinationId}
            onChange={(nextIds, nextPrimaryId) => {
              const targets = buildBackupTargets(nextIds, nextPrimaryId);
              setConfig({
                ...config,
                destination: nextPrimaryId,
                primaryDestinationId: nextPrimaryId,
                backupTargets: targets,
                destinationType: targets.length > 1 ? "multi" : "storage",
              });
            }}
          />

          <div style={{ marginTop: 18 }}>
            <div className="h-row" style={{ marginBottom: 8 }}>
              <strong style={{ fontSize: 13.5 }}>执行方式</strong>
              <span className="sp" />
              {mongoOplogMode ? (
                <Button type="button" variant="ghost" className="sm" onClick={() => setMongoGuideOpen(true)}>
                  <Info size={14} /> Oplog 与 RPO 说明
                </Button>
              ) : null}
            </div>

            <div className="radio-cards" style={{ marginBottom: 14 }}>
              <button
                type="button"
                className={`radio-card ${config.scheduleMode === "once" ? "on" : ""}`}
                onClick={() =>
                  setConfig({
                    ...config,
                    scheduleMode: "once",
                    notificationEnabled: false,
                    notificationRecipientIds: [],
                    notificationTrigger: "all",
                  })
                }
              >
                <div className="rc-h">一次性</div>
                <div className="rc-d">创建后立即执行一次导出与上传。</div>
              </button>
              <button
                type="button"
                className={`radio-card ${config.scheduleMode === "scheduled" ? "on" : ""}`}
                onClick={() => setConfig({ ...config, scheduleMode: "scheduled" })}
              >
                <div className="rc-h">定时计划</div>
                <div className="rc-d">按频率自动触发，支持保留清理与结果通知。</div>
              </button>
            </div>

            {config.scheduleMode === "scheduled" ? (
              <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
                <div className="grid2">
                  <SelectField
                    label="频率"
                    value={config.scheduleFrequency ?? "daily"}
                    onChange={(scheduleFrequency) =>
                      setConfig({ ...config, scheduleFrequency: scheduleFrequency as BackupConfig["scheduleFrequency"] })
                    }
                  >
                    <option value="hourly">每小时</option>
                    <option value="daily">每天</option>
                    <option value="weekly">每周</option>
                    <option value="monthly">每月</option>
                  </SelectField>
                  <TimeSelect value={config.scheduleTime ?? "02:00:00"} onChange={(scheduleTime) => setConfig({ ...config, scheduleTime })} />
                  {config.scheduleFrequency === "weekly" ? (
                    <SelectField
                      label="每周"
                      value={config.scheduleWeekday ?? 1}
                      onChange={(scheduleWeekday) => setConfig({ ...config, scheduleWeekday: Number(scheduleWeekday) })}
                    >
                      {weekdays.map((item, index) => (
                        <option key={item} value={index}>
                          {item}
                        </option>
                      ))}
                    </SelectField>
                  ) : null}
                  {config.scheduleFrequency === "monthly" ? (
                    <SelectField
                      label="每月"
                      value={config.scheduleMonthDay ?? 1}
                      onChange={(scheduleMonthDay) => setConfig({ ...config, scheduleMonthDay: Number(scheduleMonthDay) })}
                    >
                      {Array.from({ length: 28 }, (_, index) => index + 1).map((day) => (
                        <option key={day} value={day}>
                          {day} 日
                        </option>
                      ))}
                    </SelectField>
                  ) : null}
                  <RetentionDaysField
                    value={config.retentionDays ?? 30}
                    onChange={(retentionDays) => setConfig({ ...config, retentionDays })}
                  />
                  {mongoOplogMode ? (
                    <SelectField
                      label="全量基线周期"
                      value={config.fullBackupIntervalDays ?? 7}
                      onChange={(fullBackupIntervalDays) =>
                        setConfig({ ...config, fullBackupIntervalDays: Number(fullBackupIntervalDays) })
                      }
                    >
                      <option value={1}>每天</option>
                      <option value={3}>每 3 天</option>
                      <option value={7}>每周</option>
                      <option value={14}>每 2 周</option>
                      <option value={30}>每月</option>
                    </SelectField>
                  ) : null}
                </div>

                {mongoOplogMode ? (
                  <div>
                    <button
                      type="button"
                      className="toggle-row"
                      onClick={() => setConfig({ ...config, restoreDrillEnabled: !config.restoreDrillEnabled })}
                    >
                      <span className={`switch ${config.restoreDrillEnabled ? "on" : ""}`}>
                        <i />
                      </span>
                      <div>
                        <strong>全量后恢复演练</strong>
                        <small>每次生成全量基线后，恢复到独立的目标副本集并校验</small>
                      </div>
                    </button>
                    {config.restoreDrillEnabled ? (
                      <SelectField
                        label="演练目标副本集"
                        value={config.restoreDrillTargetId ?? ""}
                        onChange={(restoreDrillTargetId) => setConfig({ ...config, restoreDrillTargetId })}
                      >
                        <option value="">选择独立 MongoDB 连接</option>
                        {mongoRestoreDrillTargets.map((connection) => (
                          <option key={connection.id} value={connection.id}>
                            {connection.name} · {connection.host}:{connection.port}
                          </option>
                        ))}
                      </SelectField>
                    ) : null}
                  </div>
                ) : null}

                <div>
                  <button
                    type="button"
                    className="toggle-row"
                    onClick={() =>
                      setConfig({
                        ...config,
                        notificationEnabled: !config.notificationEnabled,
                        notificationTrigger: config.notificationTrigger ?? "all",
                      })
                    }
                  >
                    <span className={`switch ${config.notificationEnabled ? "on" : ""}`}>
                      <i />
                    </span>
                    <div>
                      <strong>备份通知</strong>
                      <small>选择定时任务结束后的通知发送范围</small>
                    </div>
                  </button>
                  {config.notificationEnabled ? (
                    <div className="card pad" style={{ background: "var(--surface-2)" }}>
                      <NotificationTriggerSelector
                        value={config.notificationTrigger ?? "all"}
                        onChange={(notificationTrigger) => setConfig({ ...config, notificationTrigger })}
                      />
                      <div style={{ marginTop: 12 }}>
                        <span style={{ display: "block", fontSize: 13, fontWeight: 550, marginBottom: 6 }}>通知接收者</span>
                        <NotificationRecipientPicker
                          recipients={recipients}
                          loading={loadingRecipients}
                          selected={config.notificationRecipientIds ?? []}
                          onChange={(notificationRecipientIds) => setConfig({ ...config, notificationRecipientIds })}
                        />
                      </div>
                    </div>
                  ) : null}
                </div>
              </div>
            ) : null}
          </div>
        </Card>
      ) : null}

      {step === 2 ? (
        <Card className="pad">
          <div style={{ display: "flex", alignItems: "center", gap: 12, marginBottom: 16 }}>
            <span
              style={{
                width: 36,
                height: 36,
                borderRadius: 10,
                background: "var(--ok-soft)",
                color: "var(--ok)",
                display: "grid",
                placeItems: "center",
              }}
            >
              <Check size={18} />
            </span>
            <div>
              <h3 className="sec-title">确认执行备份</h3>
              <div className="sec-sub">任务启动后会导出源数据库并写入目标存储。</div>
            </div>
          </div>

          <div className="kv">
            <span className="k">源端连接</span>
            <span className="v">
              {source?.name}（{source?.type}）
            </span>
          </div>
          <div className="kv">
            <span className="k">源数据库</span>
            <span className="v">{config.sourceDatabases.join(", ")}</span>
          </div>
          <div className="kv">
            <span className="k">主备份节点</span>
            <span className="v">
              {primaryDestination?.name}（共 {selectedTargets.length} 个目标）
            </span>
          </div>
          <div className="kv">
            <span className="k">执行方式</span>
            <span className="v">{scheduleLabel(config)}</span>
          </div>
          <div className="kv">
            <span className="k">保留策略</span>
            <span className="v">{retentionPolicyLabel(config)}</span>
          </div>
          <div className="kv">
            <span className="k">备份通知</span>
            <span className="v">{notificationLabel(config, recipients)}</span>
          </div>
          <div className="kv">
            <span className="k">文件格式</span>
            <span className="v mono">{backupFormatLabel(source?.type)}</span>
          </div>
        </Card>
      ) : null}

      <div className="h-row">
        <span style={{ fontSize: 12, color: "var(--muted)" }}>备份文件会写入本地任务记录，密钥仅在后端加密保存</span>
        <span className="sp" />
        {step > 0 ? (
          <Button variant="secondary" onClick={() => setStep(step - 1)}>
            <ArrowLeft size={15} />
            上一步
          </Button>
        ) : null}
        <Button
          disabled={loading || starting || databaseConnectionId !== config.sourceId || !config.sourceDatabases.length || !primaryDestinationId}
          onClick={() => (step === 2 ? void start() : setStep(step + 1))}
        >
          {starting ? "正在创建..." : step === 2 ? (config.scheduleMode === "scheduled" ? "创建计划" : "启动备份") : "下一步"}
          <ArrowRight size={15} />
        </Button>
      </div>

      {mongoGuideOpen ? <MongoOplogGuide onClose={() => setMongoGuideOpen(false)} /> : null}
    </div>
  );
}

function MongoOplogGuide({ onClose }: { onClose: () => void }) {
  const inspectCommand = "db.getReplicationInfo().timeDiffHours\nrs.status().members.map(m => ({ name: m.name, state: m.stateStr }))";
  const resizeCommand = "use admin\ndb.adminCommand({\n  replSetResizeOplog: 1,\n  size: Double(16384),\n  minRetentionHours: Double(72)\n})";
  return createPortal(
    <div className="modal-backdrop" onMouseDown={onClose}>
      <div className="modal wide" role="dialog" aria-modal="true" onMouseDown={(event) => event.stopPropagation()}>
        <div className="modal-h">
          <div style={{ flex: 1 }}>
            <h3>MongoDB 增量备份说明</h3>
            <div className="ms">副本集 oplog、RPO 与容量配置</div>
          </div>
          <button type="button" className="icon-btn ghost" aria-label="关闭说明" onClick={onClose}>
            <X size={17} />
          </button>
        </div>
        <div className="modal-b" style={{ display: "flex", flexDirection: "column", gap: 12 }}>
          <div className="kv">
            <span className="k">oplog</span>
            <span className="v" style={{ textAlign: "left" }}>
              副本集记录数据变更的滚动日志。增量备份依赖它获取两次备份之间的变化。
            </span>
          </div>
          <div className="kv">
            <span className="k">所需窗口</span>
            <span className="v" style={{ textAlign: "left" }}>
              增量间隔 + 最近全量耗时 + 12 小时缓冲；生产保护下限为 48 小时。
            </span>
          </div>
          <div>
            <strong style={{ fontSize: 13 }}>检查当前 oplog 窗口</strong>
            <pre className="codebox">{inspectCommand}</pre>
          </div>
          <div>
            <strong style={{ fontSize: 13 }}>自建副本集在线扩容</strong>
            <pre className="codebox">{resizeCommand}</pre>
          </div>
        </div>
        <div className="modal-f">
          <Button type="button" variant="secondary" onClick={onClose}>
            关闭
          </Button>
        </div>
      </div>
    </div>,
    document.body,
  );
}

export function selectedBackupTargetIds(config: BackupConfig, storages: RemoteStorage[]) {
  const validIds = new Set(storages.map((item) => item.id));
  const ids = (config.backupTargets?.length ? config.backupTargets.map((target) => target.storageId) : [config.destination]).filter(
    (id): id is string => Boolean(id && validIds.has(id)),
  );
  if (!ids.length && storages[0]?.id) return [storages[0].id];
  return Array.from(new Set(ids));
}

export function normalizeTargetSelection(config: BackupConfig, storages: RemoteStorage[]) {
  const selectedIds = selectedBackupTargetIds(config, storages);
  const primaryId = selectedIds.includes(config.primaryDestinationId ?? "")
    ? (config.primaryDestinationId ?? "")
    : (selectedIds[0] ?? "");
  const targets = buildBackupTargets(selectedIds, primaryId);
  return {
    destination: primaryId,
    primaryDestinationId: primaryId,
    backupTargets: targets,
    destinationType: targets.length > 1 ? ("multi" as const) : ("storage" as const),
  };
}

export function buildBackupTargets(selectedIds: string[], primaryId: string) {
  const ids = Array.from(new Set(selectedIds.filter(Boolean)));
  const resolvedPrimary = ids.includes(primaryId) ? primaryId : (ids[0] ?? "");
  return ids
    .map((storageId, index) => ({
      storageId,
      role: storageId === resolvedPrimary ? ("primary" as const) : ("secondary" as const),
      order: storageId === resolvedPrimary ? 0 : index + 1,
    }))
    .sort((left, right) => {
      if (left.role === "primary") return -1;
      if (right.role === "primary") return 1;
      return left.order - right.order;
    });
}

export function BackupTargetSelector({
  storages,
  selectedIds,
  primaryId,
  onChange,
}: {
  storages: RemoteStorage[];
  selectedIds: string[];
  primaryId: string;
  onChange: (selectedIds: string[], primaryId: string) => void;
}) {
  function toggleStorage(storageId: string) {
    const selected = selectedIds.includes(storageId);
    const nextIds = selected ? selectedIds.filter((id) => id !== storageId) : [...selectedIds, storageId];
    const nextPrimary = nextIds.includes(primaryId) ? primaryId : (nextIds[0] ?? "");
    onChange(nextIds, nextPrimary);
  }

  return (
    <div className="ledger">
      {storages.map((storage) => {
        const selected = selectedIds.includes(storage.id);
        const primary = selected && primaryId === storage.id;
        return (
          <div className="lrow" style={{ cursor: "default", padding: "10px 14px" }} key={storage.id}>
            <input
              type="checkbox"
              checked={selected}
              onChange={() => toggleStorage(storage.id)}
              style={{ width: 16, height: 16, accentColor: "var(--indigo)" }}
            />
            <Archive size={16} style={{ color: "var(--muted)" }} />
            <div className="lr-main">
              <div className="lr-t" style={{ fontSize: 13.5 }}>
                {storage.name}
                <span className="tag">{storageTypeLabel(storage)}</span>
              </div>
              <div className="lr-s">{storage.bucket || storage.path || storage.provider}</div>
            </div>
            <Button
              type="button"
              variant={primary ? "primary" : "secondary"}
              className="sm"
              disabled={!selected || primary}
              onClick={() => onChange(selectedIds, storage.id)}
            >
              <ShieldCheck size={13} />
              {primary ? "主节点" : "设为主节点"}
            </Button>
          </div>
        );
      })}
    </div>
  );
}

const notificationTriggerOptions: Array<{ value: BackupNotificationTrigger; label: string }> = [
  { value: "all", label: "全部发送" },
  { value: "success", label: "成功发送" },
  { value: "failure", label: "失败发送" },
];

export function NotificationTriggerSelector({
  value,
  onChange,
}: {
  value: BackupNotificationTrigger;
  onChange: (value: BackupNotificationTrigger) => void;
}) {
  return (
    <div>
      <span style={{ display: "block", fontSize: 13, fontWeight: 550, marginBottom: 6 }}>发送范围</span>
      <div className="chiprow" role="radiogroup" aria-label="通知发送范围">
        {notificationTriggerOptions.map((item) => (
          <button
            type="button"
            key={item.value}
            className={`fchip ${value === item.value ? "on" : ""}`}
            role="radio"
            aria-checked={value === item.value}
            onClick={() => onChange(item.value)}
          >
            {item.label}
          </button>
        ))}
      </div>
    </div>
  );
}

export function notificationTriggerLabel(value: BackupNotificationTrigger) {
  return notificationTriggerOptions.find((item) => item.value === value)?.label ?? "全部发送";
}

export function RetentionDaysField({ value, onChange }: { value: number; onChange: (value: number) => void }) {
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState(String(value || 30));
  const rootRef = useRef<HTMLDivElement | null>(null);
  const menuRef = useRef<HTMLDivElement | null>(null);
  const [pos, setPos] = useState<{ left: number; top: number; minWidth: number }>({ left: 0, top: 0, minWidth: 160 });

  useEffect(() => setDraft(String(value || 30)), [value]);

  const updatePos = useCallback(() => {
    const el = rootRef.current;
    if (!el) return;
    const r = el.getBoundingClientRect();
    setPos({ left: r.left, top: r.bottom + 6, minWidth: r.width });
  }, []);

  useLayoutEffect(() => {
    if (open) updatePos();
  }, [open, updatePos]);

  useEffect(() => {
    if (!open) return;
    const close = (event: MouseEvent) => {
      const target = event.target as Node;
      if (rootRef.current?.contains(target) || menuRef.current?.contains(target)) return;
      commitRetentionDays(draft, onChange, setDraft);
      setOpen(false);
    };
    window.addEventListener("mousedown", close);
    return () => window.removeEventListener("mousedown", close);
  }, [draft, onChange, open]);

  return (
    <div className="field" ref={rootRef}>
      <span>保留期限（天）</span>
      <div style={{ display: "flex", gap: 6 }}>
        <input
          className="inp num"
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
        <Button type="button" variant="secondary" onClick={() => setOpen((c) => !c)} aria-label="展开保留期限预设">
          <ChevronDown size={14} />
        </Button>
      </div>
      {open
        ? createPortal(
            <div ref={menuRef} className="csel-list" style={{ left: pos.left, top: pos.top, minWidth: pos.minWidth }}>
              {retentionDayOptions.map((days) => (
                <button
                  type="button"
                  key={days}
                  className="csel-opt"
                  aria-selected={Number(draft) === days}
                  onClick={() => {
                    setDraft(String(days));
                    onChange(days);
                    setOpen(false);
                  }}
                >
                  <span className="csel-txt">{days} 天</span>
                </button>
              ))}
            </div>,
            document.body,
          )
        : null}
    </div>
  );
}

function commitRetentionDays(input: string, onChange: (value: number) => void, setDraft: (value: string) => void) {
  const parsed = Number(input);
  const next = Number.isFinite(parsed) ? Math.max(1, Math.min(1095, Math.floor(parsed))) : 30;
  setDraft(String(next));
  onChange(next);
}

export function TimeSelect({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);

  function commit(input: string) {
    const nextValue = normalizeScheduleTime(input);
    setDraft(nextValue);
    onChange(nextValue);
  }

  return (
    <label className="field">
      <span>执行时间（HH:mm:ss）</span>
      <span className="sfield" style={{ width: "100%" }}>
        <span className="sfield-ic">
          <Clock size={14} />
        </span>
        <input
          className="inp mono"
          inputMode="numeric"
          value={draft}
          placeholder="02:00:00"
          onBlur={(e) => commit(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") commit(e.currentTarget.value);
          }}
          onChange={(event) => setDraft(event.target.value.replace(/[^\d:]/g, "").slice(0, 8))}
          style={{ width: "100%", height: 38 }}
          aria-label="执行时间"
        />
      </span>
    </label>
  );
}

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

function SourceConnectionSelect({
  connectionId,
  connections,
  onChange,
}: {
  connectionId: string;
  connections: DatabaseConnection[];
  onChange: (value: string) => void;
}) {
  const connection = connections.find((item) => item.id === connectionId);
  const [open, setOpen] = useState(false);
  const triggerRef = useRef<HTMLButtonElement | null>(null);
  const menuRef = useRef<HTMLDivElement | null>(null);
  const [pos, setPos] = useState<{ left: number; top: number; minWidth: number }>({ left: 0, top: 0, minWidth: 220 });

  const updatePos = useCallback(() => {
    const tr = triggerRef.current;
    if (!tr) return;
    const r = tr.getBoundingClientRect();
    setPos({ left: r.left, top: r.bottom + 6, minWidth: r.width });
  }, []);

  useLayoutEffect(() => {
    if (open) updatePos();
  }, [open, updatePos]);

  useEffect(() => {
    if (!open) return;
    const close = (event: MouseEvent) => {
      const target = event.target as Node;
      if (triggerRef.current?.contains(target) || menuRef.current?.contains(target)) return;
      setOpen(false);
    };
    window.addEventListener("mousedown", close);
    return () => window.removeEventListener("mousedown", close);
  }, [open]);

  return (
    <div className="field">
      <span>源端连接</span>
      <button
        ref={triggerRef}
        type="button"
        className="csel-trigger"
        aria-label="源端连接"
        aria-haspopup="listbox"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
      >
        <span className="csel-val">
          {connection ? <DatabaseTypeIcon type={connection.type} /> : null}
          <span className="csel-txt">{connection ? `${connection.name} · ${connection.host}` : "请选择源端"}</span>
          {connection?.region && connection.region !== "内网" ? <RegionFlag region={connection.region} /> : null}
        </span>
        <span className="csel-chev">
          <ChevronDown size={14} />
        </span>
      </button>
      {open
        ? createPortal(
            <div ref={menuRef} className="csel-list" role="listbox" style={{ left: pos.left, top: pos.top, minWidth: pos.minWidth }}>
              {connections.map((item) => (
                <button
                  type="button"
                  key={item.id}
                  className="csel-opt"
                  aria-selected={item.id === connectionId}
                  onClick={() => {
                    onChange(item.id);
                    setOpen(false);
                  }}
                >
                  <DatabaseTypeIcon type={item.type} />
                  <span className="csel-txt">
                    {item.name} · {item.host}
                  </span>
                </button>
              ))}
            </div>,
            document.body,
          )
        : null}
    </div>
  );
}

export function DatabaseMultiSelect({
  type,
  databases,
  loading,
  selected,
  onChange,
}: {
  type?: DatabaseConnection["type"];
  databases: DatabaseInfo[];
  loading: boolean;
  selected: string[];
  onChange: (value: string[]) => void;
}) {
  function toggle(name: string) {
    onChange(selected.includes(name) ? selected.filter((item) => item !== name) : [...selected, name]);
  }

  return (
    <div className="field">
      <div className="h-row" style={{ marginBottom: 8 }}>
        <span style={{ fontSize: 13, fontWeight: 550 }}>源数据库（已选 {selected.length} / {databases.length}）</span>
        <span className="sp" />
        <Button type="button" variant="secondary" className="sm" disabled={loading || !databases.length} onClick={() => onChange(databases.map((d) => d.name))}>
          全选
        </Button>
        <Button type="button" variant="secondary" className="sm" disabled={loading || !databases.length} onClick={() => onChange([])}>
          清空
        </Button>
      </div>
      {loading ? (
        <div style={{ color: "var(--muted)", fontSize: 13 }}>正在加载数据库...</div>
      ) : databases.length ? (
        <div className="ledger" style={{ maxHeight: 220, overflowY: "auto" }}>
          {databases.map((item) => {
            const checked = selected.includes(item.name);
            return (
              <label key={item.name} className="lrow" style={{ cursor: "pointer", padding: "9px 14px" }}>
                <input
                  type="checkbox"
                  checked={checked}
                  onChange={() => toggle(item.name)}
                  style={{ width: 16, height: 16, accentColor: "var(--indigo)" }}
                />
                <div className="lr-main">
                  <div className="lr-t" style={{ fontSize: 13.5 }}>
                    {item.name}
                  </div>
                </div>
                <span className="num" style={{ fontSize: 12.5, color: "var(--muted)" }}>
                  {formatDatabaseMetric(type, item)}
                </span>
              </label>
            );
          })}
        </div>
      ) : (
        <div style={{ color: "var(--muted)", fontSize: 13 }}>当前连接未发现可备份数据库</div>
      )}
    </div>
  );
}

function scheduleLabel(config: BackupConfig) {
  if (config.scheduleMode !== "scheduled") return "一次性立即执行";
  if (config.scheduleFrequency === "hourly") return `每小时第 ${(config.scheduleTime ?? "02:00:00").slice(3)}`;
  if (config.scheduleFrequency === "weekly") return `每周${weekdays[config.scheduleWeekday ?? 1]} ${config.scheduleTime}`;
  if (config.scheduleFrequency === "monthly") return `每月 ${config.scheduleMonthDay ?? 1} 日 ${config.scheduleTime}`;
  return `每天 ${config.scheduleTime}`;
}

function notificationLabel(config: BackupConfig, recipients?: NotificationRecipient[]) {
  if (config.scheduleMode !== "scheduled" || !config.notificationEnabled) return "未开启";
  const names = (config.notificationRecipientIds ?? []).map((id) => recipients?.find((item) => item.id === id)?.name).filter(Boolean);
  const trigger = notificationTriggerLabel(config.notificationTrigger ?? "all");
  return names.length ? `${trigger}：${names.join("、")}` : `${trigger}：未选择接收者`;
}

function retentionPolicyLabel(config: BackupConfig) {
  if (config.scheduleMode !== "scheduled") return "不适用";
  return `${config.retentionDays ?? 30} 天`;
}

function storageTypeLabel(storage?: RemoteStorage) {
  if (!storage) return "未选择";
  if (storage.type === "object") return "对象存储";
  if (storage.type === "webdav") return "WebDAV";
  if (storage.type === "one_drive") return "OneDrive";
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
  if (type === "etcd") return "etcd snapshot (.db) + tar.gz";
  if (type === "MongoDB") return "mongodump + tar.gz";
  if (type === "Neo4j") return "cypher-shell + APOC + tar.gz";
  return "tar.gz";
}
