import { Archive, ArrowLeft, ChevronDown, Clipboard, Copy, Download, HardDrive, Trash2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { api } from "../api";
import { Badge, Button, Card, ConfirmDialog } from "../components/UI";
import type { BackupJob } from "../types";
import { artifactVersionsForRestore, backupArtifacts, formatBytes, storageTypeLabel, totalArtifactSize } from "./BackupMonitoring";

type BackupArtifactItem = ReturnType<typeof backupArtifacts>[number];

export function BackupArtifacts({
  job,
  onBack,
  onUpdated,
}: {
  job: BackupJob;
  onBack: () => void;
  onUpdated: (job: BackupJob) => void;
}) {
  const versions = artifactVersionsForRestore(job);
  const [selectedVersionId, setSelectedVersionId] = useState(versions[0]?.id ?? "current");
  const selectedVersion = versions.find((version) => version.id === selectedVersionId) ?? versions[0] ?? null;
  const artifacts = selectedVersion?.artifacts ?? backupArtifacts(job);
  const [copied, setCopied] = useState("");
  const [downloading, setDownloading] = useState("");
  const [deleteTarget, setDeleteTarget] = useState<BackupArtifactItem | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState("");
  const [versionMenuOpen, setVersionMenuOpen] = useState(false);
  const versionMenuRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!versions.length) {
      setSelectedVersionId("current");
      return;
    }
    if (!versions.some((version) => version.id === selectedVersionId)) {
      setSelectedVersionId(versions[0].id);
    }
  }, [selectedVersionId, versions]);

  useEffect(() => {
    if (!versionMenuOpen) return;
    const close = (event: MouseEvent) => {
      if (!versionMenuRef.current?.contains(event.target as Node)) setVersionMenuOpen(false);
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setVersionMenuOpen(false);
    };
    window.addEventListener("mousedown", close);
    window.addEventListener("keydown", closeOnEscape);
    return () => {
      window.removeEventListener("mousedown", close);
      window.removeEventListener("keydown", closeOnEscape);
    };
  }, [versionMenuOpen]);

  async function copyArtifactPath(artifact: BackupArtifactItem) {
    const value = artifact.path || artifact.name;
    if (!value) return;
    setError("");
    try {
      await copyText(value);
      setCopied(artifact.database);
      window.setTimeout(() => setCopied((current) => current === artifact.database ? "" : current), 1800);
    } catch {
      setError("复制失败，请手动选择路径后复制");
    }
  }

  async function downloadArtifact(artifact: BackupArtifactItem) {
    setDownloading(artifact.database);
    setError("");
    try {
      const { blob, fileName } = await api.downloadBackupArtifact(job.id, artifact.database, selectedVersion?.current ? undefined : selectedVersion?.id);
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = fileName || artifactFileName(artifact);
      document.body.appendChild(link);
      link.click();
      link.remove();
      URL.revokeObjectURL(url);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "下载备份产物失败");
    } finally {
      setDownloading("");
    }
  }

  async function confirmDeleteArtifact() {
    if (!deleteTarget) return;
    setDeleting(true);
    setError("");
    try {
      const next = await api.deleteBackupArtifact(job.id, deleteTarget.database, selectedVersion?.current ? undefined : selectedVersion?.id);
      onUpdated(next);
      setDeleteTarget(null);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "删除备份产物失败");
    } finally {
      setDeleting(false);
    }
  }

  return (
    <div className="page-stack artifact-manager-page">
      <div className="page-heading">
        <div><h2>备份产物</h2><p>{job.name} · 任务 ID {job.id}</p></div>
        <Button variant="secondary" onClick={onBack}><ArrowLeft size={16} />返回备份详情</Button>
      </div>
      {error && <div className="error-banner">{error}</div>}
      <Card className="artifact-manager-card">
        <div className="section-title compact"><div><h3>产物版本</h3><p>选择某一次备份版本后，再管理该版本下的数据库产物。</p></div><Badge tone="gray">{storageTypeLabel(job.destinationType)}</Badge></div>
        {versions.length > 1 && <div className="artifact-version-picker field select-field artifact-manager-picker" ref={versionMenuRef}><span>备份版本</span><button type="button" className="select-trigger artifact-version-trigger" aria-haspopup="listbox" aria-expanded={versionMenuOpen} onClick={() => setVersionMenuOpen((current) => !current)}><span>{selectedVersion ? `${selectedVersion.label} · ${selectedVersion.artifacts.length} 个数据库 · ${formatBytes(totalArtifactSize(selectedVersion.artifacts))}` : "选择备份版本"}</span><ChevronDown size={16} /></button>{versionMenuOpen && <div className="select-menu artifact-version-menu" role="listbox" aria-label="备份版本">{versions.map((version) => <button type="button" key={version.id} className={version.id === selectedVersion?.id ? "active" : ""} role="option" aria-selected={version.id === selectedVersion?.id} onClick={() => { setSelectedVersionId(version.id); setVersionMenuOpen(false); }}><strong>{version.label}</strong><small>{version.artifacts.length} 个数据库 · {formatBytes(totalArtifactSize(version.artifacts))}{version.current ? " · 最新产物" : ""}</small></button>)}</div>}</div>}
        <div className="artifact-manager-summary">
          <strong>{selectedVersion ? selectedVersion.label : "当前最新产物"}</strong>
          <span>{artifacts.length} 个数据库 · {formatBytes(totalArtifactSize(artifacts))}</span>
          <small>{selectedVersion?.current ? "当前展示最新一次备份版本的真实产物。" : "当前展示所选备份版本的真实产物。"}</small>
        </div>
        {artifacts.length ? (
          <div className="backup-artifact-list artifact-manager-list">
            {artifacts.map((artifact) => (
              <div className="backup-artifact-row" key={artifact.database}>
                <span className="artifact-icon">{job.destinationType === "local" ? <HardDrive size={22} /> : <Archive size={22} />}</span>
                <div className="artifact-main">
                  <div className="artifact-title"><strong>{artifact.database}</strong><span>{formatBytes(artifact.size ?? 0)}</span></div>
                  <dl>
                    <div><dt>存储类型</dt><dd>{storageTypeLabel(job.destinationType)}</dd></div>
                    <div><dt>{job.destinationType === "local" ? "本地路径" : "对象路径"}</dt><dd>{artifact.path || artifact.name}</dd></div>
                  </dl>
                </div>
                <div className="artifact-actions">
                  <button type="button" title="复制路径" onClick={() => copyArtifactPath(artifact)}>{copied === artifact.database ? <Clipboard size={15} /> : <Copy size={15} />}<span>{copied === artifact.database ? "已复制路径" : "复制路径"}</span></button>
                  <button type="button" title="下载备份产物" disabled={downloading === artifact.database} onClick={() => downloadArtifact(artifact)}><Download size={15} /><span>{downloading === artifact.database ? "下载中" : "下载"}</span></button>
                  <button type="button" className="danger" title="删除备份文件" onClick={() => setDeleteTarget(artifact)}><Trash2 size={15} /><span>删文件</span></button>
                </div>
              </div>
            ))}
          </div>
        ) : (
          <div className="backup-artifact"><span className="artifact-icon"><Archive size={22} /></span><div><strong>当前版本没有可管理产物</strong><span>0 B</span><small>请选择其他备份版本或等待备份完成。</small></div></div>
        )}
      </Card>
      {deleteTarget && <ConfirmDialog title="删除备份文件" description="这是高危操作，会删除当前所选备份版本中的真实备份产物，任务记录仍会保留。" tone="danger" confirmText="确认删除文件" submitting={deleting} onCancel={() => setDeleteTarget(null)} onConfirm={confirmDeleteArtifact} details={[`备份版本：${selectedVersion?.label ?? "当前最新产物"}`, `数据库：${deleteTarget.database}`, `文件：${deleteTarget.name}`, `路径：${deleteTarget.path || deleteTarget.name}`, "删除后该数据库将无法再从这个备份版本恢复，操作不可撤销。"]} />}
    </div>
  );
}

async function copyText(value: string) {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(value);
    return;
  }
  const textarea = document.createElement("textarea");
  textarea.value = value;
  textarea.readOnly = true;
  textarea.style.position = "fixed";
  textarea.style.left = "-9999px";
  textarea.style.top = "0";
  document.body.appendChild(textarea);
  textarea.select();
  const ok = typeof document.execCommand === "function" && document.execCommand("copy");
  document.body.removeChild(textarea);
  if (!ok) throw new Error("copy failed");
}

function artifactFileName(artifact: BackupArtifactItem) {
  const parts = artifact.name.split("/");
  return parts[parts.length - 1] || "databridge-backup.tar.gz";
}
