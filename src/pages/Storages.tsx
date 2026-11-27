import {
  BookOpen,
  Check,
  Cloud,
  Edit3,
  FolderOpen,
  KeyRound,
  Plus,
  RefreshCw,
  Search,
  Star,
  Trash2,
  X,
} from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { api, type WebDAVEntry } from "../api";
import {
  ActionMenu,
  type ActionMenuItem,
  Badge,
  Button,
  Card,
  ConfirmDialog,
  DensitySwitch,
  requiredLabel,
  SelectField,
  useCollectionView,
} from "../components/UI";
import type { RemoteStorage, RemoteStorageInput, RemoteStorageType } from "../types";

type StorageForm = RemoteStorageInput & { secretKey: string };
type StorageFilter = "all" | "object" | "webdav" | "cloud";

const emptyForm: StorageForm = {
  type: "object",
  name: "",
  provider: "S3 Compatible",
  endpoint: "",
  bucket: "",
  region: "",
  accessKey: "",
  secretKey: "",
  path: "",
  authMode: "access_key",
  note: "",
  verified: false,
};

export function Storages({
  storages,
  onAdd,
  onUpdate,
  onDelete,
  onToggleFavorite,
}: {
  storages: RemoteStorage[];
  onAdd: (storage: RemoteStorage) => void;
  onUpdate: (storage: RemoteStorage) => void;
  onDelete: (storageId: string) => void;
  onToggleFavorite: (storage: RemoteStorage) => Promise<void>;
}) {
  const { prefs, updatePref } = useCollectionView();
  const viewMode = prefs.storage;
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<StorageFilter>("all");
  const [showForm, setShowForm] = useState(false);
  const [form, setForm] = useState<StorageForm>(emptyForm);
  const [editTarget, setEditTarget] = useState<RemoteStorage | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [testing, setTesting] = useState<string | null>(null);
  const [authorizing, setAuthorizing] = useState<string | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<RemoteStorage | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [bucketTarget, setBucketTarget] = useState<RemoteStorage | null>(null);
  const [bucketListTarget, setBucketListTarget] = useState<RemoteStorage | null>(null);
  const [bucketName, setBucketName] = useState("");
  const [creatingBucket, setCreatingBucket] = useState(false);
  const [viewingBuckets, setViewingBuckets] = useState(false);
  const [viewBucketOptions, setViewBucketOptions] = useState<string[]>([]);
  const [webDAVTarget, setWebDAVTarget] = useState<RemoteStorage | null>(null);
  const [webDAVPath, setWebDAVPath] = useState("");
  const [webDAVEntries, setWebDAVEntries] = useState<WebDAVEntry[]>([]);
  const [viewingWebDAV, setViewingWebDAV] = useState(false);
  const [webDAVDirectoryName, setWebDAVDirectoryName] = useState("");
  const [creatingWebDAVDirectory, setCreatingWebDAVDirectory] = useState(false);
  const [bucketLoading, setBucketLoading] = useState(false);
  const [bucketOptions, setBucketOptions] = useState<string[]>([]);
  const [bucketMenuOpen, setBucketMenuOpen] = useState(false);
  const [webDAVDirectoryLoading, setWebDAVDirectoryLoading] = useState(false);
  const [webDAVDirectoryOptions, setWebDAVDirectoryOptions] = useState<WebDAVEntry[]>([]);
  const [webDAVDirectoryMenuOpen, setWebDAVDirectoryMenuOpen] = useState(false);
  const [pageError, setPageError] = useState("");
  const [favoritingId, setFavoritingId] = useState<string | null>(null);
  const [formError, setFormError] = useState("");
  const bucketPickerRef = useRef<HTMLDivElement | null>(null);
  const webDAVDirectoryPickerRef = useRef<HTMLDivElement | null>(null);

  const summary = useMemo(
    () => ({
      total: storages.length,
      object: storages.filter((item) => item.type === "object").length,
      webdav: storages.filter((item) => item.type === "webdav").length,
      cloudDrive: storages.filter(
        (item) => item.type === "webdav" || item.type === "google_drive" || item.type === "one_drive",
      ).length,
      cloudAndLocal: storages.filter(
        (item) => item.type === "google_drive" || item.type === "one_drive" || item.type === "local",
      ).length,
    }),
    [storages],
  );

  const filteredStorages = useMemo(() => {
    const q = query.trim().toLowerCase();
    return [...storages]
      .filter((item) => {
        if (filter === "object" && item.type !== "object") return false;
        if (filter === "webdav" && item.type !== "webdav") return false;
        if (filter === "cloud" && item.type !== "google_drive" && item.type !== "one_drive" && item.type !== "local") {
          return false;
        }
        if (!q) return true;
        return [item.name, item.provider, item.endpoint, item.bucket, item.path, item.note]
          .filter(Boolean)
          .some((field) => field!.toLowerCase().includes(q));
      })
      .sort((left, right) => Number(Boolean(right.favorite)) - Number(Boolean(left.favorite)));
  }, [filter, query, storages]);

  useEffect(() => {
    if (!bucketMenuOpen) return;
    const close = (event: MouseEvent) => {
      if (!bucketPickerRef.current?.contains(event.target as Node)) {
        setBucketMenuOpen(false);
      }
    };
    window.addEventListener("mousedown", close);
    return () => window.removeEventListener("mousedown", close);
  }, [bucketMenuOpen]);

  useEffect(() => {
    if (!webDAVDirectoryMenuOpen) return;
    const close = (event: MouseEvent) => {
      if (!webDAVDirectoryPickerRef.current?.contains(event.target as Node)) {
        setWebDAVDirectoryMenuOpen(false);
      }
    };
    window.addEventListener("mousedown", close);
    return () => window.removeEventListener("mousedown", close);
  }, [webDAVDirectoryMenuOpen]);

  function openCreate() {
    setEditTarget(null);
    setForm(emptyForm);
    setBucketOptions([]);
    setBucketMenuOpen(false);
    setWebDAVDirectoryOptions([]);
    setWebDAVDirectoryMenuOpen(false);
    setFormError("");
    setShowForm(true);
  }

  function openEdit(storage: RemoteStorage) {
    setEditTarget(storage);
    setForm({
      type: storage.type,
      name: storage.name,
      provider: storage.provider,
      endpoint: storage.endpoint ?? "",
      bucket: storage.bucket ?? "",
      region: storage.region ?? "",
      accessKey: storage.accessKey ?? "",
      secretKey: "",
      path: storage.path ?? "",
      authMode: storage.authMode ?? defaultAuthMode(storage.type),
      note: storage.note ?? "",
      verified: storage.status === "connected",
    });
    setBucketOptions(storage.bucket ? [storage.bucket] : []);
    setBucketMenuOpen(false);
    setWebDAVDirectoryOptions(storage.type === "webdav" && storage.path ? [webDAVOptionFromPath(storage.path)] : []);
    setWebDAVDirectoryMenuOpen(false);
    setFormError("");
    setShowForm(true);
  }

  async function saveAndAuthorize() {
    if (submitting) return;
    setSubmitting(true);
    setFormError("");
    try {
      const created = await api.createStorage(normalizeForm(form));
      onAdd(created);
      setEditTarget(created);
      if (created.type === "one_drive") {
        await authorizeOneDrive(created);
      } else if (created.type === "google_drive") {
        await authorizeGoogleDrive(created);
      }
    } catch (reason) {
      setFormError(reason instanceof Error ? reason.message : "保存并启动授权失败");
    } finally {
      setSubmitting(false);
    }
  }

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setSubmitting(true);
    setFormError("");
    setPageError("");
    try {
      const input = normalizeForm(form);
      let saved: RemoteStorage;
      if (editTarget) {
        saved = await api.updateStorage(editTarget.id, input);
        onUpdate(saved);
      } else {
        saved = await api.createStorage(input);
        onAdd(saved);
      }
      setShowForm(false);
      if (shouldAutoTestStorage(saved)) {
        setTesting(saved.id);
        try {
          const result = await api.testStorage(saved.id);
          onUpdate(result.storage);
          if (result.storage.status === "warning") {
            setPageError(`${saved.name}：${result.message}`);
          }
        } catch (testReason) {
          setPageError(
            testReason instanceof Error ? `${saved.name}：${testReason.message}` : `${saved.name}：测试远端存储失败`,
          );
        } finally {
          setTesting(null);
        }
      }
    } catch (reason) {
      setFormError(reason instanceof Error ? reason.message : "保存远端存储失败");
    } finally {
      setSubmitting(false);
    }
  }

  async function test(storage: RemoteStorage) {
    setTesting(storage.id);
    setPageError("");
    try {
      const result = await api.testStorage(storage.id);
      onUpdate(result.storage);
    } catch (reason) {
      setPageError(reason instanceof Error ? reason.message : "测试远端存储失败");
    } finally {
      setTesting(null);
    }
  }

  async function authorizeOneDrive(storage: RemoteStorage) {
    setAuthorizing(storage.id);
    setPageError("");
    try {
      const result = await api.authorizeOneDrive(storage.id);
      window.location.assign(result.url);
    } catch (reason) {
      setPageError(reason instanceof Error ? reason.message : "启动 OneDrive 授权失败");
      setAuthorizing(null);
    }
  }

  async function authorizeGoogleDrive(storage: RemoteStorage) {
    setAuthorizing(storage.id);
    setPageError("");
    try {
      const result = await api.authorizeGoogleDrive(storage.id);
      window.location.assign(result.url);
    } catch (reason) {
      setPageError(reason instanceof Error ? reason.message : "启动 Google Drive 授权失败");
      setAuthorizing(null);
    }
  }

  async function toggleFavorite(storage: RemoteStorage) {
    setFavoritingId(storage.id);
    setPageError("");
    try {
      await onToggleFavorite(storage);
    } catch (error) {
      setPageError(error instanceof Error ? error.message : "更新收藏失败");
    } finally {
      setFavoritingId(null);
    }
  }

  async function confirmDeleteStorage() {
    if (!deleteTarget) return;
    setDeleting(true);
    setPageError("");
    try {
      await api.deleteStorage(deleteTarget.id);
      onDelete(deleteTarget.id);
      setDeleteTarget(null);
    } catch (reason) {
      setPageError(reason instanceof Error ? reason.message : "删除远端存储失败");
    } finally {
      setDeleting(false);
    }
  }

  function openBucketCreate(storage: RemoteStorage) {
    setBucketTarget(storage);
    setBucketName("");
    setFormError("");
    setPageError("");
  }

  async function openBucketList(storage: RemoteStorage) {
    setBucketListTarget(storage);
    setViewBucketOptions([]);
    setViewingBuckets(true);
    setPageError("");
    try {
      const result = await api.storedObjectBuckets(storage.id, {
        type: storage.type,
        name: storage.name,
        provider: storage.provider,
        endpoint: storage.endpoint,
        bucket: storage.bucket,
        region: storage.region,
        accessKey: storage.accessKey,
        path: storage.path,
        authMode: storage.authMode,
        note: storage.note,
      });
      setViewBucketOptions(result.buckets);
    } catch (reason) {
      setPageError(reason instanceof Error ? reason.message : "获取 Bucket 列表失败");
    } finally {
      setViewingBuckets(false);
    }
  }

  async function createBucket(event: React.FormEvent) {
    event.preventDefault();
    if (!bucketTarget) return;
    setCreatingBucket(true);
    setPageError("");
    try {
      const result = await api.createStoredObjectBucket(bucketTarget.id, bucketName);
      onUpdate(result.storage);
      if (editTarget?.id === bucketTarget.id) {
        setBucketOptions(result.buckets);
        setForm((current) => ({ ...current, bucket: result.storage.bucket ?? bucketName.trim(), verified: true }));
      }
      setBucketTarget(null);
      setBucketName("");
      if (bucketListTarget?.id === bucketTarget.id) {
        setViewBucketOptions(result.buckets);
      }
    } catch (reason) {
      setPageError(reason instanceof Error ? reason.message : "创建 Bucket 失败");
    } finally {
      setCreatingBucket(false);
    }
  }

  async function openWebDAVDirectory(storage: RemoteStorage) {
    setWebDAVTarget(storage);
    setWebDAVPath(storage.path ?? "");
    setWebDAVEntries([]);
    setWebDAVDirectoryName("");
    setPageError("");
    await loadWebDAVDirectory(storage, storage.path ?? "");
  }

  async function loadWebDAVDirectory(storage = webDAVTarget, path = webDAVPath) {
    if (!storage) return;
    setViewingWebDAV(true);
    setPageError("");
    try {
      const result = await api.webDAVDirectory(storage.id, path);
      setWebDAVPath(result.path);
      setWebDAVEntries(result.entries.filter((entry) => entry.isDirectory));
    } catch (reason) {
      setPageError(reason instanceof Error ? reason.message : "获取 WebDAV 目录失败");
    } finally {
      setViewingWebDAV(false);
    }
  }

  async function createWebDAVDirectory(event: React.FormEvent) {
    event.preventDefault();
    if (!webDAVTarget) return;
    setCreatingWebDAVDirectory(true);
    setPageError("");
    try {
      const result = await api.createWebDAVDirectory(webDAVTarget.id, webDAVPath, webDAVDirectoryName);
      setWebDAVPath(result.path);
      setWebDAVEntries(result.entries.filter((entry) => entry.isDirectory));
      setWebDAVDirectoryName("");
    } catch (reason) {
      setPageError(reason instanceof Error ? reason.message : "创建 WebDAV 目录失败");
    } finally {
      setCreatingWebDAVDirectory(false);
    }
  }

  function changeType(type: RemoteStorageType) {
    setForm((current) => ({
      ...current,
      type,
      provider: defaultProvider(type),
      endpoint:
        type === "one_drive"
          ? "https://graph.microsoft.com/v1.0"
          : type === "google_drive"
            ? "https://www.googleapis.com"
            : type === "object" || type === "webdav"
              ? current.endpoint
              : "",
      authMode: defaultAuthMode(type),
    }));
    setBucketOptions([]);
    setBucketMenuOpen(false);
    setWebDAVDirectoryOptions([]);
    setWebDAVDirectoryMenuOpen(false);
    setFormError("");
  }

  async function loadBuckets() {
    setBucketLoading(true);
    setFormError("");
    try {
      const result = editTarget
        ? await api.storedObjectBuckets(editTarget.id, normalizeForm(form))
        : await api.objectBuckets(normalizeForm(form));
      setBucketOptions(result.buckets);
      setBucketMenuOpen(result.buckets.length > 0);
      if (result.buckets.length && !result.buckets.includes(form.bucket ?? "")) {
        setForm((current) => ({ ...current, bucket: result.buckets[0], verified: true }));
      } else if (result.buckets.length) {
        setForm((current) => ({ ...current, verified: true }));
      }
    } catch (reason) {
      setFormError(reason instanceof Error ? reason.message : "获取 Bucket 列表失败，可手动填写 Bucket");
    } finally {
      setBucketLoading(false);
    }
  }

  async function loadWebDAVDirectories() {
    setWebDAVDirectoryLoading(true);
    setFormError("");
    try {
      const input = { ...normalizeForm(form), path: "" };
      const result = editTarget
        ? await api.storedWebDAVDirectories(editTarget.id, input)
        : await api.webDAVDirectories(input);
      const directories = result.entries.filter((entry) => entry.isDirectory);
      setWebDAVDirectoryOptions(directories);
      setWebDAVDirectoryMenuOpen(directories.length > 0);
      if (!directories.length) {
        setFormError("当前 WebDAV 根目录下没有可选一级目录，可继续手动填写默认目录");
      }
    } catch (reason) {
      setFormError(reason instanceof Error ? reason.message : "获取 WebDAV 目录失败，可手动填写默认目录");
    } finally {
      setWebDAVDirectoryLoading(false);
    }
  }

  function storageMenuItems(storage: RemoteStorage): ActionMenuItem[] {
    const items: ActionMenuItem[] = [
      { key: "edit", label: "编辑配置", icon: <Edit3 size={14} />, onClick: () => openEdit(storage) },
    ];
    if (storage.type === "object") {
      items.push({ key: "create-bucket", label: "创建 Bucket", icon: <Plus size={14} />, onClick: () => openBucketCreate(storage) });
      items.push({ key: "view-bucket", label: "查看 Bucket", icon: <FolderOpen size={14} />, onClick: () => void openBucketList(storage) });
    }
    if (storage.type === "webdav") {
      items.push({ key: "view-dir", label: "查看目录", icon: <FolderOpen size={14} />, onClick: () => void openWebDAVDirectory(storage) });
    }
    if (storage.type === "one_drive") {
      items.push({
        key: "auth-onedrive",
        label: authorizing === storage.id ? "跳转授权中..." : "开始授权",
        icon: <KeyRound size={14} />,
        disabled: authorizing === storage.id,
        onClick: () => void authorizeOneDrive(storage),
      });
      items.push({
        key: "guide-onedrive",
        label: "接入说明文档",
        icon: <BookOpen size={14} />,
        onClick: () => openStorageDoc("onedrive", storage.id),
      });
    }
    if (storage.type === "google_drive") {
      if ((storage.authMode ?? "oauth") !== "service_account") {
        items.push({
          key: "auth-google-drive",
          label: authorizing === storage.id ? "跳转授权中..." : "开始授权",
          icon: <KeyRound size={14} />,
          disabled: authorizing === storage.id,
          onClick: () => void authorizeGoogleDrive(storage),
        });
      }
      items.push({
        key: "guide-google-drive",
        label: "接入说明文档",
        icon: <BookOpen size={14} />,
        onClick: () => openStorageDoc("google_drive", storage.id, storage.authMode),
      });
    }
    items.push({
      key: "test",
      label: testing === storage.id ? "测试中..." : "测试配置",
      icon: <RefreshCw size={14} />,
      disabled: testing === storage.id,
      onClick: () => void test(storage),
    });
    items.push({
      key: "delete",
      label: "删除存储",
      icon: <Trash2 size={14} />,
      danger: true,
      dividerBefore: true,
      onClick: () => setDeleteTarget(storage),
    });
    return items;
  }

  return (
    <div className="page-stack">
      <div className="h-row">
        <div className="page-h">
          <h2>远端存储</h2>
          <p>集中管理数据库备份可同步的 S3 对象存储、WebDAV、云盘与本地归档目录</p>
        </div>
        <div className="sp" />
        <Button onClick={openCreate}>
          <Plus size={15} />
          新增存储
        </Button>
      </div>

      {pageError ? <div className="error-banner">{pageError}</div> : null}

      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(auto-fit, minmax(190px, 1fr))",
          gap: 12,
        }}
      >
        <Summary title="全部存储" value={summary.total} detail="配置记录已保存" />
        <Summary title="对象存储" value={summary.object} detail="S3 兼容、云厂商 OSS" tone="blue" />
        <Summary title="网盘存储" value={summary.cloudDrive} detail="WebDAV / 云盘授权" tone="green" />
      </div>

      <div className="h-row" style={{ gap: 8 }}>
        <div className="sfield" style={{ flex: "1 1 240px", maxWidth: 320 }}>
          <Search size={14} className="sfield-ic" />
          <input
            className="inp"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="搜索名称、Endpoint、Bucket、路径..."
            aria-label="搜索远端存储"
          />
        </div>
        <div className="chiprow">
          <button type="button" className={filter === "all" ? "fchip on" : "fchip"} onClick={() => setFilter("all")}>
            全部 {summary.total}
          </button>
          <button
            type="button"
            className={filter === "object" ? "fchip on" : "fchip"}
            onClick={() => setFilter("object")}
          >
            对象存储 {summary.object}
          </button>
          <button
            type="button"
            className={filter === "webdav" ? "fchip on" : "fchip"}
            onClick={() => setFilter("webdav")}
          >
            WebDAV {summary.webdav}
          </button>
          <button
            type="button"
            className={filter === "cloud" ? "fchip on" : "fchip"}
            onClick={() => setFilter("cloud")}
          >
            云盘 / 本地 {summary.cloudAndLocal}
          </button>
        </div>
        <div className="sp" />
        <DensitySwitch
          label="视图"
          value={viewMode}
          onChange={(next) => updatePref("storage", next as "cards" | "list")}
          options={[
            { value: "cards", label: "卡片" },
            { value: "list", label: "列表" },
          ]}
        />
      </div>

      {filteredStorages.length === 0 ? (
        <Card className="empty">
          <Cloud size={28} style={{ color: "var(--muted)", marginBottom: 8 }} />
          <div className="e-t">{storages.length ? "没有匹配的远端存储" : "暂无远端存储"}</div>
          <div className="e-s">
            {storages.length
              ? "调整搜索关键词或类型筛选，或新建远端存储目标。"
              : "新增对象存储或云盘授权后，备份任务就能选择同步目标。"}
          </div>
          {!storages.length ? (
            <Button onClick={openCreate}>
              <Plus size={15} />
              新增存储
            </Button>
          ) : null}
        </Card>
      ) : viewMode === "cards" ? (
        <div className="storage-cards-grid">
          {filteredStorages.map((storage) => (
            <Card key={storage.id} className="pad" style={{ display: "grid", gap: 14 }}>
              <div style={{ display: "flex", alignItems: "flex-start", gap: 10 }}>
                <span
                  style={{
                    width: 38,
                    height: 38,
                    borderRadius: 10,
                    background: "var(--surface)",
                    border: "1px solid var(--hair)",
                    display: "inline-flex",
                    alignItems: "center",
                    justifyContent: "center",
                    color: "var(--indigo-ink)",
                    flexShrink: 0,
                  }}
                >
                  <StorageIcon type={storage.type} />
                </span>
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div
                    style={{
                      fontWeight: 650,
                      fontSize: 14,
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                    }}
                  >
                    {storage.name}
                  </div>
                  <div style={{ fontSize: 12, color: "var(--muted)", marginTop: 2 }}>
                    {typeLabel(storage.type)} · {storage.provider}
                  </div>
                </div>
                <div style={{ display: "flex", alignItems: "center", gap: 2 }}>
                  <Button
                    variant="ghost"
                    size="sm"
                    aria-label={storage.favorite ? `取消收藏 ${storage.name}` : `收藏 ${storage.name}`}
                    data-tooltip-mode="always"
                    data-tooltip-content={storage.favorite ? "取消收藏" : "收藏并置顶"}
                    disabled={favoritingId === storage.id}
                    onClick={() => void toggleFavorite(storage)}
                    style={{ color: storage.favorite ? "#F59E0B" : "var(--muted)" }}
                  >
                    <Star size={15} fill={storage.favorite ? "currentColor" : "none"} />
                  </Button>
                  <ActionMenu ariaLabel={`${storage.name} 更多操作`} items={storageMenuItems(storage)} />
                </div>
              </div>

              <div className="kv" style={{ gap: "8px 12px" }}>
                <div>
                  <span className="k">Endpoint</span>
                  <div className="v mono" style={{ fontSize: 12, wordBreak: "break-all" }}>
                    {storage.endpoint || "未设置"}
                  </div>
                </div>
                <div>
                  <span className="k">Bucket / 路径</span>
                  <div className="v mono" style={{ fontSize: 12, wordBreak: "break-all" }}>
                    {storage.bucket || storage.path || "未设置"}
                  </div>
                </div>
                <div style={{ gridColumn: "1 / -1" }}>
                  <span className="k">备注</span>
                  <div className="v" style={{ fontSize: 12.5, color: storage.note ? "var(--ink-2)" : "var(--muted)" }}>
                    {storage.note || "未填写"}
                  </div>
                </div>
              </div>

              <div
                style={{
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "space-between",
                  paddingTop: 10,
                  borderTop: "1px solid var(--hair)",
                }}
              >
                <Badge tone={statusTone(storage.status)}>{statusLabel(storage.status)}</Badge>
                <span style={{ fontSize: 12, color: "var(--muted)" }}>{storageKindFooter(storage.type)}</span>
              </div>
            </Card>
          ))}
        </div>
      ) : (
        <div className="ledger card-list">
          {filteredStorages.map((storage) => (
            <div className="lrow" key={storage.id}>
              <span
                className="tglyph"
                style={{
                  width: 38,
                  height: 38,
                  borderRadius: 10,
                  background: "var(--surface)",
                  border: "1px solid var(--hair)",
                  color: "var(--indigo-ink)",
                }}
              >
                <StorageIcon type={storage.type} />
              </span>
              <div className="lr-main">
                <div className="lr-t">
                  <span>{storage.name}</span>
                  <Badge tone={statusTone(storage.status)}>{statusLabel(storage.status)}</Badge>
                  <Badge tone="gray">{typeLabel(storage.type)}</Badge>
                </div>
                <div className="lr-s mono">
                  {[
                    storage.provider,
                    storage.endpoint,
                    storage.bucket || storage.path,
                    storage.note,
                  ]
                    .filter(Boolean)
                    .join(" · ") || "未配置路径"}
                </div>
              </div>
              <div className="lr-actions" onClick={(event) => event.stopPropagation()}>
                <Button
                  variant="ghost"
                  size="sm"
                  aria-label={storage.favorite ? `取消收藏 ${storage.name}` : `收藏 ${storage.name}`}
                  data-tooltip-mode="always"
                  data-tooltip-content={storage.favorite ? "取消收藏" : "收藏并置顶"}
                  disabled={favoritingId === storage.id}
                  onClick={() => void toggleFavorite(storage)}
                  style={{ color: storage.favorite ? "#F59E0B" : "var(--muted)" }}
                >
                  <Star size={15} fill={storage.favorite ? "currentColor" : "none"} />
                </Button>
                <ActionMenu ariaLabel={`${storage.name} 更多操作`} items={storageMenuItems(storage)} />
              </div>
            </div>
          ))}
        </div>
      )}

      {bucketTarget
        ? createPortal(
            <div className="modal-backdrop" onMouseDown={() => setBucketTarget(null)}>
              <form
                className="modal"
                onSubmit={createBucket}
                onMouseDown={(event) => event.stopPropagation()}
              >
                <div className="modal-h">
                  <div>
                    <h3>创建 Bucket</h3>
                    <div className="ms">
                      {bucketTarget.name} · {bucketTarget.endpoint}
                    </div>
                  </div>
                  <button type="button" className="icon-btn ghost" onClick={() => setBucketTarget(null)} aria-label="关闭">
                    <X size={17} />
                  </button>
                </div>
                <div className="modal-b">
                  <label className="field">
                    <span>{requiredLabel("Bucket 名称")}</span>
                    <input
                      required
                      autoFocus
                      value={bucketName}
                      onChange={(event) => setBucketName(event.target.value)}
                      placeholder="例如 databridge-backups"
                    />
                  </label>
                </div>
                <div className="modal-f">
                  <Button type="button" variant="secondary" onClick={() => setBucketTarget(null)}>
                    取消
                  </Button>
                  <Button type="submit" disabled={creatingBucket}>
                    {creatingBucket ? "正在创建..." : "创建 Bucket"}
                  </Button>
                </div>
              </form>
            </div>,
            document.body,
          )
        : null}

      {bucketListTarget
        ? createPortal(
            <div className="modal-backdrop" onMouseDown={() => setBucketListTarget(null)}>
              <div className="modal" onMouseDown={(event) => event.stopPropagation()}>
                <div className="modal-h">
                  <div>
                    <h3>Bucket 列表</h3>
                    <div className="ms">
                      {bucketListTarget.name} · {bucketListTarget.endpoint}
                    </div>
                  </div>
                  <button type="button" className="icon-btn ghost" onClick={() => setBucketListTarget(null)} aria-label="关闭">
                    <X size={17} />
                  </button>
                </div>
                <div className="modal-b">
                  {viewingBuckets ? (
                    <div className="info-callout">正在获取 Bucket 列表...</div>
                  ) : viewBucketOptions.length ? (
                    <div className="ledger card-list">
                      {viewBucketOptions.map((bucket) => (
                        <div className="lrow" key={bucket}>
                          <div className="lr-main">
                            <div className="lr-t mono">{bucket}</div>
                          </div>
                          {bucket === bucketListTarget.bucket ? <Badge tone="green">当前</Badge> : null}
                        </div>
                      ))}
                    </div>
                  ) : (
                    <div className="empty" style={{ padding: "24px 16px" }}>
                      <Cloud size={22} style={{ color: "var(--muted)", marginBottom: 6 }} />
                      <div className="e-t">暂无 Bucket</div>
                      <div className="e-s">可以通过“创建 Bucket”快速新增一个。</div>
                    </div>
                  )}
                </div>
                <div className="modal-f">
                  <Button type="button" variant="secondary" onClick={() => setBucketListTarget(null)}>
                    关闭
                  </Button>
                  <Button
                    type="button"
                    onClick={() => bucketListTarget && openBucketCreate(bucketListTarget)}
                  >
                    <Plus size={15} />
                    创建 Bucket
                  </Button>
                </div>
              </div>
            </div>,
            document.body,
          )
        : null}

      {webDAVTarget
        ? createPortal(
            <div className="modal-backdrop" onMouseDown={() => setWebDAVTarget(null)}>
              <div className="modal wide" onMouseDown={(event) => event.stopPropagation()}>
                <div className="modal-h">
                  <div>
                    <h3>WebDAV 目录</h3>
                    <div className="ms mono">
                      {webDAVTarget.name} · /{webDAVPath || webDAVTarget.path || ""}
                    </div>
                  </div>
                  <button type="button" className="icon-btn ghost" onClick={() => setWebDAVTarget(null)} aria-label="关闭">
                    <X size={17} />
                  </button>
                </div>
                <div className="modal-b" style={{ display: "grid", gap: 14 }}>
                  <form
                    onSubmit={createWebDAVDirectory}
                    style={{ display: "flex", gap: 8, alignItems: "flex-end", flexWrap: "wrap" }}
                  >
                    <label className="field" style={{ flex: "1 1 220px", marginBottom: 0 }}>
                      <span>新目录名称</span>
                      <input
                        value={webDAVDirectoryName}
                        onChange={(event) => setWebDAVDirectoryName(event.target.value)}
                        placeholder="例如 2026-archive"
                      />
                    </label>
                    <Button type="submit" style={{ height: 38 }} disabled={creatingWebDAVDirectory || !webDAVDirectoryName.trim()}>
                      {creatingWebDAVDirectory ? "创建中..." : "创建目录"}
                    </Button>
                    <Button
                      type="button"
                      variant="secondary"
                      style={{ height: 38 }}
                      disabled={viewingWebDAV}
                      onClick={() => void loadWebDAVDirectory()}
                    >
                      <RefreshCw size={14} />
                      {viewingWebDAV ? "刷新中" : "刷新"}
                    </Button>
                  </form>

                  {viewingWebDAV ? (
                    <div className="info-callout">正在读取 WebDAV 目录...</div>
                  ) : webDAVEntries.length ? (
                    <div className="ledger card-list">
                      {webDAVEntries.map((entry) => (
                        <div className="lrow" key={entry.path}>
                          <FolderOpen size={16} style={{ color: "var(--muted)", flexShrink: 0 }} />
                          <div className="lr-main">
                            <div className="lr-t">{entry.name}</div>
                            <div className="lr-s">
                              文件夹{entry.modified ? ` · ${formatWebDAVTime(entry.modified)}` : ""}
                            </div>
                          </div>
                        </div>
                      ))}
                    </div>
                  ) : (
                    <div className="empty" style={{ padding: "24px 16px" }}>
                      <FolderOpen size={22} style={{ color: "var(--muted)", marginBottom: 6 }} />
                      <div className="e-t">暂无子目录</div>
                      <div className="e-s">可以创建目录，备份文件不会在这里展开显示。</div>
                    </div>
                  )}
                </div>
                <div className="modal-f">
                  <Button type="button" variant="secondary" onClick={() => setWebDAVTarget(null)}>
                    关闭
                  </Button>
                </div>
              </div>
            </div>,
            document.body,
          )
        : null}

      {showForm
        ? createPortal(
            <div className="modal-backdrop" onMouseDown={() => setShowForm(false)}>
              <form
                className="modal wide"
                onSubmit={submit}
                onMouseDown={(event) => event.stopPropagation()}
              >
                <div className="modal-h">
                  <div>
                    <h3>{editTarget ? "编辑远端存储" : "新增远端存储"}</h3>
                    <div className="ms">密钥会加密保存，不会返回到浏览器</div>
                  </div>
                  <button type="button" className="icon-btn ghost" onClick={() => setShowForm(false)} aria-label="关闭">
                    <X size={17} />
                  </button>
                </div>

                <div className="modal-b">
                  {formError ? (
                    <div className="error-banner" style={{ marginBottom: 14 }}>
                      {formError}
                    </div>
                  ) : null}

                  <SelectField
                    label={requiredLabel("存储类型")}
                    value={form.type}
                    onChange={(value) => changeType(value as RemoteStorageType)}
                  >
                    <option value="object">对象存储</option>
                    <option value="webdav">WebDAV 网盘</option>
                    <option value="google_drive">Google Drive</option>
                    <option value="one_drive">OneDrive</option>
                    <option value="local">本地目录</option>
                  </SelectField>

                  <div className="grid2">
                    <label className="field">
                      <span>{requiredLabel("名称")}</span>
                      <input
                        required
                        value={form.name}
                        onChange={(event) => setForm({ ...form, name: event.target.value })}
                        placeholder="生产备份 OSS"
                      />
                    </label>
                    <label className="field">
                      <span>{requiredLabel("服务商")}</span>
                      <input
                        required
                        value={form.provider}
                        onChange={(event) => setForm({ ...form, provider: event.target.value })}
                        placeholder="AWS S3 / Google Drive / OneDrive"
                      />
                    </label>
                  </div>

                  {form.type === "object" ? (
                    <>
                      <label className="field">
                        <span>{requiredLabel("Endpoint")}</span>
                        <input
                          required
                          value={form.endpoint}
                          onChange={(event) => setForm({ ...form, endpoint: event.target.value, verified: false })}
                          placeholder="https://s3.amazonaws.com"
                        />
                      </label>
                      <div className="grid2">
                        <label className="field">
                          <span>{requiredLabel("Access Key")}</span>
                          <input
                            required
                            value={form.accessKey}
                            onChange={(event) => setForm({ ...form, accessKey: event.target.value, verified: false })}
                          />
                        </label>
                        <label className="field">
                          <span>{editTarget ? "Secret Key" : requiredLabel("Secret Key")}</span>
                          <input
                            type="password"
                            required={!editTarget}
                            value={form.secretKey}
                            onChange={(event) => setForm({ ...form, secretKey: event.target.value, verified: false })}
                            placeholder={editTarget ? "留空则保持当前密钥" : ""}
                          />
                        </label>
                      </div>
                      <div
                        ref={bucketPickerRef}
                        style={{ display: "flex", gap: 8, alignItems: "flex-end", marginBottom: 14 }}
                      >
                        <div style={{ flex: 1, position: "relative" }}>
                          <label className="field" style={{ marginBottom: 0 }}>
                            <span>{requiredLabel("Bucket")}</span>
                            <input
                              required
                              value={form.bucket}
                              onFocus={() => bucketOptions.length > 0 && setBucketMenuOpen(true)}
                              onClick={() => bucketOptions.length > 0 && setBucketMenuOpen(true)}
                              onChange={(event) => {
                                const bucket = event.target.value;
                                setForm({ ...form, bucket, verified: bucketOptions.includes(bucket) });
                                setBucketMenuOpen(bucketOptions.length > 0);
                              }}
                              placeholder={bucketOptions.length ? "输入或选择 Bucket" : "先获取 Bucket 列表"}
                            />
                          </label>
                          {bucketMenuOpen && bucketOptions.length > 0 ? (
                            <div
                              className="csel-list"
                              style={{
                                position: "absolute",
                                top: "100%",
                                left: 0,
                                right: 0,
                                marginTop: 4,
                                zIndex: 30,
                              }}
                            >
                              {bucketOptions.map((bucket) => (
                                <button
                                  key={bucket}
                                  type="button"
                                  className={bucket === form.bucket ? "csel-opt active" : "csel-opt"}
                                  onMouseDown={(event) => event.preventDefault()}
                                  onClick={() => {
                                    setForm((current) => ({ ...current, bucket, verified: true }));
                                    setBucketMenuOpen(false);
                                  }}
                                >
                                  <span style={{ flex: 1 }}>{bucket}</span>
                                  {bucket === form.bucket ? <Check size={13} /> : null}
                                </button>
                              ))}
                            </div>
                          ) : null}
                        </div>
                        <Button
                          type="button"
                          variant="secondary"
                          style={{ height: 38 }}
                          onClick={loadBuckets}
                          disabled={bucketLoading || !form.endpoint || !form.accessKey || (!form.secretKey && !editTarget)}
                        >
                          <RefreshCw size={14} />
                          {bucketLoading ? "获取中..." : "获取 Bucket"}
                        </Button>
                      </div>
                      <div className="grid2">
                        <label className="field">
                          <span>Region</span>
                          <input
                            value={form.region}
                            onChange={(event) => setForm({ ...form, region: event.target.value })}
                            placeholder="可选，例如 us-east-1"
                          />
                        </label>
                        <label className="field">
                          <span>默认目录</span>
                          <input
                            value={form.path}
                            onChange={(event) => setForm({ ...form, path: event.target.value })}
                            placeholder="databridge/backups"
                          />
                        </label>
                      </div>
                    </>
                  ) : null}

                  {form.type === "webdav" ? (
                    <>
                      <label className="field">
                        <span>{requiredLabel("Endpoint")}</span>
                        <input
                          required
                          value={form.endpoint}
                          onChange={(event) => setForm({ ...form, endpoint: event.target.value, verified: false })}
                          placeholder="https://dav.jianguoyun.com/dav"
                        />
                      </label>
                      <div className="grid2">
                        <label className="field">
                          <span>{requiredLabel("用户名")}</span>
                          <input
                            required
                            value={form.accessKey}
                            onChange={(event) => setForm({ ...form, accessKey: event.target.value, verified: false })}
                            placeholder="WebDAV 账号或邮箱"
                          />
                        </label>
                        <label className="field">
                          <span>{editTarget ? "密码 / 应用密码" : requiredLabel("密码 / 应用密码")}</span>
                          <input
                            type="password"
                            required={!editTarget}
                            value={form.secretKey}
                            onChange={(event) => setForm({ ...form, secretKey: event.target.value, verified: false })}
                            placeholder={editTarget ? "留空则保持当前密码" : "建议使用应用专用密码"}
                          />
                        </label>
                      </div>
                      <div
                        ref={webDAVDirectoryPickerRef}
                        style={{ display: "flex", gap: 8, alignItems: "flex-end", marginBottom: 14 }}
                      >
                        <div style={{ flex: 1, position: "relative" }}>
                          <label className="field" style={{ marginBottom: 0 }}>
                            <span>默认目录</span>
                            <input
                              value={form.path}
                              onFocus={() => webDAVDirectoryOptions.length > 0 && setWebDAVDirectoryMenuOpen(true)}
                              onClick={() => webDAVDirectoryOptions.length > 0 && setWebDAVDirectoryMenuOpen(true)}
                              onChange={(event) => {
                                setForm({ ...form, path: event.target.value, verified: false });
                                setWebDAVDirectoryMenuOpen(webDAVDirectoryOptions.length > 0);
                              }}
                              placeholder={webDAVDirectoryOptions.length ? "输入或选择一级目录" : "DataBridge Backups"}
                            />
                          </label>
                          {webDAVDirectoryMenuOpen && webDAVDirectoryOptions.length > 0 ? (
                            <div
                              className="csel-list"
                              style={{
                                position: "absolute",
                                top: "100%",
                                left: 0,
                                right: 0,
                                marginTop: 4,
                                zIndex: 30,
                              }}
                            >
                              {webDAVDirectoryOptions.map((entry) => (
                                <button
                                  key={entry.path}
                                  type="button"
                                  className={entry.path === form.path ? "csel-opt active" : "csel-opt"}
                                  onMouseDown={(event) => event.preventDefault()}
                                  onClick={() => {
                                    setForm((current) => ({ ...current, path: entry.path, verified: false }));
                                    setWebDAVDirectoryMenuOpen(false);
                                  }}
                                >
                                  <span style={{ flex: 1 }}>{entry.name || entry.path}</span>
                                  {entry.path === form.path ? <Check size={13} /> : null}
                                </button>
                              ))}
                            </div>
                          ) : null}
                        </div>
                        <Button
                          type="button"
                          variant="secondary"
                          style={{ height: 38 }}
                          onClick={loadWebDAVDirectories}
                          disabled={
                            webDAVDirectoryLoading || !form.endpoint || !form.accessKey || (!form.secretKey && !editTarget)
                          }
                        >
                          <RefreshCw size={14} />
                          {webDAVDirectoryLoading ? "获取中..." : "获取目录"}
                        </Button>
                      </div>
                    </>
                  ) : null}

                  {form.type === "google_drive" || form.type === "one_drive" ? (
                    <>
                      {form.type === "one_drive" ? (
                        <>
                          <div style={{ display: "flex", gap: 10, alignItems: "flex-end" }}>
                            <div style={{ flex: 1 }}>
                              <SelectField
                                label="Microsoft Graph 端点"
                                value={form.endpoint ?? "https://graph.microsoft.com/v1.0"}
                                onChange={(endpoint) => setForm({ ...form, endpoint, verified: false })}
                              >
                                <option value="https://graph.microsoft.com/v1.0">公有云（国际版）</option>
                                <option value="https://microsoftgraph.chinacloudapi.cn/v1.0">世纪互联（中国区）</option>
                              </SelectField>
                            </div>
                            <Button
                              type="button"
                              variant="secondary"
                              style={{ height: 38, marginBottom: 14, whiteSpace: "nowrap" }}
                              onClick={() => openStorageDoc("onedrive", editTarget?.id)}
                            >
                              <BookOpen size={14} />
                              接入说明文档
                            </Button>
                          </div>
                          <div className="grid2">
                            <label className="field">
                              <span>{requiredLabel("应用程序（客户端）ID")}</span>
                              <input
                                required
                                value={form.accessKey}
                                onChange={(event) =>
                                  setForm({ ...form, accessKey: event.target.value, verified: false })
                                }
                                placeholder="来自 Microsoft Entra ID 应用注册"
                              />
                            </label>
                            <label className="field">
                              <span>{editTarget ? "客户端密钥" : requiredLabel("客户端密钥")}</span>
                              <input
                                type="password"
                                required={!editTarget}
                                value={form.secretKey}
                                onChange={(event) =>
                                  setForm({ ...form, secretKey: event.target.value, verified: false })
                                }
                                placeholder={editTarget ? "留空则保持当前密钥" : "来自证书和密码"}
                              />
                            </label>
                          </div>
                        </>
                      ) : (
                        <>
                          <div style={{ display: "flex", gap: 10, alignItems: "flex-end" }}>
                            <div style={{ flex: 1 }}>
                              <SelectField
                                label="授权方式"
                                value={form.authMode ?? "oauth"}
                                onChange={(authMode) => setForm({ ...form, authMode, verified: false })}
                              >
                                <option value="oauth">OAuth 授权</option>
                                <option value="service_account">服务账号</option>
                              </SelectField>
                            </div>
                            <Button
                              type="button"
                              variant="secondary"
                              style={{ height: 38, marginBottom: 14, whiteSpace: "nowrap" }}
                              onClick={() => openStorageDoc("google_drive", editTarget?.id, form.authMode)}
                            >
                              <BookOpen size={14} />
                              接入说明文档
                            </Button>
                          </div>
                          {(form.authMode ?? "oauth") === "service_account" ? (
                            <label className="field">
                              <span>{editTarget ? "服务账号 JSON 密钥" : requiredLabel("服务账号 JSON 密钥")}</span>
                              <textarea
                                rows={5}
                                required={!editTarget}
                                value={form.secretKey}
                                onChange={(event) =>
                                  setForm({ ...form, secretKey: event.target.value, verified: false })
                                }
                                placeholder={
                                  editTarget
                                    ? "留空则保持当前服务账号密钥"
                                    : '粘贴 Google Cloud 服务账号 JSON 密钥（包含 client_email 和 private_key），并将目标 Google Drive 文件夹共享给该服务账号邮箱'
                                }
                              />
                            </label>
                          ) : (
                            <>
                              <div className="grid2">
                                <label className="field">
                                  <span>{requiredLabel("客户端 ID")}</span>
                                  <input
                                    required
                                    value={form.accessKey}
                                    onChange={(event) =>
                                      setForm({ ...form, accessKey: event.target.value, verified: false })
                                    }
                                    placeholder="来自 Google Cloud OAuth 2.0 客户端"
                                  />
                                </label>
                                <label className="field">
                                  <span>{editTarget ? "客户端密钥" : requiredLabel("客户端密钥")}</span>
                                  <input
                                    type="password"
                                    required={!editTarget}
                                    value={form.secretKey}
                                    onChange={(event) =>
                                      setForm({ ...form, secretKey: event.target.value, verified: false })
                                    }
                                    placeholder={editTarget ? "留空则保持当前密钥" : "来自 Google Cloud 客户端密钥"}
                                  />
                                </label>
                              </div>
                            </>
                          )}
                        </>
                      )}
                      <label className="field">
                        <span>默认目录</span>
                        <input
                          value={form.path}
                          onChange={(event) => setForm({ ...form, path: event.target.value })}
                          placeholder="DataBridge Backups"
                        />
                      </label>
                    </>
                  ) : null}

                  {form.type === "local" ? (
                    <label className="field">
                      <span>{requiredLabel("本地目录")}</span>
                      <input
                        required
                        value={form.path}
                        onChange={(event) => setForm({ ...form, path: event.target.value })}
                        placeholder="/secure/backups"
                      />
                    </label>
                  ) : null}

                  <label className="field">
                    <span>备注</span>
                    <input
                      value={form.note}
                      onChange={(event) => setForm({ ...form, note: event.target.value })}
                      placeholder="可选，记录用途、保留策略或负责人"
                    />
                  </label>
                </div>

                <div className="modal-f">
                  <Button type="button" variant="secondary" onClick={() => setShowForm(false)}>
                    取消
                  </Button>
                  {form.type === "one_drive" ||
                  (form.type === "google_drive" && (form.authMode ?? "oauth") !== "service_account") ? (
                    <Button
                      type="button"
                      variant="secondary"
                      disabled={(editTarget ? authorizing === editTarget.id : false) || submitting}
                      onClick={() => {
                        if (editTarget) {
                          void (form.type === "one_drive"
                            ? authorizeOneDrive(editTarget)
                            : authorizeGoogleDrive(editTarget));
                        } else {
                          void saveAndAuthorize();
                        }
                      }}
                    >
                      <KeyRound size={14} />
                      {editTarget
                        ? authorizing === editTarget.id
                          ? "跳转授权中..."
                          : "开始授权"
                        : submitting
                          ? "保存并跳转中..."
                          : "保存并授权"}
                    </Button>
                  ) : null}
                  <Button type="submit" disabled={submitting}>
                    {submitting ? "正在保存..." : "保存存储"}
                  </Button>
                </div>
              </form>
            </div>,
            document.body,
          )
        : null}

      {deleteTarget ? (
        <ConfirmDialog
          title="删除远端存储"
          description="这是高危操作，删除后备份任务将不能再使用这条存储配置。"
          tone="danger"
          confirmText="删除存储"
          submitting={deleting}
          onCancel={() => setDeleteTarget(null)}
          onConfirm={confirmDeleteStorage}
          details={[
            `存储：${deleteTarget.name}`,
            "已经上传到远端的历史对象不会被自动删除，但 DataBridge 中的配置和凭证会被移除。",
          ]}
        />
      ) : null}
    </div>
  );
}

