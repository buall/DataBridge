import {
  AlertTriangle,
  CalendarClock,
  Check,
  Copy,
  Database,
  Download,
  Edit3,
  FileSpreadsheet,
  Info,
  Play,
  Plus,
  RefreshCw,
  Search,
  ShieldCheck,
  Star,
  Trash2,
  WandSparkles,
  X,
} from "lucide-react";
import { useEffect, useId, useMemo, useState } from "react";
import { createPortal } from "react-dom";
import { api } from "../api";
import { BatchImportModal } from "../components/BatchImportModal";
import {
  ActionMenu,
  Badge,
  Button,
  ConfirmDialog,
  DatabaseLogo,
  DatabaseTypeIcon,
  DensitySwitch,
  LatencyIndicator,
  RegionFlag,
  requiredLabel,
  SelectField,
  useCollectionView,
  type ActionMenuItem,
} from "../components/UI";
import { connectionEnvironmentLabel, connectionEnvironmentTone, CONNECTION_ENVIRONMENT_OPTIONS } from "../labels";
import type {
  BackupJob,
  BatchImportResult,
  ConnectionEnvironment,
  ConnectionInput,
  ConnectionTLSConfig,
  DatabaseConnection,
  DatabaseInfo,
  DatabaseType,
  DuplicateConnectionInput,
  SeedDataResult,
} from "../types";

import {
  ConnectionMetadataFields,
  EtcdTLSHint,
  EtcdTLSModeGuide,
  MongoConnectionModeFields,
  TLSCertificateFields,
  compactVersionLabel,
  connectionCompatibilityHint,
  connectionHealthLabel,
  connectionInputFromForm,
  connectionSecurityConfig,
  connectionStatusLabel,
  connectionStatusTone,
  defaultPort,
  defaultSecurityMode,
  duplicateConnectionName,
  emptyConnectionForm,
  emptyTLSConfig,
  formatDatabaseMetric,
  groupScheduledBackupsByConnection,
  mongoBackupReadinessLabel,
  scheduleLabel,
  scheduledBackupTime,
  scheduledBackupsForConnection,
  seedDataHint,
  seedDataTargetSuffix,
  sortScheduledBackups,
  suggestConnectionName,
  validateConnectionForm,
  type ConnectionFormErrors,
  type ConnectionFormState,
  type DuplicateConnectionFormState,
  type EditConnectionFormState,
} from "../components/connections/connectionShared";

