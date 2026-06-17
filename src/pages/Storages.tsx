import { Check, Cloud, Edit3, FolderOpen, HardDrive, KeyRound, MoreHorizontal, Plus, RefreshCw, Trash2, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { api } from "../api";
import type { RemoteStorage, RemoteStorageInput, RemoteStorageType } from "../types";
import { Badge, Button, Card, SelectField } from "../components/UI";

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
  const [bucketTarget, setBucketTarget] = useState<RemoteStorage | null>(null);
  const [bucketListTarget, setBucketListTarget] = useState<RemoteStorage | null>(null);
  const [bucketName, setBucketName] = useState("");
  const [creatingBucket, setCreatingBucket] = useState(false);
  const [viewingBuckets, setViewingBuckets] = useState(false);
  const [viewBucketOptions, setViewBucketOptions] = useState<string[]>([]);
  const [bucketLoading, setBucketLoading] = useState(false);
  const [bucketOptions, setBucketOptions] = useState<string[]>([]);
  const [bucketMenuOpen, setBucketMenuOpen] = useState(false);
  const [pageError, setPageError] = useState("");
  const [formError, setFormError] = useState("");
  const bucketPickerRef = useRef<HTMLDivElement | null>(null);
  const summary = useMemo(() => ({
    total: storages.length,
    object: storages.filter((item) => item.type === "object").length,
    cloudDrive: storages.filter((item) => item.type === "google_drive" || item.type === "one_drive").length,
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

  function openCreate() {
    setEditTarget(null);
    setForm(emptyForm);
    setBucketOptions([]);
    setBucketMenuOpen(false);
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
    setFormError("");
    setShowForm(true);
  }

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setSubmitting(true);
    setFormError("");
    try {
      const input = normalizeForm(form);
      if (editTarget) {
        const updated = await api.updateStorage(editTarget.id, input);
        onUpdate(updated);
      } else {
        const created = await api.createStorage(input);
        onAdd(created);
      }
      setShowForm(false);
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

  async function remove(storage: RemoteStorage) {
    setOpenMenu(null);
    if (!window.confirm(`删除远端存储“${storage.name}”？`)) return;
    await api.deleteStorage(storage.id);
    onDelete(storage.id);
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

  function changeType(type: RemoteStorageType) {
    setForm((current) => ({
      ...current,
      type,
      provider: defaultProvider(type),
      authMode: defaultAuthMode(type),
    }));
    setBucketOptions([]);
    setBucketMenuOpen(false);
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
        <Summary title="网盘授权" value={summary.cloudDrive} detail="Google Drive / OneDrive" tone="green" />
      </div>
      <div className="storage-grid">
        {storages.map((storage) => (
          <Card className="storage-card" key={storage.id}>
            <div className="storage-card-head">
              <span className={`storage-icon ${storage.type}`}><StorageIcon type={storage.type} /></span>
              <div>
                <strong>{storage.name}</strong>
                <span>{typeLabel(storage.type)} · {storage.provider}</span>
              </div>
              <div className="connection-actions" onClick={(event) => event.stopPropagation()}>
                <button type="button" className="plain-icon action-trigger" aria-label={`${storage.name} 更多操作`} onClick={() => setOpenMenu(openMenu === storage.id ? null : storage.id)}><MoreHorizontal size={18} /></button>
                {openMenu === storage.id && (
                  <div className="connection-action-menu">
                    <button type="button" onClick={() => openEdit(storage)}><Edit3 size={15} /><span>编辑配置</span></button>
                    {storage.type === "object" && <button type="button" onClick={() => openBucketCreate(storage)}><Plus size={15} /><span>创建 Bucket</span></button>}
                    {storage.type === "object" && <button type="button" onClick={() => openBucketList(storage)}><FolderOpen size={15} /><span>查看 Bucket</span></button>}
                    <button type="button" onClick={() => test(storage)} disabled={testing === storage.id}>{testing === storage.id ? <RefreshCw size={15} className="spin" /> : <RefreshCw size={15} />}<span>测试配置</span></button>
                    <button type="button" className="danger-action" onClick={() => remove(storage)}><Trash2 size={15} /><span>删除存储</span></button>
                  </div>
                )}
              </div>
            </div>
            <dl>
              <div><dt>状态</dt><dd><Badge tone={statusTone(storage.status)}>{statusLabel(storage.status)}</Badge></dd></div>
              <div><dt>Endpoint</dt><dd>{storage.endpoint || "未设置"}</dd></div>
              <div><dt>Bucket / 路径</dt><dd>{storage.bucket || storage.path || "未设置"}</dd></div>
              <div><dt>备注</dt><dd>{storage.note || "未填写"}</dd></div>
            </dl>
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

      {showForm && (
        <div className="modal-backdrop" onMouseDown={() => setShowForm(false)}>
          <form className="modal storage-modal" onSubmit={submit} onMouseDown={(event) => event.stopPropagation()}>
            <div className="modal-head"><div><h3>{editTarget ? "编辑远端存储" : "新增远端存储"}</h3><p>密钥会加密保存，不会返回到浏览器。</p></div><button type="button" className="plain-icon" onClick={() => setShowForm(false)}><X /></button></div>
            {formError && <div className="form-error left modal-error">{formError}</div>}
            <SelectField label="存储类型" value={form.type} onChange={(value) => changeType(value as RemoteStorageType)}>
              <option value="object">对象存储</option>
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
            {(form.type === "google_drive" || form.type === "one_drive") && (
              <>
                <SelectField label="授权方式" value={form.authMode ?? "oauth"} onChange={(authMode) => setForm({ ...form, authMode })}><option value="oauth">OAuth 授权</option><option value="service_account">服务账号</option></SelectField>
                <label className="field"><span>默认目录</span><input value={form.path} onChange={(event) => setForm({ ...form, path: event.target.value })} placeholder="DataBridge Backups" /></label>
                <p className="field-hint">当前先保存授权目标配置，真实登录授权和文件上传会在下一步接入。</p>
              </>
            )}
            {form.type === "local" && <label className="field"><span>本地目录</span><input required value={form.path} onChange={(event) => setForm({ ...form, path: event.target.value })} placeholder="/secure/backups" /></label>}
            <label className="field"><span>备注</span><input value={form.note} onChange={(event) => setForm({ ...form, note: event.target.value })} placeholder="可选，记录用途、保留策略或负责人" /></label>
            <div className="modal-actions"><Button type="button" variant="secondary" onClick={() => setShowForm(false)}>取消</Button><Button type="submit" disabled={submitting}>{submitting ? "正在保存..." : "保存存储"}</Button></div>
          </form>
        </div>
      )}
    </div>
  );
}

function normalizeForm(form: StorageForm): RemoteStorageInput {
  return { ...form, name: form.name.trim(), provider: form.provider.trim() };
}

function StorageIcon({ type }: { type: RemoteStorageType }) {
  if (type === "local") return <HardDrive size={18} />;
  if (type === "object") return <Cloud size={18} />;
  return <KeyRound size={18} />;
}

function Summary({ title, value, detail, tone = "gray" }: { title: string; value: number; detail: string; tone?: "gray" | "blue" | "green" }) {
  return <Card className={`task-summary-card ${tone}`}><span>{title}</span><strong>{value}</strong><small>{detail}</small></Card>;
}

function defaultProvider(type: RemoteStorageType) {
  if (type === "google_drive") return "Google Drive";
  if (type === "one_drive") return "Microsoft OneDrive";
  if (type === "local") return "Local Filesystem";
  return "S3 Compatible";
}

function defaultAuthMode(type: RemoteStorageType) {
  return type === "object" ? "access_key" : type === "local" ? "filesystem" : "oauth";
}

function typeLabel(type: RemoteStorageType) {
  if (type === "object") return "对象存储";
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