function openStorageDoc(type: "google_drive" | "onedrive", storageId?: string, authMode?: string) {
  const basePath = type === "onedrive" ? "/docs/onedrive" : "/docs/google-drive";
  const params = new URLSearchParams();
  if (storageId) {
    params.set("storageId", storageId);
  }
  if (type === "google_drive" && authMode === "service_account") {
    params.set("tab", "service_account");
  }
  const query = params.toString();
  window.open(query ? `${basePath}?${query}` : basePath, "_blank", "noopener,noreferrer");
}

function normalizeForm(form: StorageForm): RemoteStorageInput {
  return { ...form, name: form.name.trim(), provider: form.provider.trim() };
}

function webDAVOptionFromPath(value: string): WebDAVEntry {
  const path = value.replace(/^\/+|\/+$/g, "");
  return {
    name: path.split("/").filter(Boolean).pop() || path,
    path,
    isDirectory: true,
  };
}

function shouldAutoTestStorage(storage: RemoteStorage) {
  return storage.type === "object" || storage.type === "webdav";
}

function GoogleDriveLogo({ size = 18 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 87.3 78" aria-hidden="true">
      <path d="m6.6 66.85 3.85 6.65c.8 1.4 1.95 2.5 3.3 3.3l13.75-23.8h-27.5c0 1.55.4 3.1 1.2 4.5z" fill="#0066da" />
      <path d="m43.65 25-13.75-23.8c-1.35.8-2.5 1.9-3.3 3.3l-25.4 44a9.06 9.06 0 0 0-1.2 4.5h27.5z" fill="#00ac47" />
      <path d="m73.55 76.8c1.35-.8 2.5-1.9 3.3-3.3l1.6-2.75 7.65-13.25c.8-1.4 1.2-2.95 1.2-4.5h-27.502l5.852 11.5z" fill="#ea4335" />
      <path d="m43.65 25 13.75-23.8c-1.35-.8-2.9-1.2-4.5-1.2h-18.5c-1.6 0-3.15.45-4.5 1.2z" fill="#00832d" />
      <path d="m59.8 53h-32.3l-13.75 23.8c1.35.8 2.9 1.2 4.5 1.2h50.8c1.6 0 3.15-.45 4.5-1.2z" fill="#2684fc" />
      <path d="m73.4 26.5-12.7-22c-.8-1.4-1.95-2.5-3.3-3.3l-13.75 23.8 16.15 28h27.45c0-1.55-.4-3.1-1.2-4.5z" fill="#ffba00" />
    </svg>
  );
}

