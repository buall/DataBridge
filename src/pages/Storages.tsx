import { Check, Cloud, Edit3, FolderOpen, HardDrive, KeyRound, MoreHorizontal, Plus, RefreshCw, Trash2, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { api, type WebDAVEntry } from "../api";
import type { RemoteStorage, RemoteStorageInput, RemoteStorageType } from "../types";
import { Badge, Button, Card, ConfirmDialog, SelectField } from "../components/UI";

type StorageForm = RemoteStorageInput & { secretKey: string };

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
}: {
  storages: RemoteStorage[];
  onAdd: (storage: RemoteStorage) => void;
  onUpdate: (storage: RemoteStorage) => void;
  onDelete: (storageId: string) => void;
}) {
  const [showForm, setShowForm] = useState(false);
  const [form, setForm] = useState<StorageForm>(emptyForm);
  const [editTarget, setEditTarget] = useState<RemoteStorage | null>(null);
  const [openMenu, setOpenMenu] = useState<string | null>(null);
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
  const [formError, setFormError] = useState("");
  const bucketPickerRef = useRef<HTMLDivElement | null>(null);
  const webDAVDirectoryPickerRef = useRef<HTMLDivElement | null>(null);
  const summary = useMemo(() => ({
    total: storages.length,
    object: storages.filter((item) => item.type === "object").length,
    cloudDrive: storages.filter((item) => item.type === "webdav" || item.type === "google_drive" || item.type === "one_drive").length,
  }), [storages]);
  useEffect(() => {
    if (!openMenu) return;
    const close = () => setOpenMenu(null);
    window.addEventListener("click", close);
    return () => window.removeEventListener("click", close);
  }, [openMenu]);

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
    setOpenMenu(null);
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
          setPageError(testReason instanceof Error ? `${saved.name}：${testReason.message}` : `${saved.name}：测试远端存储失败`);
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
    setOpenMenu(null);
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
    setOpenMenu(null);
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

  async function remove(storage: RemoteStorage) {
    setOpenMenu(null);
    setDeleteTarget(storage);
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
    setOpenMenu(null);
    setBucketTarget(storage);
    setBucketName("");
    setFormError("");
    setPageError("");
  }

  async function openBucketList(storage: RemoteStorage) {
    setOpenMenu(null);
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
    setOpenMenu(null);
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
      endpoint: type === "one_drive" ? "https://graph.microsoft.com/v1.0" : type === "object" || type === "webdav" ? current.endpoint : "",
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

  return (
    <div className="page-stack">
      <div className="page-heading">
        <div><h2>远端存储</h2><p>集中管理数据库备份可同步的存储目标。</p></div>
        <Button onClick={openCreate}><Plus size={17} />新增存储</Button>
      </div>
      {pageError && <div className="error-banner">{pageError}</div>}
      <div className="task-summary-grid">
        <Summary title="全部存储" value={summary.total} detail="配置记录已保存" />
        <Summary title="对象存储" value={summary.object} detail="S3 兼容、云厂商 OSS" tone="blue" />
        <Summary title="网盘存储" value={summary.cloudDrive} detail="WebDAV / 云盘授权" tone="green" />
      </div>
      <div className="storage-grid">
        {storages.map((storage) => (
          <Card className="storage-card" key={storage.id}>
            <div className="storage-card-head">
              <span className={`storage-icon ${storage.type}`}><StorageIcon type={storage.type} /></span>
              <div>
                <strong>{storage.name}</strong>
                <span className="storage-card-subtitle">{typeLabel(storage.type)} · {storage.provider}</span>
              </div>
              <div className="connection-actions" onClick={(event) => event.stopPropagation()}>
                <button type="button" className="plain-icon action-trigger" aria-label={`${storage.name} 更多操作`} onClick={() => setOpenMenu(openMenu === storage.id ? null : storage.id)}><MoreHorizontal size={18} /></button>
                {openMenu === storage.id && (
                  <div className="connection-action-menu">
                    <button type="button" onClick={() => openEdit(storage)}><Edit3 size={15} /><span>编辑配置</span></button>
                    {storage.type === "object" && <button type="button" onClick={() => openBucketCreate(storage)}><Plus size={15} /><span>创建 Bucket</span></button>}
                    {storage.type === "object" && <button type="button" onClick={() => openBucketList(storage)}><FolderOpen size={15} /><span>查看 Bucket</span></button>}
                    {storage.type === "webdav" && <button type="button" onClick={() => openWebDAVDirectory(storage)}><FolderOpen size={15} /><span>查看目录</span></button>}
                    {storage.type === "one_drive" && <button type="button" onClick={() => authorizeOneDrive(storage)} disabled={authorizing === storage.id}><KeyRound size={15} /><span>{authorizing === storage.id ? "跳转授权中" : "开始授权"}</span></button>}
                    <button type="button" onClick={() => test(storage)} disabled={testing === storage.id}>{testing === storage.id ? <RefreshCw size={15} className="spin" /> : <RefreshCw size={15} />}<span>测试配置</span></button>
                    <button type="button" className="danger-action" onClick={() => remove(storage)}><Trash2 size={15} /><span>删除存储</span></button>
                  </div>
                )}
              </div>
            </div>
            <dl>
              <div><dt>Endpoint</dt><dd>{storage.endpoint || "未设置"}</dd></div>
              <div><dt>Bucket / 路径</dt><dd>{storage.bucket || storage.path || "未设置"}</dd></div>
              <div><dt>备注</dt><dd>{storage.note || "未填写"}</dd></div>
            </dl>
            <div className="storage-card-foot">
              <Badge tone={statusTone(storage.status)}>{statusLabel(storage.status)}</Badge>
              <span>{storage.type === "object" ? "对象存储配置" : storage.type === "webdav" ? "WebDAV 配置" : storage.type === "local" ? "本地目录配置" : "云盘授权配置"}</span>
            </div>
          </Card>
        ))}
      </div>
      {!storages.length && <div className="inline-empty"><Cloud size={22} /><div><strong>暂无远端存储</strong><span>新增对象存储或云盘授权后，备份任务就能选择同步目标。</span></div></div>}

      {bucketTarget && (
        <div className="modal-backdrop" onMouseDown={() => setBucketTarget(null)}>
          <form className="modal bucket-create-modal" onSubmit={createBucket} onMouseDown={(event) => event.stopPropagation()}>
            <div className="modal-head"><div><h3>创建 Bucket</h3><p>{bucketTarget.name} · {bucketTarget.endpoint}</p></div><button type="button" className="plain-icon" onClick={() => setBucketTarget(null)}><X /></button></div>
            <label className="field"><span>Bucket 名称</span><input required autoFocus value={bucketName} onChange={(event) => setBucketName(event.target.value)} placeholder="例如 databridge-backups" /></label>
            <div className="modal-actions"><Button type="button" variant="secondary" onClick={() => setBucketTarget(null)}>取消</Button><Button type="submit" disabled={creatingBucket}>{creatingBucket ? "正在创建..." : "创建 Bucket"}</Button></div>
          </form>
        </div>
      )}

      {bucketListTarget && (
        <div className="modal-backdrop" onMouseDown={() => setBucketListTarget(null)}>
          <div className="modal bucket-list-modal" onMouseDown={(event) => event.stopPropagation()}>
            <div className="modal-head"><div><h3>Bucket 列表</h3><p>{bucketListTarget.name} · {bucketListTarget.endpoint}</p></div><button type="button" className="plain-icon" onClick={() => setBucketListTarget(null)}><X /></button></div>
            {viewingBuckets ? (
              <div className="loading-copy">正在获取 Bucket 列表...</div>
            ) : viewBucketOptions.length ? (
              <div className="bucket-list">
                {viewBucketOptions.map((bucket) => <div key={bucket} className={bucket === bucketListTarget.bucket ? "active" : ""}><span>{bucket}</span>{bucket === bucketListTarget.bucket && <Badge tone="green">当前</Badge>}</div>)}
              </div>
            ) : (
              <div className="inline-empty"><Cloud size={20} /><div><strong>暂无 Bucket</strong><span>可以通过“创建 Bucket”快速新增一个。</span></div></div>
            )}
            <div className="modal-actions"><Button type="button" variant="secondary" onClick={() => setBucketListTarget(null)}>关闭</Button><Button type="button" onClick={() => bucketListTarget && openBucketCreate(bucketListTarget)}><Plus size={16} />创建 Bucket</Button></div>
          </div>
        </div>
      )}

      {webDAVTarget && (
        <div className="modal-backdrop" onMouseDown={() => setWebDAVTarget(null)}>
          <div className="modal bucket-list-modal" onMouseDown={(event) => event.stopPropagation()}>
            <div className="modal-head"><div><h3>WebDAV 目录</h3><p>{webDAVTarget.name} · /{webDAVPath || webDAVTarget.path || ""}</p></div><button type="button" className="plain-icon" onClick={() => setWebDAVTarget(null)}><X /></button></div>
            <form className="webdav-directory-toolbar" onSubmit={createWebDAVDirectory}>
              <label className="field grow"><span>新目录名称</span><input value={webDAVDirectoryName} onChange={(event) => setWebDAVDirectoryName(event.target.value)} placeholder="例如 2026-archive" /></label>
              <Button type="submit" disabled={creatingWebDAVDirectory || !webDAVDirectoryName.trim()}>{creatingWebDAVDirectory ? "创建中..." : "创建目录"}</Button>
              <Button type="button" variant="secondary" disabled={viewingWebDAV} onClick={() => loadWebDAVDirectory()}>{viewingWebDAV ? <RefreshCw size={15} className="spin" /> : <RefreshCw size={15} />}{viewingWebDAV ? "刷新中" : "刷新"}</Button>
            </form>
            {viewingWebDAV ? (
              <div className="loading-copy">正在读取 WebDAV 目录...</div>
            ) : webDAVEntries.length ? (
              <div className="bucket-list">
                {webDAVEntries.map((entry) => <div key={entry.path}><span>{entry.name}</span><small>文件夹{entry.modified ? ` · ${formatWebDAVTime(entry.modified)}` : ""}</small></div>)}
              </div>
            ) : (
              <div className="inline-empty"><FolderOpen size={20} /><div><strong>暂无子目录</strong><span>可以创建目录，备份文件不会在这里展开显示。</span></div></div>
            )}
            <div className="modal-actions"><Button type="button" variant="secondary" onClick={() => setWebDAVTarget(null)}>关闭</Button></div>
          </div>
        </div>
      )}

      {showForm && (
        <div className="modal-backdrop" onMouseDown={() => setShowForm(false)}>
          <form className="modal storage-modal" onSubmit={submit} onMouseDown={(event) => event.stopPropagation()}>
            <div className="modal-head"><div><h3>{editTarget ? "编辑远端存储" : "新增远端存储"}</h3><p>密钥会加密保存，不会返回到浏览器。</p></div><button type="button" className="plain-icon" onClick={() => setShowForm(false)}><X /></button></div>
            {formError && <div className="form-error left modal-error">{formError}</div>}
            <SelectField label="存储类型" value={form.type} onChange={(value) => changeType(value as RemoteStorageType)}>
              <option value="object">对象存储</option>
              <option value="webdav">WebDAV 网盘</option>
              <option value="google_drive">Google Drive</option>
              <option value="one_drive">OneDrive</option>
              <option value="local">本地目录</option>
            </SelectField>
            <label className="field"><span>名称</span><input required value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} placeholder="生产备份 OSS" /></label>
            <label className="field"><span>服务商</span><input required value={form.provider} onChange={(event) => setForm({ ...form, provider: event.target.value })} placeholder="AWS S3 / Google Drive / OneDrive" /></label>
            {form.type === "object" && (
              <>
                <label className="field"><span>Endpoint</span><input required value={form.endpoint} onChange={(event) => setForm({ ...form, endpoint: event.target.value, verified: false })} placeholder="https://s3.amazonaws.com" /></label>
                <label className="field"><span>Access Key</span><input required value={form.accessKey} onChange={(event) => setForm({ ...form, accessKey: event.target.value, verified: false })} /></label>
                <label className="field"><span>Secret Key</span><input type="password" required={!editTarget} value={form.secretKey} onChange={(event) => setForm({ ...form, secretKey: event.target.value, verified: false })} placeholder={editTarget ? "留空则保持当前密钥" : ""} /></label>
                <div className="bucket-picker" ref={bucketPickerRef}>
                  <label className="field grow bucket-combobox">
                    <span>Bucket</span>
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
                    {bucketMenuOpen && bucketOptions.length > 0 && (
                      <div className="bucket-menu">
                        {bucketOptions.map((bucket) => (
                          <button
                            key={bucket}
                            type="button"
                            className={`bucket-option ${bucket === form.bucket ? "active" : ""}`}
                            onMouseDown={(event) => event.preventDefault()}
                            onClick={() => {
                              setForm((current) => ({ ...current, bucket, verified: true }));
                              setBucketMenuOpen(false);
                            }}
                          >
                            {bucket}
                          </button>
                        ))}
                      </div>
                    )}
                  </label>
                  <Button type="button" variant="secondary" onClick={loadBuckets} disabled={bucketLoading || !form.endpoint || !form.accessKey || (!form.secretKey && !editTarget)}>{bucketLoading ? <RefreshCw size={15} className="spin" /> : <RefreshCw size={15} />}{bucketLoading ? "获取中" : "获取 Bucket"}</Button>
                </div>
                <label className="field"><span>Region</span><input value={form.region} onChange={(event) => setForm({ ...form, region: event.target.value })} placeholder="可选，例如 us-east-1" /></label>
                <label className="field"><span>默认目录</span><input value={form.path} onChange={(event) => setForm({ ...form, path: event.target.value })} placeholder="databridge/backups" /></label>
              </>
            )}
            {form.type === "webdav" && (
              <>
                <label className="field"><span>Endpoint</span><input required value={form.endpoint} onChange={(event) => setForm({ ...form, endpoint: event.target.value, verified: false })} placeholder="https://dav.jianguoyun.com/dav" /></label>
                <label className="field"><span>用户名</span><input required value={form.accessKey} onChange={(event) => setForm({ ...form, accessKey: event.target.value, verified: false })} placeholder="WebDAV 账号或邮箱" /></label>
                <label className="field"><span>密码 / 应用密码</span><input type="password" required={!editTarget} value={form.secretKey} onChange={(event) => setForm({ ...form, secretKey: event.target.value, verified: false })} placeholder={editTarget ? "留空则保持当前密码" : "建议使用应用专用密码"} /></label>
                <div className="bucket-picker" ref={webDAVDirectoryPickerRef}>
                  <label className="field grow bucket-combobox">
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
                    {webDAVDirectoryMenuOpen && webDAVDirectoryOptions.length > 0 && (
                      <div className="bucket-menu">
                        {webDAVDirectoryOptions.map((entry) => (
                          <button
                            key={entry.path}
                            type="button"
                            className={`bucket-option ${entry.path === form.path ? "active" : ""}`}
                            onMouseDown={(event) => event.preventDefault()}
                            onClick={() => {
                              setForm((current) => ({ ...current, path: entry.path, verified: false }));
                              setWebDAVDirectoryMenuOpen(false);
                            }}
                          >
                            {entry.name || entry.path}
                          </button>
                        ))}
                      </div>
                    )}
                  </label>
                  <Button type="button" variant="secondary" onClick={loadWebDAVDirectories} disabled={webDAVDirectoryLoading || !form.endpoint || !form.accessKey || (!form.secretKey && !editTarget)}>{webDAVDirectoryLoading ? <RefreshCw size={15} className="spin" /> : <RefreshCw size={15} />}{webDAVDirectoryLoading ? "获取中" : "获取目录"}</Button>
                </div>
              </>
            )}
            {(form.type === "google_drive" || form.type === "one_drive") && (
              <>
                {form.type === "one_drive" ? (
                  <>
                    <SelectField label="Microsoft Graph 端点" value={form.endpoint ?? "https://graph.microsoft.com/v1.0"} onChange={(endpoint) => setForm({ ...form, endpoint, verified: false })}>
                      <option value="https://graph.microsoft.com/v1.0">公有云（国际版）</option>
                      <option value="https://microsoftgraph.chinacloudapi.cn/v1.0">世纪互联（中国区）</option>
                    </SelectField>
                    <label className="field"><span>应用程序（客户端）ID</span><input required value={form.accessKey} onChange={(event) => setForm({ ...form, accessKey: event.target.value, verified: false })} placeholder="来自 Microsoft Entra ID 应用注册" /></label>
                    <label className="field"><span>客户端密钥</span><input type="password" required={!editTarget} value={form.secretKey} onChange={(event) => setForm({ ...form, secretKey: event.target.value, verified: false })} placeholder={editTarget ? "留空则保持当前密钥" : "来自证书和密码"} /></label>
                    <p className="field-hint">在 Entra ID 应用中添加 Web 重定向 URI：<code>{window.location.origin}/api/storages/&lt;存储 ID&gt;/onedrive/callback</code>，并授予委托权限 Files.ReadWrite.All、offline_access。</p>
                  </>
                ) : (
                  <SelectField label="授权方式" value={form.authMode ?? "oauth"} onChange={(authMode) => setForm({ ...form, authMode })}><option value="oauth">OAuth 授权</option><option value="service_account">服务账号</option></SelectField>
                )}
                <label className="field"><span>默认目录</span><input value={form.path} onChange={(event) => setForm({ ...form, path: event.target.value })} placeholder="DataBridge Backups" /></label>
                {form.type === "google_drive" && <p className="field-hint">Google Drive 授权暂未接入。</p>}
              </>
            )}
            {form.type === "local" && <label className="field"><span>本地目录</span><input required value={form.path} onChange={(event) => setForm({ ...form, path: event.target.value })} placeholder="/secure/backups" /></label>}
            <label className="field"><span>备注</span><input value={form.note} onChange={(event) => setForm({ ...form, note: event.target.value })} placeholder="可选，记录用途、保留策略或负责人" /></label>
            <div className="modal-actions"><Button type="button" variant="secondary" onClick={() => setShowForm(false)}>取消</Button><Button type="submit" disabled={submitting}>{submitting ? "正在保存..." : "保存存储"}</Button></div>
          </form>
        </div>
      )}

      {deleteTarget && <ConfirmDialog title="删除远端存储" description="这是高危操作，删除后备份任务将不能再使用这条存储配置。" tone="danger" confirmText="删除存储" submitting={deleting} onCancel={() => setDeleteTarget(null)} onConfirm={confirmDeleteStorage} details={[`存储：${deleteTarget.name}`, "已经上传到远端的历史对象不会被自动删除，但 DataBridge 中的配置和凭证会被移除。"]} />}
    </div>
  );
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

function StorageIcon({ type }: { type: RemoteStorageType }) {
  if (type === "local") return <HardDrive size={18} />;
  if (type === "object") return <Cloud size={18} />;
  if (type === "webdav") return <FolderOpen size={18} />;
  return <KeyRound size={18} />;
}

function Summary({ title, value, detail, tone = "gray" }: { title: string; value: number; detail: string; tone?: "gray" | "blue" | "green" }) {
  return <Card className={`task-summary-card ${tone}`}><span>{title}</span><strong>{value}</strong><small>{detail}</small></Card>;
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
  return date.toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false });
}