export function Connections({
  connections,
  backups,
  onAdd,
  onUpdate,
  onDelete,
  onToggleFavorite,
  onCreateMigration,
  onBatchImported,
}: {
  connections: DatabaseConnection[];
  backups: BackupJob[];
  onAdd: (connection: DatabaseConnection) => void;
  onUpdate: (connection: DatabaseConnection) => void;
  onDelete: (connectionId: string) => void;
  onToggleFavorite: (connection: DatabaseConnection) => Promise<void>;
  onCreateMigration: (sourceId?: string) => void;
  onBatchImported: (result: BatchImportResult) => void;
}) {
  const supportsMigration = (type: DatabaseType) =>
    type === "PostgreSQL" || type === "MySQL" || type === "MongoDB" || type === "Neo4j";
  const supportsDirectDatabaseCreate = (type: DatabaseType) =>
    type === "PostgreSQL" || type === "MySQL" || type === "MongoDB" || type === "Neo4j";
  const supportsSeedData = (type: DatabaseType) =>
    type === "PostgreSQL" || type === "MySQL" || type === "MongoDB" || type === "Neo4j";

  const { prefs, updatePref } = useCollectionView();
  const [showForm, setShowForm] = useState(false);
  const [showBatchImport, setShowBatchImport] = useState(false);
  const [editModal, setEditModal] = useState<{
    connection: DatabaseConnection;
    form: EditConnectionFormState;
    submitting: boolean;
    error: string;
  } | null>(null);
  const [duplicateModal, setDuplicateModal] = useState<{
    connection: DatabaseConnection;
    form: DuplicateConnectionFormState;
    submitting: boolean;
    error: string;
  } | null>(null);
  const [testing, setTesting] = useState<string | null>(null);
  const [inspectedId, setInspectedId] = useState<string | null>(null);
  const [inspectorDbs, setInspectorDbs] = useState<{
    connectionId: string;
    databases: DatabaseInfo[];
    loading: boolean;
    error: string;
  } | null>(null);
  const [databaseModal, setDatabaseModal] = useState<{
    connection: DatabaseConnection;
    databases: DatabaseInfo[];
    loading: boolean;
    error: string;
  } | null>(null);
  const [createDatabaseModal, setCreateDatabaseModal] = useState<{
    connection: DatabaseConnection;
    name: string;
    submitting: boolean;
    error: string;
  } | null>(null);
  const [seedDataModal, setSeedDataModal] = useState<{
    connection: DatabaseConnection;
    database: string;
    rows: string;
    mode: "append" | "replace";
    template: "users" | "orders" | "products" | "payload";
    databases: DatabaseInfo[];
    loading: boolean;
    submitting: boolean;
    error: string;
    result: SeedDataResult | null;
  } | null>(null);
  const [seedDataConfirmOpen, setSeedDataConfirmOpen] = useState(false);
  const [deleteModal, setDeleteModal] = useState<{
    connection: DatabaseConnection;
    submitting: boolean;
    error: string;
  } | null>(null);
  const [copied, setCopied] = useState<string | null>(null);
  const [manualCopyValue, setManualCopyValue] = useState("");
  const [actionError, setActionError] = useState("");
  const [favoritingId, setFavoritingId] = useState<string | null>(null);

  const [query, setQuery] = useState("");
  const [typeFilter, setTypeFilter] = useState<string>("全部");
  const [statusFilter, setStatusFilter] = useState<string>("全部");
  const [envFilter, setEnvFilter] = useState<string>("全部");
  const [projectFilter, setProjectFilter] = useState<string>("全部");
  const [favOnly, setFavOnly] = useState(false);

  const [form, setForm] = useState<ConnectionFormState>(emptyConnectionForm("PostgreSQL"));
  const [nameCustomized, setNameCustomized] = useState(false);
  const [submittingAction, setSubmittingAction] = useState<"save" | "test" | null>(null);
  const [formError, setFormError] = useState("");
  const [formErrors, setFormErrors] = useState<ConnectionFormErrors>({});

  const scheduledBackupsByConnection = useMemo(() => groupScheduledBackupsByConnection(backups), [backups]);

  const projectSuggestions = useMemo(
    () =>
      Array.from(new Set(connections.map((item) => item.project?.trim() ?? "").filter(Boolean))).sort((a, b) =>
        a.localeCompare(b, "zh-CN"),
      ),
    [connections],
  );

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return connections
      .filter((item) => {
        if (typeFilter !== "全部" && item.type !== typeFilter) return false;
        if (statusFilter !== "全部" && connectionStatusLabel(item.status) !== statusFilter) return false;
        if (envFilter !== "全部") {
          if (envFilter === "__none__" && item.environment) return false;
          if (envFilter !== "__none__" && item.environment !== envFilter) return false;
        }
        if (projectFilter !== "全部") {
          if (projectFilter === "__none__" && item.project) return false;
          if (projectFilter !== "__none__" && item.project !== projectFilter) return false;
        }
        if (favOnly && !item.favorite) return false;
        if (
          q &&
          !`${item.name} ${item.note ?? ""} ${item.project ?? ""} ${item.environment ?? ""} ${connectionEnvironmentLabel(item.environment)} ${(item.tags ?? []).join(" ")} ${item.host} ${item.type} ${item.region ?? ""}`
            .toLowerCase()
            .includes(q)
        ) {
          return false;
        }
        return true;
      })
      .sort((left, right) => Number(Boolean(right.favorite)) - Number(Boolean(left.favorite)));
  }, [connections, envFilter, favOnly, projectFilter, query, statusFilter, typeFilter]);

  const inspectedConnection = useMemo(
    () => (inspectedId ? connections.find((item) => item.id === inspectedId) ?? null : null),
    [connections, inspectedId],
  );

  const formSecurityConfig = connectionSecurityConfig(form.type);
  const formCompatibilityHint = connectionCompatibilityHint(form.type);

  useEffect(() => {
    if (nameCustomized) return;
    const generatedName = suggestConnectionName(form.type, form.host, form.port);
    if (form.name === generatedName) return;
    setForm((current) => ({ ...current, name: generatedName }));
  }, [form.type, form.host, form.port, form.name, nameCustomized]);

  useEffect(() => {
    if (!inspectedConnection || inspectedConnection.type === "etcd") {
      setInspectorDbs(null);
      return;
    }
    let cancelled = false;
    setInspectorDbs({ connectionId: inspectedConnection.id, databases: [], loading: true, error: "" });
    api.databases(inspectedConnection.id)
      .then((databases) => {
        if (!cancelled) setInspectorDbs({ connectionId: inspectedConnection.id, databases, loading: false, error: "" });
      })
      .catch((error) => {
        if (!cancelled) {
          setInspectorDbs({
            connectionId: inspectedConnection.id,
            databases: [],
            loading: false,
            error: error instanceof Error ? error.message : "数据库读取失败",
          });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [inspectedConnection?.id, inspectedConnection?.type]);

  function updateFormField<K extends keyof ConnectionFormState>(key: K, value: ConnectionFormState[K]) {
    setForm((current) => ({ ...current, [key]: value }));
  }

  function validateSingleField(key: keyof ConnectionFormErrors, nextForm: ConnectionFormState) {
    const nextErrors = validateConnectionForm(nextForm);
    setFormErrors((current) => ({ ...current, [key]: nextErrors[key] }));
  }

  function fallbackCopyText(value: string) {
    const textarea = document.createElement("textarea");
    textarea.value = value;
    textarea.readOnly = true;
    textarea.style.position = "fixed";
    textarea.style.left = "-9999px";
    textarea.style.top = "0";
    document.body.appendChild(textarea);
    textarea.select();
    textarea.setSelectionRange(0, value.length);
    const copiedOk = typeof document.execCommand === "function" && document.execCommand("copy");
    document.body.removeChild(textarea);
    return copiedOk;
  }

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const submitter = (event.nativeEvent as SubmitEvent).submitter as HTMLElement | null;
    const action = submitter?.dataset.action === "save" ? "save" : "test";
    const validate = action === "test";
    const validationErrors = validateConnectionForm(form);
    setFormErrors(validationErrors);
    if (Object.keys(validationErrors).length > 0) {
      setFormError("请先修正表单中的必填项和格式问题。");
      return;
    }
    setSubmittingAction(action);
    setFormError("");
    try {
      const input: ConnectionInput = { ...connectionInputFromForm(form), port: Number(form.port), validate };
      const connection = await api.createConnection(input);
      onAdd(connection);
      closeConnectionForm();
    } catch (error) {
      setFormError(error instanceof Error ? error.message : validate ? "连接失败" : "保存连接失败");
    } finally {
      setSubmittingAction(null);
    }
  }

  async function testConnection(id: string) {
    setTesting(id);
    setActionError("");
    try {
      const connection = await api.refreshConnection(id);
      onUpdate(connection);
    } catch (error) {
      const connection = connections.find((item) => item.id === id);
      if (connection) {
        onUpdate({ ...connection, status: "warning", latency: 0 });
      }
      setActionError(error instanceof Error ? error.message : "连接测试失败");
    } finally {
      setTesting(null);
    }
  }

  async function showDatabases(connection: DatabaseConnection) {
    setDatabaseModal({ connection, databases: [], loading: true, error: "" });
    try {
      const databases = await api.databases(connection.id);
      setDatabaseModal({ connection, databases, loading: false, error: "" });
    } catch (error) {
      setDatabaseModal({
        connection,
        databases: [],
        loading: false,
        error: error instanceof Error ? error.message : "数据库读取失败",
      });
    }
  }

  function openCreateDatabase(connection: DatabaseConnection) {
    setCreateDatabaseModal({ connection, name: "", submitting: false, error: "" });
  }

  async function openSeedData(connection: DatabaseConnection) {
    setSeedDataModal({
      connection,
      database: "",
      rows: "100",
      mode: "append",
      template: "users",
      databases: [],
      loading: true,
      submitting: false,
      error: "",
      result: null,
    });
    try {
      const databases = await api.databases(connection.id);
      setSeedDataModal({
        connection,
        database: databases[0]?.name ?? "",
        rows: "100",
        mode: "append",
        template: "users",
        databases,
        loading: false,
        submitting: false,
        error: "",
        result: null,
      });
    } catch (error) {
      setSeedDataModal({
        connection,
        database: "",
        rows: "100",
        mode: "append",
        template: "users",
        databases: [],
        loading: false,
        submitting: false,
        error: error instanceof Error ? error.message : "加载数据库失败",
        result: null,
      });
    }
  }

  function openEditConnection(connection: DatabaseConnection) {
    setEditModal({
      connection,
      form: {
        name: connection.name,
        note: connection.note ?? "",
        project: connection.project ?? "",
        environment: connection.environment ?? "",
        tags: [...(connection.tags ?? [])],
        host: connection.host,
        port: String(connection.port),
        username: connection.username,
        password: "",
        sslMode: connection.sslMode,
        tlsConfig: emptyTLSConfig(),
        mongoMode: connection.type === "MongoDB" && (connection.endpoints?.length ?? 0) > 0 ? "replica_set" : "direct",
        mongoSeeds: connection.endpoints?.join("\n") ?? "",
      },
      submitting: false,
      error: "",
    });
  }

  function openNewConnection() {
    setForm(emptyConnectionForm("PostgreSQL"));
    setNameCustomized(false);
    setFormError("");
    setFormErrors({});
    setShowForm(true);
  }

  function openCopyConnection(connection: DatabaseConnection) {
    setDuplicateModal({
      connection,
      form: {
        name: duplicateConnectionName(
          connection.name,
          connections.map((item) => item.name),
        ),
        note: connection.note ?? "",
        project: connection.project ?? "",
        environment: connection.environment ?? "",
        tags: [...(connection.tags ?? [])],
        host: connection.host,
        port: String(connection.port),
        username: connection.username,
        password: "",
        sslMode: connection.sslMode,
        tlsConfig: emptyTLSConfig(),
      },
      submitting: false,
      error: "",
    });
  }

  async function submitDuplicateConnection(event: React.FormEvent) {
    event.preventDefault();
    if (!duplicateModal) return;
    setDuplicateModal({ ...duplicateModal, submitting: true, error: "" });
    try {
      const input: DuplicateConnectionInput = {
        ...duplicateModal.form,
        port: Number(duplicateModal.form.port),
      };
      const connection = await api.duplicateConnection(duplicateModal.connection.id, input);
      onAdd(connection);
      setDuplicateModal(null);
    } catch (error) {
      setDuplicateModal({
        ...duplicateModal,
        submitting: false,
        error: error instanceof Error ? error.message : "复制连接失败",
      });
    }
  }

  function updateDuplicateForm(patch: Partial<DuplicateConnectionFormState>) {
    if (!duplicateModal) return;
    setDuplicateModal({ ...duplicateModal, form: { ...duplicateModal.form, ...patch }, error: "" });
  }

  function closeConnectionForm() {
    setShowForm(false);
    setForm(emptyConnectionForm("PostgreSQL"));
    setNameCustomized(false);
    setFormError("");
    setFormErrors({});
  }

  async function submitEditConnection(event: React.FormEvent) {
    event.preventDefault();
    if (!editModal) return;
    setEditModal({ ...editModal, submitting: true, error: "" });
    try {
      const updated = await api.updateConnection(editModal.connection.id, {
        ...connectionInputFromForm({ ...editModal.form, type: editModal.connection.type }),
        port: Number(editModal.form.port),
      });
      onUpdate(updated);
      setEditModal(null);
    } catch (error) {
      setEditModal({
        ...editModal,
        submitting: false,
        error: error instanceof Error ? error.message : "更新连接失败",
      });
    }
  }

  async function submitCreateDatabase(event: React.FormEvent) {
    event.preventDefault();
    if (!createDatabaseModal) return;
    const name = createDatabaseModal.name.trim();
    setCreateDatabaseModal({ ...createDatabaseModal, name, submitting: true, error: "" });
    try {
      const databases = await api.createDatabase(createDatabaseModal.connection.id, name);
      setCreateDatabaseModal(null);
      if (inspectedId === createDatabaseModal.connection.id) {
        setInspectorDbs({ connectionId: createDatabaseModal.connection.id, databases, loading: false, error: "" });
      } else {
        setDatabaseModal({ connection: createDatabaseModal.connection, databases, loading: false, error: "" });
      }
    } catch (error) {
      setCreateDatabaseModal({
        ...createDatabaseModal,
        name,
        submitting: false,
        error: error instanceof Error ? error.message : "数据库创建失败",
      });
    }
  }

  async function confirmDeleteConnection() {
    if (!deleteModal) return;
    setDeleteModal({ ...deleteModal, submitting: true, error: "" });
    try {
      await api.deleteConnection(deleteModal.connection.id);
      if (inspectedId === deleteModal.connection.id) setInspectedId(null);
      onDelete(deleteModal.connection.id);
      setDeleteModal(null);
    } catch (error) {
      setDeleteModal({
        ...deleteModal,
        submitting: false,
        error: error instanceof Error ? error.message : "删除连接失败",
      });
    }
  }

  async function submitSeedData(event: React.FormEvent) {
    event.preventDefault();
    if (!seedDataModal) return;
    if (seedDataModal.mode === "replace" && !seedDataConfirmOpen) {
      setSeedDataConfirmOpen(true);
      return;
    }
    await executeSeedData();
  }

  async function executeSeedData() {
    if (!seedDataModal) return;
    setSeedDataModal({ ...seedDataModal, submitting: true, error: "", result: null });
    try {
      const result = await api.seedData(
        seedDataModal.connection.id,
        seedDataModal.database,
        Number(seedDataModal.rows),
        seedDataModal.mode,
        seedDataModal.template,
      );
      setSeedDataModal({ ...seedDataModal, submitting: false, error: "", result });
      setSeedDataConfirmOpen(false);
    } catch (error) {
      setSeedDataModal({
        ...seedDataModal,
        submitting: false,
        error: error instanceof Error ? error.message : "写入假数据失败",
        result: null,
      });
    }
  }

  async function copyConnectionField(key: string, value: string) {
    setActionError("");
    setManualCopyValue("");
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(value);
      } else {
        const copiedOk = fallbackCopyText(value);
        if (!copiedOk) {
          setManualCopyValue(value);
          setActionError(`当前浏览器不支持自动复制，请手动复制：${value}`);
          return;
        }
      }
      setCopied(key);
      window.setTimeout(() => setCopied((prev) => (prev === key ? null : prev)), 1400);
    } catch {
      const copiedOk = fallbackCopyText(value);
      if (!copiedOk) {
        setManualCopyValue(value);
        setActionError(`当前浏览器不支持自动复制，请手动复制：${value}`);
        return;
      }
      setCopied(key);
      window.setTimeout(() => setCopied((prev) => (prev === key ? null : prev)), 1400);
    }
  }

  async function exportConnections() {
    try {
      setActionError("");
      const { blob, fileName } = await api.exportExcel("connections");
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = fileName;
      link.click();
      URL.revokeObjectURL(url);
    } catch (error) {
      setActionError(error instanceof Error ? error.message : "导出数据连接失败");
    }
  }

  async function toggleFavorite(connection: DatabaseConnection) {
    setFavoritingId(connection.id);
    setActionError("");
    try {
      await onToggleFavorite(connection);
    } catch (error) {
      setActionError(error instanceof Error ? error.message : "更新收藏失败");
    } finally {
      setFavoritingId(null);
    }
  }

  return (
    <div className="page-stack">
      <div className="h-row">
        <div>
          <h1 className="page-h">数据连接</h1>
          <div className="sec-sub">统一管理源端与目标端 · 支持 PostgreSQL / MySQL / MongoDB / Neo4j / etcd</div>
        </div>
        <span className="sp" />
        <Button variant="secondary" onClick={exportConnections}>
          <Download size={15} />
          导出连接
        </Button>
        <Button variant="secondary" onClick={() => setShowBatchImport(true)}>
          <FileSpreadsheet size={15} />
          批量导入
        </Button>
        <Button variant="primary" onClick={openNewConnection}>
          <Plus size={15} />
          新建连接
        </Button>
      </div>

      <div className="h-row">
        <span className="sfield">
          <span className="sfield-ic">
            <Search size={14} />
          </span>
          <input
            className="inp"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="搜索名称、项目、环境、标签或主机…"
            style={{ width: 250 }}
          />
        </span>

        <span className="chiprow">
          {["全部", "PostgreSQL", "MySQL", "MongoDB", "Neo4j", "etcd"].map((type) => (
            <button
              key={type}
              type="button"
              className={`fchip ${typeFilter === type ? "on" : ""}`}
              onClick={() => setTypeFilter(type)}
            >
              {type}
            </button>
          ))}
        </span>

        <SelectField ariaLabel="连接环境筛选" value={envFilter} onChange={setEnvFilter} inline width={120}>
          <option value="全部">全部环境</option>
          {CONNECTION_ENVIRONMENT_OPTIONS.map((opt) => (
            <option key={opt.value} value={opt.value}>
              {connectionEnvironmentLabel(opt.value)}
            </option>
          ))}
          <option value="__none__">未设置</option>
        </SelectField>

        <SelectField ariaLabel="连接项目筛选" value={projectFilter} onChange={setProjectFilter} inline width={132}>
          <option value="全部">全部项目</option>
          {projectSuggestions.map((proj) => (
            <option key={proj} value={proj}>
              {proj}
            </option>
          ))}
          <option value="__none__">未分配项目</option>
        </SelectField>

        <SelectField ariaLabel="连接状态筛选" value={statusFilter} onChange={setStatusFilter} inline width={124}>
          <option value="全部">全部状态</option>
          <option value="已连接">已连接</option>
          <option value="未验证">未验证</option>
          <option value="连接失败">连接失败</option>
        </SelectField>

        <button type="button" className={`fchip ${favOnly ? "on" : ""}`} onClick={() => setFavOnly((v) => !v)}>
          <Star size={13} fill={favOnly ? "currentColor" : "none"} /> 仅收藏
        </button>

        <span className="sp" />
        <span style={{ fontSize: 12.5, color: "var(--muted)" }}>共 {filtered.length} 个连接</span>
        <DensitySwitch value={prefs.conn} onChange={(v) => updatePref("conn", v as "comfortable" | "compact")} />
      </div>

      {actionError ? <div className="error-banner">{actionError}</div> : null}
      {manualCopyValue ? (
        <div className="error-banner" style={{ alignItems: "center" }}>
          <strong>手动复制：</strong>
          <input
            readOnly
            className="inp"
            style={{ height: 30, maxWidth: 260 }}
            value={manualCopyValue}
            onFocus={(event) => event.currentTarget.select()}
            aria-label="手动复制主机地址"
          />
        </div>
      ) : null}

      <div style={{ display: "flex", gap: 18, alignItems: "flex-start" }}>
        <div className={`node-list ${prefs.conn}`} style={{ flex: 1, minWidth: 0 }}>
          {filtered.length ? (
            filtered.map((connection) => {
              const scheduledBackups = scheduledBackupsForConnection(connection, scheduledBackupsByConnection);
              const versionLabel = compactVersionLabel(connection.version);
              const isSelected = inspectedId === connection.id;
              const menuItems: ActionMenuItem[] = [
                {
                  key: "test",
                  label: testing === connection.id ? "测试中..." : "刷新状态",
                  icon: <RefreshCw size={14} className={testing === connection.id ? "spin" : undefined} />,
                  disabled: testing === connection.id,
                  onClick: () => void testConnection(connection.id),
                },
                {
                  key: "edit",
                  label: "编辑连接信息",
                  icon: <Edit3 size={14} />,
                  onClick: () => openEditConnection(connection),
                },
                {
                  key: "dup",
                  label: "复制连接",
                  icon: <Copy size={14} />,
                  onClick: () => openCopyConnection(connection),
                },
              ];
              if (connection.type !== "etcd") {
                menuItems.push({
                  key: "dbs",
                  label: "查看数据库",
                  icon: <Database size={14} />,
                  onClick: () => void showDatabases(connection),
                });
              }
              if (supportsDirectDatabaseCreate(connection.type)) {
                menuItems.push({
                  key: "newdb",
                  label: "创建数据库",
                  icon: <Plus size={14} />,
                  onClick: () => openCreateDatabase(connection),
                });
              }
              if (supportsSeedData(connection.type)) {
                menuItems.push({
                  key: "seed",
                  label: "写入假数据",
                  icon: <WandSparkles size={14} />,
                  onClick: () => void openSeedData(connection),
                });
              }
              if (supportsMigration(connection.type)) {
                menuItems.push({
                  key: "mig",
                  label: "发起迁移",
                  icon: <Play size={14} />,
                  onClick: () => onCreateMigration(connection.id),
                });
              }
              menuItems.push({
                key: "del",
                label: "删除连接",
                icon: <Trash2 size={14} />,
                danger: true,
                dividerBefore: true,
                onClick: () => setDeleteModal({ connection, submitting: false, error: "" }),
              });

              return (
                <div
                  key={connection.id}
                  className={`node-row ${isSelected ? "sel" : ""}`}
                  onClick={() => setInspectedId(isSelected ? null : connection.id)}
                >
                  <span data-tooltip-content={connection.type} style={{ flex: "none" }}>
                    <DatabaseLogo type={connection.type} name={connection.name} />
                  </span>
                  <div className="node-main">
                    <div className="node-name">
                      <span>{connection.name}</span>
                      {connection.environment ? (
                        <Badge tone={connectionEnvironmentTone(connection.environment)}>
                          {connectionEnvironmentLabel(connection.environment)}
                        </Badge>
                      ) : null}
                      {connection.project ? <span className="tag">项目: {connection.project}</span> : null}
                      <Badge tone={connectionStatusTone(connection.status)}>
                        <span className="pd" />
                        {connectionStatusLabel(connection.status)}
                      </Badge>
                      {connection.version ? (
                        <span className="tag mono-t" data-tooltip-content={`v${connection.version}`}>
                          {versionLabel}
                        </span>
                      ) : null}
                      {connection.type === "etcd" ? <span className="tag">整集群快照</span> : null}
                      {connection.type === "MongoDB" && connection.mongoBackupReadiness ? (
                        <span className="tag" data-tooltip-content={connection.mongoBackupReadiness.message}>
                          <ShieldCheck size={12} />
                          {mongoBackupReadinessLabel(connection)}
                        </span>
                      ) : null}
                      {scheduledBackups.length > 0 ? (
                        <span
                          className="tag"
                          style={{
                            borderColor: "var(--indigo-soft)",
                            background: "var(--indigo-faint)",
                            color: "var(--indigo-ink)",
                          }}
                          data-tooltip-content={scheduledBackups
                            .map((job) => `${job.name} · ${job.schedulePaused ? "已暂停 · " : ""}${scheduleLabel(job)}`)
                            .join("\n")}
                        >
                          <CalendarClock size={12} />
                          {scheduledBackups.length} 个定时任务
                        </span>
                      ) : null}
                    </div>

                    <div className="node-meta">
                      <button
                        type="button"
                        className="copy-host"
                        onClick={(event) => {
                          event.stopPropagation();
                          void copyConnectionField(`${connection.id}:host`, `${connection.host}:${connection.port}`);
                        }}
                        aria-label={`复制 ${connection.host}:${connection.port}`}
                        data-tooltip-content="点击复制主机地址"
                      >
                        <span>
                          {connection.host}:{connection.port}
                        </span>
                        {copied === `${connection.id}:host` ? <Check size={12} style={{ color: "var(--ok)" }} /> : <Copy size={12} />}
                      </button>
                      {connection.username ? (
                        <button
                          type="button"
                          className="copy-host"
                          onClick={(event) => {
                            event.stopPropagation();
                            void copyConnectionField(`${connection.id}:user`, connection.username);
                          }}
                          aria-label={`复制用户名 ${connection.username}`}
                          data-tooltip-content="点击复制用户名"
                        >
                          <span>{connection.username}</span>
                          {copied === `${connection.id}:user` ? (
                            <Check size={12} style={{ color: "var(--ok)" }} />
                          ) : (
                            <Copy size={12} />
                          )}
                        </button>
                      ) : (
                        <span>未启用鉴权</span>
                      )}
                      {connection.status === "connected" ? (
                        <LatencyIndicator latency={connection.latency} />
                      ) : (
                        <button
                          type="button"
                          style={{
                            background: "none",
                            border: "none",
                            padding: 0,
                            minHeight: "auto",
                            fontSize: 12.5,
                            fontWeight: 550,
                            color: connection.status === "warning" ? "var(--danger)" : "var(--warn)",
                          }}
                          onClick={(event) => {
                            event.stopPropagation();
                            void testConnection(connection.id);
                          }}
                        >
                          {testing === connection.id ? "正在测试..." : connectionHealthLabel(connection)}
                        </button>
                      )}
                      <span style={{ display: "inline-flex", alignItems: "center", gap: 5 }}>
                        <RegionFlag region={connection.region} />
                        <span>{connection.region || "未知"}</span>
                      </span>
                      {connection.note ? <span>{connection.note}</span> : null}
                    </div>

                    {connection.type === "etcd" || (connection.tags && connection.tags.length > 0) ? (
                      <div className="dbchips">
                        {connection.type === "etcd" ? (
                          <span className="dbchip">{connection.endpoints?.length ?? 0} 个已发现端点</span>
                        ) : null}
                        {(connection.tags ?? []).slice(0, 3).map((tag) => (
                          <span className="dbchip" key={tag}>
                            # {tag}
                          </span>
                        ))}
                        {(connection.tags?.length ?? 0) > 3 ? (
                          <span className="dbchip" data-tooltip-content={(connection.tags ?? []).slice(3).join("、")}>
                            +{(connection.tags?.length ?? 0) - 3}
                          </span>
                        ) : null}
                      </div>
                    ) : null}
                  </div>

                  <div className="node-side" onClick={(event) => event.stopPropagation()}>
                    <div style={{ display: "flex", alignItems: "center", gap: 2 }}>
                      <button
                        type="button"
                        className={`star-btn ${connection.favorite ? "on" : ""}`}
                        aria-label={connection.favorite ? `取消收藏 ${connection.name}` : `收藏 ${connection.name}`}
                        data-tooltip-mode="always"
                        data-tooltip-content={connection.favorite ? "取消收藏" : "收藏并置顶"}
                        disabled={favoritingId === connection.id}
                        onClick={() => void toggleFavorite(connection)}
                      >
                        <Star size={16} fill={connection.favorite ? "currentColor" : "none"} />
                      </button>
                      <ActionMenu items={menuItems} ariaLabel={`${connection.name} 更多操作`} />
                    </div>
                    <span className="node-date" style={{ fontSize: 11.5, color: "var(--faint)" }}>
                      {new Date(connection.createdAt).toLocaleDateString("zh-CN")}
                    </span>
                  </div>
                </div>
              );
            })
          ) : (
            <div className="empty">
              <div className="e-t">{connections.length ? "没有匹配的数据库连接" : "暂无数据库连接"}</div>
              <div className="e-s">
                {connections.length ? "换个关键词或清除筛选条件试试。" : "先添加 PostgreSQL、MySQL、MongoDB、Neo4j 或 etcd 实例。"}
              </div>
            </div>
          )}
        </div>

        {inspectedConnection ? (
          <>
            <div className="insp-scrim" onClick={() => setInspectedId(null)} />
            <aside
              className="inspector card pad"
              style={{
                borderRadius: "var(--r-card)",
                border: "1px solid var(--hair)",
                position: "sticky",
                top: 0,
              }}
              aria-label="连接详情检查器"
            >
              <div className="insp-head">
                <DatabaseLogo type={inspectedConnection.type} name={inspectedConnection.name} />
                <div style={{ minWidth: 0 }}>
                  <h3>{inspectedConnection.name}</h3>
                  <div className="sub">
                    {inspectedConnection.type} {inspectedConnection.version ? `v${inspectedConnection.version}` : ""} ·{" "}
                    {inspectedConnection.region || "未知"}
                  </div>
                </div>
                <button
                  type="button"
                  className="icon-btn ghost insp-close"
                  aria-label="关闭检查器"
                  onClick={() => setInspectedId(null)}
                >
                  <X size={16} />
                </button>
              </div>

              <div className="chiprow" style={{ marginBottom: 10 }}>
                {inspectedConnection.environment ? (
                  <Badge tone={connectionEnvironmentTone(inspectedConnection.environment)}>
                    {connectionEnvironmentLabel(inspectedConnection.environment)}
                  </Badge>
                ) : null}
                {inspectedConnection.project ? <span className="tag">项目: {inspectedConnection.project}</span> : null}
                <Badge tone={connectionStatusTone(inspectedConnection.status)}>
                  <span className="pd" />
                  {connectionStatusLabel(inspectedConnection.status)}
                </Badge>
                {inspectedConnection.status === "connected" ? (
                  <span className="tag lat-tag">
                    <LatencyIndicator latency={inspectedConnection.latency} />
                  </span>
                ) : null}
              </div>

              <div className="kv">
                <span className="k">主机地址</span>
                <span className="v mono">
                  {inspectedConnection.host}:{inspectedConnection.port}
                </span>
              </div>
              <div className="kv">
                <span className="k">连接用户</span>
                <span className="v">{inspectedConnection.username || "未启用鉴权"}</span>
              </div>
              <div className="kv">
                <span className="k">安全模式</span>
                <span className="v">{inspectedConnection.sslMode}</span>
              </div>
              <div className="kv">
                <span className="k">节点地区</span>
                <span className="v" style={{ display: "inline-flex", alignItems: "center", gap: 6, justifyContent: "flex-end" }}>
                  <RegionFlag region={inspectedConnection.region} />
                  {inspectedConnection.region || "未知"}
                </span>
              </div>
              {inspectedConnection.tags && inspectedConnection.tags.length > 0 ? (
                <div className="kv">
                  <span className="k">标签</span>
                  <span className="v" style={{ display: "inline-flex", flexWrap: "wrap", gap: 4, justifyContent: "flex-end" }}>
                    {inspectedConnection.tags.map((tag) => (
                      <span className="dbchip" key={tag}>
                        # {tag}
                      </span>
                    ))}
                  </span>
                </div>
              ) : null}
              {inspectedConnection.note ? (
                <div className="kv">
                  <span className="k">备注</span>
                  <span className="v">{inspectedConnection.note}</span>
                </div>
              ) : null}

              {inspectedConnection.endpoints && inspectedConnection.endpoints.length > 0 ? (
                <div className="insp-sec">
                  <h5>{inspectedConnection.type === "etcd" ? "已发现集群端点" : "副本集种子节点"}</h5>
                  {inspectedConnection.endpoints.map((ep) => (
                    <div className="kv" key={ep}>
                      <span className="k mono" style={{ color: "var(--ink-2)" }}>
                        {ep}
                      </span>
                    </div>
                  ))}
                </div>
              ) : null}

              {inspectedConnection.type === "MongoDB" && inspectedConnection.mongoBackupReadiness ? (
                <div className="insp-sec">
                  <h5>增量备份预检</h5>
                  <div className="kv">
                    <span className="k">状态</span>
                    <span className="v">{mongoBackupReadinessLabel(inspectedConnection)}</span>
                  </div>
                  <div style={{ fontSize: 12, color: "var(--muted)", marginTop: 4 }}>
                    {inspectedConnection.mongoBackupReadiness.message}
                  </div>
                </div>
              ) : null}

              {inspectedConnection.type !== "etcd" ? (
                <div className="insp-sec">
                  <h5>数据库列表</h5>
                  {inspectorDbs?.loading ? (
                    <div style={{ fontSize: 12.5, color: "var(--muted)" }}>正在加载数据库...</div>
                  ) : inspectorDbs?.error ? (
                    <div className="error-banner">{inspectorDbs.error}</div>
                  ) : inspectorDbs?.databases.length ? (
                    <div>
                      {inspectorDbs.databases.map((db) => (
                        <div className="kv" key={db.name}>
                          <span className="k" style={{ color: "var(--ink)", fontWeight: 550 }}>
                            <Database size={12} /> {db.name}
                          </span>
                          <span className="v num">{formatDatabaseMetric(inspectedConnection.type, db)}</span>
                        </div>
                      ))}
                    </div>
                  ) : (
                    <div style={{ fontSize: 12.5, color: "var(--muted)" }}>暂无可用数据库</div>
                  )}

                  <div style={{ display: "flex", gap: 8, marginTop: 10, flexWrap: "wrap" }}>
                    {supportsDirectDatabaseCreate(inspectedConnection.type) ? (
                      <Button variant="secondary" className="sm" onClick={() => openCreateDatabase(inspectedConnection)}>
                        <Plus size={13} /> 创建数据库
                      </Button>
                    ) : null}
                    {supportsSeedData(inspectedConnection.type) ? (
                      <Button variant="secondary" className="sm" onClick={() => void openSeedData(inspectedConnection)}>
                        <WandSparkles size={13} /> 写入假数据
                      </Button>
                    ) : null}
                  </div>
                </div>
              ) : null}

              <div className="insp-sec">
                <h5>操作</h5>
                <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
                  <Button
                    variant="secondary"
                    className="sm"
                    disabled={testing === inspectedConnection.id}
                    onClick={() => void testConnection(inspectedConnection.id)}
                  >
                    <RefreshCw size={13} className={testing === inspectedConnection.id ? "spin" : undefined} />
                    刷新状态
                  </Button>
                  {supportsMigration(inspectedConnection.type) ? (
                    <Button variant="primary" className="sm" onClick={() => onCreateMigration(inspectedConnection.id)}>
                      <Play size={13} /> 发起迁移
                    </Button>
                  ) : null}
                  <Button variant="secondary" className="sm" onClick={() => openEditConnection(inspectedConnection)}>
                    <Edit3 size={13} /> 编辑
                  </Button>
                  <Button variant="secondary" className="sm" onClick={() => openCopyConnection(inspectedConnection)}>
                    <Copy size={13} /> 复制
                  </Button>
                  <Button
                    variant="danger"
                    className="sm"
                    onClick={() => setDeleteModal({ connection: inspectedConnection, submitting: false, error: "" })}
                  >
                    <Trash2 size={13} /> 删除
                  </Button>
                </div>
              </div>
            </aside>
          </>
        ) : null}
      </div>

      {showBatchImport ? <BatchImportModal onClose={() => setShowBatchImport(false)} onImported={onBatchImported} /> : null}

      {databaseModal
        ? createPortal(
            <div className="modal-backdrop" onMouseDown={() => setDatabaseModal(null)}>
              <div className="modal" role="dialog" aria-modal="true" onMouseDown={(event) => event.stopPropagation()}>
                <div className="modal-h">
                  <div style={{ flex: 1 }}>
                    <h3>{databaseModal.connection.name} 的数据库</h3>
                    <div className="ms">
                      {databaseModal.connection.host}:{databaseModal.connection.port}
                    </div>
                  </div>
                  {supportsDirectDatabaseCreate(databaseModal.connection.type) ? (
                    <Button variant="secondary" className="sm" onClick={() => openCreateDatabase(databaseModal.connection)}>
                      <Plus size={14} /> 创建数据库
                    </Button>
                  ) : null}
                  <button type="button" className="icon-btn ghost" onClick={() => setDatabaseModal(null)}>
                    <X size={17} />
                  </button>
                </div>
                <div className="modal-b">
                  {databaseModal.loading ? (
                    <div style={{ color: "var(--muted)" }}>正在加载数据库...</div>
                  ) : databaseModal.error ? (
                    <div className="error-banner">{databaseModal.error}</div>
                  ) : databaseModal.databases.length ? (
                    <div className="ledger">
                      {databaseModal.databases.map((db) => (
                        <div className="lrow" style={{ cursor: "default" }} key={db.name}>
                          <Database size={15} style={{ color: "var(--muted)" }} />
                          <div className="lr-main">
                            <div className="lr-t">{db.name}</div>
                          </div>
                          <span className="num" style={{ color: "var(--muted)", fontSize: 13 }}>
                            {formatDatabaseMetric(databaseModal.connection.type, db)}
                          </span>
                        </div>
                      ))}
                    </div>
                  ) : (
                    <div className="empty">
                      <div className="e-t">暂无可用数据库</div>
                    </div>
                  )}
                </div>
              </div>
            </div>,
            document.body,
          )
        : null}

      {createDatabaseModal
        ? createPortal(
            <div className="modal-backdrop" onMouseDown={() => setCreateDatabaseModal(null)}>
              <form
                className="modal"
                role="dialog"
                aria-modal="true"
                onSubmit={submitCreateDatabase}
                onMouseDown={(event) => event.stopPropagation()}
              >
                <div className="modal-h">
                  <div style={{ flex: 1 }}>
                    <h3>创建数据库</h3>
                    <div className="ms">
                      {createDatabaseModal.connection.name} · {createDatabaseModal.connection.host}:{createDatabaseModal.connection.port}
                    </div>
                  </div>
                  <button type="button" className="icon-btn ghost" onClick={() => setCreateDatabaseModal(null)}>
                    <X size={17} />
                  </button>
                </div>
                <div className="modal-b">
                  <label className="field">
                    <span>{requiredLabel("数据库名称")}</span>
                    <input
                      required
                      autoFocus
                      value={createDatabaseModal.name}
                      onChange={(event) => setCreateDatabaseModal({ ...createDatabaseModal, name: event.target.value, error: "" })}
                      placeholder="例如 app_prod"
                    />
                  </label>
                  <p className="field-hint">
                    {createDatabaseModal.connection.type === "MongoDB"
                      ? 'MongoDB 会创建一个初始化集合，让数据库立即可见。数据库名不要包含空格以及 / \\ . " $ 等字符。'
                      : createDatabaseModal.connection.type === "Neo4j"
                        ? "Neo4j Community 通常只支持默认库；如果实例不支持多数据库，创建时会返回权限或版本限制。"
                        : "最多 63 个字符，使用字母或下划线开头，只包含字母、数字、下划线或短横线。"}
                  </p>
                  {createDatabaseModal.error ? <div className="error-banner">{createDatabaseModal.error}</div> : null}
                </div>
                <div className="modal-f">
                  <Button type="button" variant="secondary" onClick={() => setCreateDatabaseModal(null)}>
                    取消
                  </Button>
                  <Button type="submit" disabled={createDatabaseModal.submitting}>
                    {createDatabaseModal.submitting ? "正在创建..." : "创建数据库"}
                  </Button>
                </div>
              </form>
            </div>,
            document.body,
          )
        : null}

      {seedDataModal
        ? createPortal(
            <div className="modal-backdrop" onMouseDown={() => setSeedDataModal(null)}>
              <form
                className="modal"
                role="dialog"
                aria-modal="true"
                onSubmit={submitSeedData}
                onMouseDown={(event) => event.stopPropagation()}
              >
                <div className="modal-h">
                  <div style={{ flex: 1 }}>
                    <h3>写入假数据</h3>
                    <div className="ms">
                      {seedDataModal.connection.name} · {seedDataModal.connection.host}:{seedDataModal.connection.port}
                    </div>
                  </div>
                  <button type="button" className="icon-btn ghost" onClick={() => setSeedDataModal(null)}>
                    <X size={17} />
                  </button>
                </div>
                <div className="modal-b">
                  <p className="field-hint" style={{ marginBottom: 14 }}>
                    {seedDataHint(seedDataModal.connection.type)}
                  </p>
                  {seedDataModal.loading ? (
                    <div style={{ color: "var(--muted)" }}>正在加载数据库...</div>
                  ) : (
                    <>
                      <SelectField
                        label={requiredLabel("目标数据库")}
                        value={seedDataModal.database}
                        onChange={(database) => setSeedDataModal({ ...seedDataModal, database, error: "", result: null })}
                      >
                        {seedDataModal.databases.map((item) => (
                          <option key={item.name} value={item.name}>
                            {item.name}
                          </option>
                        ))}
                      </SelectField>
                      <div className="grid2">
                        <SelectField
                          label="写入模板"
                          value={seedDataModal.template}
                          onChange={(template) =>
                            setSeedDataModal({
                              ...seedDataModal,
                              template: template as "users" | "orders" | "products" | "payload",
                              error: "",
                              result: null,
                            })
                          }
                        >
                          <option value="users">用户数据</option>
                          <option value="orders">订单数据</option>
                          <option value="products">商品数据</option>
                          <option value="payload">高熵负载测试</option>
                        </SelectField>
                        <SelectField
                          label="写入方式"
                          value={seedDataModal.mode}
                          onChange={(mode) =>
                            setSeedDataModal({ ...seedDataModal, mode: mode as "append" | "replace", error: "", result: null })
                          }
                        >
                          <option value="append">追加写入</option>
                          <option value="replace">覆盖写入</option>
                        </SelectField>
                      </div>
                      <label className="field">
                        <span>写入条数</span>
                        <input
                          type="number"
                          min={1}
                          max={50000}
                          value={seedDataModal.rows}
                          onChange={(event) => setSeedDataModal({ ...seedDataModal, rows: event.target.value, error: "", result: null })}
                        />
                      </label>
                    </>
                  )}
                  {seedDataModal.error ? <div className="error-banner">{seedDataModal.error}</div> : null}
                  {seedDataModal.result ? (
                    <div className="info-callout">
                      已向 {seedDataModal.result.database} 的 {seedDataModal.result.table}
                      {seedDataTargetSuffix(seedDataModal.connection.type)}{" "}
                      {seedDataModal.result.mode === "replace" ? "覆盖写入" : "追加写入"} {seedDataModal.result.rows} 条
                      {seedDataModal.result.template === "users"
                        ? "用户"
                        : seedDataModal.result.template === "orders"
                          ? "订单"
                          : seedDataModal.result.template === "products"
                            ? "商品"
                            : "高熵负载"}
                      数据。
                    </div>
                  ) : null}
                </div>
                <div className="modal-f">
                  <Button type="button" variant="secondary" onClick={() => setSeedDataModal(null)}>
                    关闭
                  </Button>
                  <Button type="submit" disabled={seedDataModal.loading || seedDataModal.submitting || !seedDataModal.database}>
                    {seedDataModal.submitting ? "正在写入..." : "开始写入"}
                  </Button>
                </div>
              </form>
            </div>,
            document.body,
          )
        : null}

      {deleteModal ? (
        <ConfirmDialog
          title="删除连接"
          description={`${deleteModal.connection.name} · ${deleteModal.connection.host}:${deleteModal.connection.port}`}
          tone="danger"
          confirmText={deleteModal.submitting ? "正在删除..." : "删除连接"}
          submitting={deleteModal.submitting}
          onCancel={() => setDeleteModal(null)}
          onConfirm={confirmDeleteConnection}
          details={[
            "删除后将清除这条连接的已保存凭证，且不能再基于它查看数据库或发起迁移。",
            "历史迁移记录会保留，但不会再关联这条连接继续操作。",
            deleteModal.error ? <span style={{ color: "var(--danger)" }}>{deleteModal.error}</span> : null,
          ].filter(Boolean)}
        />
      ) : null}

      {duplicateModal
        ? createPortal(
            <div className="modal-backdrop" onMouseDown={() => setDuplicateModal(null)}>
              <form
                className="modal wide"
                role="dialog"
                aria-modal="true"
                onSubmit={submitDuplicateConnection}
                onMouseDown={(event) => event.stopPropagation()}
              >
                <div className="modal-h">
                  <div style={{ flex: 1 }}>
                    <h3>复制连接</h3>
                    <div className="ms">从 {duplicateModal.connection.name} 复制配置，默认沿用已保存的密码和证书。</div>
                  </div>
                  <button type="button" className="icon-btn ghost" onClick={() => setDuplicateModal(null)}>
                    <X size={17} />
                  </button>
                </div>
                <div className="modal-b">
                  <div className="grid2">
                    <label className="field">
                      <span>{requiredLabel("连接名称")}</span>
                      <input
                        required
                        value={duplicateModal.form.name}
                        onChange={(e) => updateDuplicateForm({ name: e.target.value })}
                      />
                    </label>
                    <SelectField label="数据库类型" value={duplicateModal.connection.type} disabled onChange={() => undefined}>
                      <option value={duplicateModal.connection.type}>{duplicateModal.connection.type}</option>
                    </SelectField>
                  </div>
                  <div className="field-row">
                    <label className="field">
                      <span>{requiredLabel("主机地址")}</span>
                      <input
                        required
                        value={duplicateModal.form.host}
                        onChange={(e) => updateDuplicateForm({ host: e.target.value })}
                      />
                    </label>
                    <label className="field">
                      <span>{requiredLabel("端口")}</span>
                      <input
                        required
                        type="number"
                        value={duplicateModal.form.port}
                        onChange={(e) => updateDuplicateForm({ port: e.target.value })}
                      />
                    </label>
                  </div>
                  <div className="grid2">
                    <label className="field">
                      <span>{duplicateModal.connection.type === "etcd" ? "用户名（可选）" : requiredLabel("用户名")}</span>
                      <input
                        required={duplicateModal.connection.type !== "etcd"}
                        value={duplicateModal.form.username}
                        onChange={(e) => updateDuplicateForm({ username: e.target.value })}
                      />
                    </label>
                    <label className="field">
                      <span>密码</span>
                      <input
                        type="password"
                        value={duplicateModal.form.password}
                        onChange={(e) => updateDuplicateForm({ password: e.target.value })}
                        placeholder="留空则沿用原连接密码"
                      />
                    </label>
                  </div>
                  <EtcdTLSModeGuide enabled={duplicateModal.connection.type === "etcd"} />
                  <SelectField
                    label={requiredLabel(connectionSecurityConfig(duplicateModal.connection.type).label)}
                    value={duplicateModal.form.sslMode}
                    onChange={(sslMode) => updateDuplicateForm({ sslMode })}
                  >
                    {connectionSecurityConfig(duplicateModal.connection.type).options.map((option) => (
                      <option key={option.value} value={option.value}>
                        {option.label}
                      </option>
                    ))}
                  </SelectField>
                  <p className="field-hint">{connectionSecurityConfig(duplicateModal.connection.type).hint}</p>
                  {duplicateModal.form.sslMode === "verify-full" ? (
                    <>
                      <EtcdTLSHint enabled={duplicateModal.connection.type === "etcd"} />
                      <TLSCertificateFields
                        value={duplicateModal.form.tlsConfig}
                        onChange={(tlsConfig) => updateDuplicateForm({ tlsConfig })}
                        preserveExisting
                      />
                    </>
                  ) : null}
                  <ConnectionMetadataFields
                    project={duplicateModal.form.project}
                    environment={duplicateModal.form.environment}
                    tags={duplicateModal.form.tags}
                    projectSuggestions={projectSuggestions}
                    onProjectChange={(project) => updateDuplicateForm({ project })}
                    onEnvironmentChange={(environment) => updateDuplicateForm({ environment })}
                    onTagsChange={(tags) => updateDuplicateForm({ tags })}
                  />
                  <label className="field">
                    <span>备注</span>
                    <input
                      value={duplicateModal.form.note}
                      onChange={(e) => updateDuplicateForm({ note: e.target.value })}
                      placeholder="可选，记录用途或负责人"
                    />
                  </label>
                  {duplicateModal.error ? <div className="error-banner">{duplicateModal.error}</div> : null}
                </div>
                <div className="modal-f">
                  <Button type="button" variant="secondary" onClick={() => setDuplicateModal(null)}>
                    取消
                  </Button>
                  <Button type="submit" disabled={duplicateModal.submitting}>
                    {duplicateModal.submitting ? "正在复制..." : "复制并测试"}
                  </Button>
                </div>
              </form>
            </div>,
            document.body,
          )
        : null}

      {editModal
        ? createPortal(
            <div className="modal-backdrop" onMouseDown={() => setEditModal(null)}>
              <form
                className="modal wide"
                role="dialog"
                aria-modal="true"
                onSubmit={submitEditConnection}
                onMouseDown={(event) => event.stopPropagation()}
              >
                <div className="modal-h">
                  <div style={{ flex: 1 }}>
                    <h3>编辑连接信息</h3>
                    <div className="ms">{editModal.connection.name} · 保存后会重新测试连接。</div>
                  </div>
                  <button type="button" className="icon-btn ghost" onClick={() => setEditModal(null)}>
                    <X size={17} />
                  </button>
                </div>
                <div className="modal-b">
                  <div className="grid2">
                    <label className="field">
                      <span>{requiredLabel("连接名称")}</span>
                      <input
                        required
                        value={editModal.form.name}
                        onChange={(e) => setEditModal({ ...editModal, form: { ...editModal.form, name: e.target.value }, error: "" })}
                      />
                    </label>
                    <SelectField label="数据库类型" value={editModal.connection.type} disabled onChange={() => undefined}>
                      <option value={editModal.connection.type}>{editModal.connection.type}</option>
                    </SelectField>
                  </div>
                  <div className="field-row">
                    <label className="field">
                      <span>{requiredLabel("主机地址")}</span>
                      <input
                        required
                        value={editModal.form.host}
                        onChange={(e) => setEditModal({ ...editModal, form: { ...editModal.form, host: e.target.value }, error: "" })}
                      />
                    </label>
                    <label className="field">
                      <span>{requiredLabel("端口")}</span>
                      <input
                        required
                        type="number"
                        value={editModal.form.port}
                        onChange={(e) => setEditModal({ ...editModal, form: { ...editModal.form, port: e.target.value }, error: "" })}
                      />
                    </label>
                  </div>
                  <div className="grid2">
                    <label className="field">
                      <span>{editModal.connection.type === "etcd" ? "用户名（可选）" : requiredLabel("用户名")}</span>
                      <input
                        required={editModal.connection.type !== "etcd"}
                        value={editModal.form.username}
                        onChange={(e) => setEditModal({ ...editModal, form: { ...editModal.form, username: e.target.value }, error: "" })}
                      />
                    </label>
                    <label className="field">
                      <span>密码</span>
                      <input
                        type="password"
                        value={editModal.form.password}
                        onChange={(e) => setEditModal({ ...editModal, form: { ...editModal.form, password: e.target.value }, error: "" })}
                        placeholder="留空则保持当前密码不变"
                      />
                    </label>
                  </div>
                  {editModal.connection.type === "MongoDB" ? (
                    <MongoConnectionModeFields
                      mode={editModal.form.mongoMode}
                      seeds={editModal.form.mongoSeeds}
                      onModeChange={(mongoMode) => setEditModal({ ...editModal, form: { ...editModal.form, mongoMode }, error: "" })}
                      onSeedsChange={(mongoSeeds) => setEditModal({ ...editModal, form: { ...editModal.form, mongoSeeds }, error: "" })}
                    />
                  ) : null}
                  <EtcdTLSModeGuide enabled={editModal.connection.type === "etcd"} />
                  <SelectField
                    label={requiredLabel(connectionSecurityConfig(editModal.connection.type).label)}
                    value={editModal.form.sslMode}
                    onChange={(sslMode) => setEditModal({ ...editModal, form: { ...editModal.form, sslMode }, error: "" })}
                  >
                    {connectionSecurityConfig(editModal.connection.type).options.map((option) => (
                      <option key={option.value} value={option.value}>
                        {option.label}
                      </option>
                    ))}
                  </SelectField>
                  <p className="field-hint">{connectionSecurityConfig(editModal.connection.type).hint}</p>
                  {editModal.form.sslMode === "verify-full" ? (
                    <>
                      <EtcdTLSHint enabled={editModal.connection.type === "etcd"} />
                      <TLSCertificateFields
                        value={editModal.form.tlsConfig}
                        onChange={(tlsConfig) => setEditModal({ ...editModal, form: { ...editModal.form, tlsConfig }, error: "" })}
                        preserveExisting
                      />
                    </>
                  ) : null}
                  <ConnectionMetadataFields
                    project={editModal.form.project}
                    environment={editModal.form.environment}
                    tags={editModal.form.tags}
                    projectSuggestions={projectSuggestions}
                    onProjectChange={(project) => setEditModal({ ...editModal, form: { ...editModal.form, project }, error: "" })}
                    onEnvironmentChange={(environment) =>
                      setEditModal({ ...editModal, form: { ...editModal.form, environment }, error: "" })
                    }
                    onTagsChange={(tags) => setEditModal({ ...editModal, form: { ...editModal.form, tags }, error: "" })}
                  />
                  <label className="field">
                    <span>备注</span>
                    <input
                      value={editModal.form.note}
                      onChange={(e) => setEditModal({ ...editModal, form: { ...editModal.form, note: e.target.value }, error: "" })}
                      placeholder="可选，记录用途或负责人"
                    />
                  </label>
                  {editModal.error ? <div className="error-banner">{editModal.error}</div> : null}
                </div>
                <div className="modal-f">
                  <Button type="button" variant="secondary" onClick={() => setEditModal(null)}>
                    取消
                  </Button>
                  <Button type="submit" disabled={editModal.submitting}>
                    {editModal.submitting ? "正在保存..." : "保存并测试"}
                  </Button>
                </div>
              </form>
            </div>,
            document.body,
          )
        : null}

      {seedDataConfirmOpen && seedDataModal ? (
        <ConfirmDialog
          title="覆盖写入假数据"
          description="这是中危操作，目标演示表中的现有数据会被覆盖。"
          tone="warning"
          confirmText="确认覆盖写入"
          submitting={seedDataModal.submitting}
          onCancel={() => setSeedDataConfirmOpen(false)}
          onConfirm={() => void executeSeedData()}
          details={[
            `目标：${seedDataModal.connection.name} / ${seedDataModal.database}`,
            `模板：${seedDataModal.template}`,
            `写入条数：${seedDataModal.rows}`,
          ]}
        />
      ) : null}

      {showForm
        ? createPortal(
            <div className="modal-backdrop" onMouseDown={closeConnectionForm}>
              <form
                className="modal wide"
                role="dialog"
                aria-modal="true"
                onSubmit={submit}
                onMouseDown={(event) => event.stopPropagation()}
              >
                <div className="modal-h">
                  <div style={{ flex: 1 }}>
                    <h3>新建数据库连接</h3>
                    <div className="ms">可直接保存草稿，或先测试连接再保存为可用连接。</div>
                  </div>
                  <button type="button" className="icon-btn ghost" onClick={closeConnectionForm}>
                    <X size={17} />
                  </button>
                </div>
                <div className="modal-b">
                  <div className="grid2">
                    <SelectField
                      label={requiredLabel("数据库类型")}
                      ariaLabel="数据库类型"
                      invalid={Boolean(formErrors.type)}
                      value={form.type}
                      onChange={(type) => {
                        const nextForm = {
                          ...form,
                          type: type as DatabaseType,
                          port: defaultPort(type as DatabaseType),
                          sslMode: defaultSecurityMode(type as DatabaseType),
                          tlsConfig: emptyTLSConfig(),
                        };
                        setForm(nextForm);
                        setFormErrors((current) => ({ ...current, type: undefined, port: undefined, sslMode: undefined }));
                      }}
                    >
                      <option value="PostgreSQL">PostgreSQL</option>
                      <option value="MySQL">MySQL</option>
                      <option value="MongoDB">MongoDB</option>
                      <option value="Neo4j">Neo4j</option>
                      <option value="etcd">etcd</option>
                    </SelectField>
                    <label className="field">
                      <span>{requiredLabel("连接名称")}</span>
                      <input
                        value={form.name}
                        className={formErrors.name ? "invalid" : ""}
                        onChange={(e) => {
                          updateFormField("name", e.target.value);
                          setNameCustomized(e.target.value.trim().length > 0);
                          setFormErrors((current) => ({ ...current, name: undefined }));
                        }}
                        onBlur={(e) => validateSingleField("name", { ...form, name: e.target.value })}
                        placeholder="输入便于识别的连接名称"
                      />
                    </label>
                  </div>
                  {formErrors.type || formErrors.name ? <div className="form-error">{formErrors.type ?? formErrors.name}</div> : null}

                  <div className="info-callout" style={{ marginBottom: 14 }}>
                    <Info size={15} style={{ flex: "none", marginTop: 2 }} />
                    <span>{formCompatibilityHint}</span>
                  </div>

                  <div className="field-row">
                    <label className="field">
                      <span>{requiredLabel("主机地址")}</span>
                      <input
                        value={form.host}
                        className={formErrors.host ? "invalid" : ""}
                        onChange={(e) => {
                          const nextForm = { ...form, host: e.target.value };
                          setForm(nextForm);
                          setFormErrors((current) => ({ ...current, host: validateConnectionForm(nextForm).host }));
                        }}
                        onBlur={(e) => validateSingleField("host", { ...form, host: e.target.value })}
                        placeholder="10.24.8.16"
                      />
                    </label>
                    <label className="field">
                      <span>{requiredLabel("端口")}</span>
                      <input
                        inputMode="numeric"
                        pattern="[0-9]*"
                        maxLength={5}
                        value={form.port}
                        className={formErrors.port ? "invalid" : ""}
                        onBeforeInput={(e) => {
                          const value = (e as unknown as { data?: string }).data ?? "";
                          if (value && /\D/.test(value)) e.preventDefault();
                        }}
                        onChange={(e) => {
                          const next = e.target.value.replace(/[^\d]/g, "").slice(0, 5);
                          const nextForm = { ...form, port: next };
                          setForm(nextForm);
                          setFormErrors((current) => ({ ...current, port: validateConnectionForm(nextForm).port }));
                        }}
                        onBlur={(e) => validateSingleField("port", { ...form, port: e.target.value })}
                      />
                    </label>
                  </div>
                  {formErrors.host || formErrors.port ? <div className="form-error">{formErrors.host ?? formErrors.port}</div> : null}

                  {form.type === "MongoDB" ? (
                    <MongoConnectionModeFields
                      mode={form.mongoMode}
                      seeds={form.mongoSeeds}
                      onModeChange={(mongoMode) => setForm({ ...form, mongoMode })}
                      onSeedsChange={(mongoSeeds) => setForm({ ...form, mongoSeeds })}
                    />
                  ) : null}

                  <div className="grid2">
                    <label className="field">
                      <span>{form.type === "etcd" ? "用户名（未启用鉴权可留空）" : requiredLabel("用户名")}</span>
                      <input
                        value={form.username}
                        className={formErrors.username ? "invalid" : ""}
                        onChange={(e) => {
                          updateFormField("username", e.target.value);
                          setFormErrors((current) => ({ ...current, username: undefined }));
                        }}
                        onBlur={(e) => validateSingleField("username", { ...form, username: e.target.value })}
                        placeholder={form.type === "etcd" ? "root（可选）" : "migration_user"}
                      />
                    </label>
                    <label className="field">
                      <span>{form.type === "etcd" ? "密码（可选）" : requiredLabel("密码")}</span>
                      <input
                        type="password"
                        value={form.password}
                        className={formErrors.password ? "invalid" : ""}
                        onChange={(e) => {
                          updateFormField("password", e.target.value);
                          setFormErrors((current) => ({ ...current, password: undefined }));
                        }}
                        onBlur={(e) => validateSingleField("password", { ...form, password: e.target.value })}
                      />
                    </label>
                  </div>
                  {formErrors.username || formErrors.password ? (
                    <div className="form-error">{formErrors.username ?? formErrors.password}</div>
                  ) : null}

                  <EtcdTLSModeGuide enabled={form.type === "etcd"} />

                  <SelectField
                    label={requiredLabel(formSecurityConfig.label)}
                    ariaLabel={formSecurityConfig.label}
                    invalid={Boolean(formErrors.sslMode)}
                    value={form.sslMode}
                    onChange={(sslMode) => {
                      setForm({ ...form, sslMode });
                      setFormErrors((current) => ({ ...current, sslMode: undefined }));
                    }}
                  >
                    {formSecurityConfig.options.map((option) => (
                      <option key={option.value} value={option.value}>
                        {option.label}
                      </option>
                    ))}
                  </SelectField>
                  {formErrors.sslMode ? <div className="form-error">{formErrors.sslMode}</div> : null}
                  <p className="field-hint">{formSecurityConfig.hint}</p>

                  {form.sslMode === "verify-full" ? (
                    <>
                      <EtcdTLSHint enabled={form.type === "etcd"} />
                      <TLSCertificateFields value={form.tlsConfig} onChange={(tlsConfig) => setForm({ ...form, tlsConfig })} />
                    </>
                  ) : null}

                  <ConnectionMetadataFields
                    project={form.project}
                    environment={form.environment}
                    tags={form.tags}
                    projectSuggestions={projectSuggestions}
                    onProjectChange={(project) => updateFormField("project", project)}
                    onEnvironmentChange={(environment) => updateFormField("environment", environment)}
                    onTagsChange={(tags) => updateFormField("tags", tags)}
                  />
                  <label className="field">
                    <span>备注</span>
                    <input
                      value={form.note}
                      onChange={(e) => setForm({ ...form, note: e.target.value })}
                      placeholder="可选，记录用途或负责人"
                    />
                  </label>
                  {formError ? <div className="error-banner">{formError}</div> : null}
                </div>

                <div className="modal-f">
                  <span className="note">地区由服务端自动解析（内网 IP 显示为“内网”）。</span>
                  <Button type="button" variant="secondary" onClick={closeConnectionForm}>
                    取消
                  </Button>
                  <Button type="submit" variant="secondary" data-action="save" disabled={Boolean(submittingAction)}>
                    {submittingAction === "save" ? "正在保存..." : "保存"}
                  </Button>
                  <Button type="submit" data-action="test" disabled={Boolean(submittingAction)}>
                    {submittingAction === "test" ? "正在测试..." : "测试并保存"}
                  </Button>
                </div>
              </form>
            </div>,
            document.body,
          )
        : null}
    </div>
  );
}