function OneDriveLogo({ size = 24 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0.8 6 23.4 13.2" aria-hidden="true">
      <path
        d="M10.1 8.5a5.6 5.6 0 0 1 9.6 2.3l-3.9 1.7-5.7-4Z"
        fill="#0364b8"
      />
      <path
        d="M5.7 10.6a4.4 4.4 0 0 1 4.4-2.1l5.7 4-6.7 6H5.9A4.4 4.4 0 0 1 5.7 10.6Z"
        fill="#0078d4"
      />
      <path
        d="M15.8 12.5 19.7 10.8a3.9 3.9 0 0 1 2.5 6.8l-6.4-5.1Z"
        fill="#1490df"
      />
      <path
        d="M9.1 18.5h9.6a3.9 3.9 0 0 0 3.5-2.2l-6.4-3.8-6.7 6Z"
        fill="#28a8ea"
      />
    </svg>
  );
}

function ObjectStorageLogo({ size = 24 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" aria-hidden="true">
      <path
        d="M5 7.2 7.2 19.2c.2 1.1 2.3 2 4.8 2s4.6-.9 4.8-2L19 7.2H5Z"
        fill="#ea580c"
      />
      <path
        d="M12 7.2H5l2.2 12c.2 1.1 2.3 2 4.8 2V7.2Z"
        fill="#f97316"
      />
      <ellipse cx="12" cy="7.2" rx="7" ry="2.6" fill="#fdba74" />
      <ellipse cx="12" cy="7.2" rx="5.2" ry="1.6" fill="#c2410c" />
      <path
        d="M6.8 6.4c.5-2.1 2.6-3.6 5.2-3.6s4.7 1.5 5.2 3.6"
        fill="none"
        stroke="#9a3412"
        strokeWidth="1.5"
        strokeLinecap="round"
      />
    </svg>
  );
}

function WebDAVLogo({ size = 24 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" aria-hidden="true">
      <path
        d="M3.2 6.8A2.2 2.2 0 0 1 5.4 4.6h4.1l1.8 2h7.3a2.2 2.2 0 0 1 2.2 2.2v1.6H3.2V6.8Z"
        fill="#4338ca"
      />
      <rect x="2.5" y="8.4" width="19" height="11.2" rx="2.4" fill="#6366f1" />
      <path
        d="M2.5 14.5h19v2.7a2.4 2.4 0 0 1-2.4 2.4H4.9a2.4 2.4 0 0 1-2.4-2.4v-2.7Z"
        fill="#4f46e5"
      />
      <circle cx="8" cy="14" r="1.4" fill="#e0e7ff" />
      <circle cx="12" cy="14" r="1.4" fill="#e0e7ff" />
      <circle cx="16" cy="14" r="1.4" fill="#e0e7ff" />
      <path d="M9.4 14h1.2M13.4 14h1.2" stroke="#c7d2fe" strokeWidth="1.3" strokeLinecap="round" />
    </svg>
  );
}

function LocalStorageLogo({ size = 24 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" aria-hidden="true">
      <path
        d="M3.2 6.6A2.2 2.2 0 0 1 5.4 4.4h4.2l1.8 2h7.2a2.2 2.2 0 0 1 2.2 2.2v2.2H3.2V6.6Z"
        fill="#0f766e"
      />
      <rect x="2.5" y="8.4" width="19" height="11.2" rx="2.4" fill="#14b8a6" />
      <rect x="5" y="13.8" width="14" height="3.4" rx="1.2" fill="#0f766e" />
      <circle cx="16.4" cy="15.5" r="1" fill="#5eead4" />
      <rect x="6.6" y="15" width="5.4" height="1" rx="0.5" fill="#99f6e4" />
    </svg>
  );
}

function StorageIcon({ type }: { type: RemoteStorageType }) {
  if (type === "local") return <LocalStorageLogo size={24} />;
  if (type === "object") return <ObjectStorageLogo size={24} />;
  if (type === "webdav") return <WebDAVLogo size={24} />;
  if (type === "google_drive") return <GoogleDriveLogo size={24} />;
  if (type === "one_drive") return <OneDriveLogo size={24} />;
  return <KeyRound size={22} />;
}

function Summary({
  title,
  value,
  detail,
  tone = "gray",
}: {
  title: string;
  value: number;
  detail: string;
  tone?: "gray" | "blue" | "green";
}) {
  const accent =
    tone === "blue" ? "var(--blue)" : tone === "green" ? "var(--ok)" : "var(--ink)";
  return (
    <Card className="pad" style={{ display: "grid", gap: 4 }}>
      <span style={{ fontSize: 12, fontWeight: 600, color: "var(--muted)" }}>{title}</span>
      <strong className="num" style={{ fontSize: 24, fontWeight: 700, color: accent, letterSpacing: "-0.02em" }}>
        {value}
      </strong>
      <small style={{ fontSize: 12, color: "var(--muted)" }}>{detail}</small>
    </Card>
  );
}

function defaultProvider(type: RemoteStorageType) {
  if (type === "webdav") return "WebDAV";
  if (type === "google_drive") return "Google Drive";
  if (type === "one_drive") return "Microsoft OneDrive";
  if (type === "local") return "Local Filesystem";
  return "S3 Compatible";
}

function defaultAuthMode(type: RemoteStorageType) {
  return type === "object" ? "access_key" : type === "webdav" ? "basic" : type === "local" ? "filesystem" : "oauth";
}

function typeLabel(type: RemoteStorageType) {
  if (type === "object") return "对象存储";
  if (type === "webdav") return "WebDAV";
  if (type === "google_drive") return "Google Drive";
  if (type === "one_drive") return "OneDrive";
  return "本地目录";
}

function storageKindFooter(type: RemoteStorageType) {
  if (type === "object") return "对象存储配置";
  if (type === "webdav") return "WebDAV 配置";
  if (type === "local") return "本地目录配置";
  return "云盘授权配置";
}

function statusLabel(status: RemoteStorage["status"]) {
  if (status === "connected") return "可用";
  if (status === "needs_auth") return "待授权";
  if (status === "warning") return "需检查";
  return "未测试";
}

function statusTone(status: RemoteStorage["status"]) {
  if (status === "connected") return "green";
  if (status === "needs_auth") return "orange";
  if (status === "warning") return "red";
  return "gray";
}

function formatWebDAVTime(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  });
}
