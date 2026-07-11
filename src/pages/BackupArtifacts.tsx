import { AlertTriangle, Archive, ArrowLeft, CheckCircle2, ChevronDown, Clipboard, Copy, DatabaseBackup, Download, FileText, HardDrive, HardDriveDownload, KeyRound, PlayCircle, Terminal, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { api } from "../api";
import { Badge, Button, Card } from "../components/UI";
import type { BackupJob } from "../types";
import { artifactVersionsForRestore, backupArtifacts, formatBytes, storageTypeLabel, totalArtifactSize } from "./BackupMonitoring";

type BackupArtifactItem = ReturnType<typeof backupArtifacts>[number];
type EtcdManualStep = {
  icon: typeof HardDriveDownload;
  title: string;
  description: string;
  notes?: Array<{ term: string; description: string }>;
  command: string;
};

export function BackupArtifacts({
	job,
	onBack,
}: {
	job: BackupJob;
	onBack: () => void;
}) {
  const versions = artifactVersionsForRestore(job);
  const [selectedVersionId, setSelectedVersionId] = useState(versions[0]?.id ?? "current");
  const selectedVersion = versions.find((version) => version.id === selectedVersionId) ?? versions[0] ?? null;
  const artifacts = selectedVersion?.artifacts ?? backupArtifacts(job);
  const showEtcdSnapshotManual = job.sourceType === "etcd";
	const [copied, setCopied] = useState("");
	const [downloading, setDownloading] = useState("");
	const [error, setError] = useState("");
	const [versionMenuOpen, setVersionMenuOpen] = useState(false);
  const [manualOpen, setManualOpen] = useState(false);
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
				  <button type="button" data-tooltip-content="复制路径" onClick={() => copyArtifactPath(artifact)}>{copied === artifact.database ? <Clipboard size={15} /> : <Copy size={15} />}<span>{copied === artifact.database ? "已复制路径" : "复制路径"}</span></button>
				  <button type="button" data-tooltip-content="下载备份产物" disabled={downloading === artifact.database} onClick={() => downloadArtifact(artifact)}><Download size={15} /><span>{downloading === artifact.database ? "下载中" : "下载"}</span></button>
          {showEtcdSnapshotManual && <button type="button" className="manual" data-tooltip-content="查看 etcd 数据恢复文档" onClick={() => setManualOpen(true)}><FileText size={15} /><span>数据恢复文档</span></button>}
				</div>
			  </div>
            ))}
          </div>
        ) : (
          <div className="backup-artifact"><span className="artifact-icon"><Archive size={22} /></span><div><strong>当前版本没有可管理产物</strong><span>0 B</span><small>请选择其他备份版本或等待备份完成。</small></div></div>
        )}
      </Card>
      {showEtcdSnapshotManual && manualOpen && <EtcdSnapshotManualDialog artifacts={artifacts} onClose={() => setManualOpen(false)} />}
	</div>
  );
}

function EtcdSnapshotManualDialog({ artifacts, onClose }: { artifacts: BackupArtifactItem[]; onClose: () => void }) {
  const artifactPath = artifacts[0]?.path || artifacts[0]?.name || "/path/to/etcd_cluster_20260709_140510.tar.gz";
  const endpoint = "http://127.0.0.1:12379";
  const snapshotLookupCommand = artifactPath.endsWith(".db")
    ? `SNAPSHOT_DB="${artifactPath}"`
    : `mkdir -p /tmp/etcd-snapshot-view
tar -tzf ${artifactPath}
tar -xzf ${artifactPath} -C /tmp/etcd-snapshot-view

SNAPSHOT_DB="$(find /tmp/etcd-snapshot-view -name '*.db' -type f | head -n 1)"
test -n "$SNAPSHOT_DB"`;
  const [copiedCommand, setCopiedCommand] = useState("");
  const [manualTab, setManualTab] = useState<"view" | "restore">("view");
  const viewSteps: EtcdManualStep[] = [
    {
      icon: HardDriveDownload,
      title: "下载并解压产物",
      description: "如果备份产物是 .tar.gz，先解压到临时目录，并自动找到里面的 etcd snapshot .db 文件。",
      command: snapshotLookupCommand,
    },
    {
      icon: FileText,
      title: "查看快照元信息",
      description: "确认快照是否可读，以及它包含的 revision、key 数量、hash 和文件大小。",
      command: `etcdutl snapshot status "$SNAPSHOT_DB" -w table`,
    },
    {
      icon: DatabaseBackup,
      title: "恢复到临时目录",
      description: "只恢复到临时目录用于查看，避免影响已有 etcd 数据目录。",
      command: `rm -rf /tmp/etcd-restore

etcdutl snapshot restore "$SNAPSHOT_DB" \\
  --data-dir /tmp/etcd-restore`,
    },
    {
      icon: PlayCircle,
      title: "启动本地 etcd",
      description: "用临时恢复目录启动一个本地实例，后续只通过这个本地端口查询。",
      command: `ETCD_UNSUPPORTED_ARCH=arm64 etcd \\
  --data-dir /tmp/etcd-restore \\
  --listen-client-urls ${endpoint} \\
  --advertise-client-urls ${endpoint}`,
    },
    {
      icon: KeyRound,
      title: "查看 key/value 内容",
      description: "另开一个终端列出 key，或读取指定 key 的内容。",
      command: `etcdctl --endpoints=${endpoint} get "" --prefix --keys-only

etcdctl --endpoints=${endpoint} get /your/key`,
    },
    {
      icon: Terminal,
      title: "Kubernetes 对象内容看起来异常",
      description: "Kubernetes 存在 etcd 里的对象通常是 protobuf，直接读取时出现乱码属于正常现象。需要粗略看文本片段时可以配合 strings。",
      command: `etcdctl --endpoints=${endpoint} get /registry/storageclasses/openebs-hostpath --print-value-only | strings`,
    },
  ];
  const restoreSteps: EtcdManualStep[] = [
    {
      icon: HardDriveDownload,
      title: "准备 snapshot 文件",
      description: "先在新集群的控制节点上下载并解压备份产物，自动定位 .db 快照文件。",
      command: snapshotLookupCommand,
    },
    {
      icon: FileText,
      title: "确认快照元信息",
      description: "恢复前先确认快照可读，记录 revision、key 数量和 hash，便于恢复后核对。",
      command: `etcdutl snapshot status "$SNAPSHOT_DB" -w table`,
    },
    {
      icon: AlertTriangle,
      title: "理解恢复影响范围",
      description: "snapshot restore 会生成一个新的 etcd 数据目录。后续如果把目标 etcd 的 data-dir 指向这个目录，目标集群当前 etcd 状态会以该快照为准；不要在已有业务运行的集群上直接覆盖使用。",
      notes: [
        { term: "恢复目录", description: "建议先恢复到新的空目录，例如 /var/lib/etcd-restore。" },
        { term: "生效条件", description: "只有当 etcd 配置切换到恢复目录后，新启动的 etcd 才会使用 snapshot 中的数据。" },
      ],
      command: `rm -rf /var/lib/etcd-restore

etcdutl snapshot restore "$SNAPSHOT_DB" \\
  --data-dir /var/lib/etcd-restore`,
    },
    {
      icon: AlertTriangle,
      title: "先确认新集群类型",
      description: "不同 Kubernetes 安装方式接管 etcd 的入口不同。先确认是独立 systemd etcd、kubeadm static pod、发行版内置 etcd，还是云厂商托管控制面。",
      command: `# kubeadm 常见路径
ls /etc/kubernetes/manifests/etcd.yaml
ls /etc/kubernetes/pki/etcd/

# 独立 systemd etcd 常见检查
systemctl status etcd --no-pager

# k3s / rke2 等发行版优先使用自身恢复命令
systemctl status k3s --no-pager
systemctl status rke2-server --no-pager`,
    },
    {
      icon: DatabaseBackup,
      title: "单节点新集群恢复模板",
      description: "适用于新建单节点 etcd。把 master-1 和 192.168.1.10 替换为新节点实际名称和 peer IP。",
      notes: [
        { term: "--name", description: "当前节点名称，必须出现在 --initial-cluster 成员列表中。" },
        { term: "--initial-cluster", description: "新集群完整成员列表，所有节点填写一致。" },
        { term: "--initial-advertise-peer-urls", description: "当前节点自己的 peer 地址，每台节点填写自己的地址。" },
        { term: "--initial-cluster-token", description: "新集群 token，所有节点填写一致，用于避免和其他恢复任务混淆。" },
      ],
      command: `rm -rf /var/lib/etcd-restore

etcdutl snapshot restore "$SNAPSHOT_DB" \\
  --data-dir /var/lib/etcd-restore \\
  --name master-1 \\
  --initial-cluster master-1=https://192.168.1.10:2380 \\
  --initial-advertise-peer-urls https://192.168.1.10:2380 \\
  --initial-cluster-token etcd-restore-$(date +%s)`,
    },
    {
      icon: DatabaseBackup,
      title: "三节点新集群恢复模板",
      description: "每个新节点都用同一个 snapshot，但 --name 和 --initial-advertise-peer-urls 要按当前节点修改，并与 initial-cluster 成员列表保持一致。",
      command: `rm -rf /var/lib/etcd-restore

etcdutl snapshot restore "$SNAPSHOT_DB" \\
  --data-dir /var/lib/etcd-restore \\
  --name master-1 \\
  --initial-cluster master-1=https://192.168.1.10:2380,master-2=https://192.168.1.11:2380,master-3=https://192.168.1.12:2380 \\
  --initial-advertise-peer-urls https://192.168.1.10:2380 \\
  --initial-cluster-token etcd-restore-$(date +%s)`,
    },
    {
      icon: Terminal,
      title: "kubeadm / static pod 接管方式",
      description: "kubeadm 创建控制平面时，etcd 通常由 kubelet 以 static pod 管理。先停 kubelet 或移走 etcd manifest，再把 manifest 的 data-dir 指向恢复目录。",
      command: `systemctl stop kubelet

mv /etc/kubernetes/manifests/etcd.yaml /etc/kubernetes/manifests/etcd.yaml.bak

# 在每个控制平面节点执行 snapshot restore 后，
# 修改 etcd.yaml 中的 --data-dir、--name、peer/client URL、证书路径
# 再放回 manifest，让 kubelet 拉起 etcd static pod

mv /etc/kubernetes/manifests/etcd.yaml.bak /etc/kubernetes/manifests/etcd.yaml
systemctl start kubelet
journalctl -u kubelet -f`,
    },
    {
      icon: PlayCircle,
      title: "独立 systemd etcd 接管方式",
      description: "如果 etcd 是独立 systemd 服务，停止 etcd 后把配置里的数据目录切到 restore 结果目录。不同安装方式的配置文件路径可能不同。",
      command: `systemctl stop etcd

# 确认 etcd 配置中的 --data-dir 指向 /var/lib/etcd-restore
# 同时确认 --name、peer/client URL、证书路径都匹配新节点

systemctl start etcd
systemctl status etcd --no-pager`,
    },
    {
      icon: AlertTriangle,
      title: "发行版和托管控制面",
      description: "k3s、rke2、Rancher、MicroK8s 等优先用发行版自己的 snapshot restore；EKS/GKE/ACK/CCE 等托管控制面通常不能直接恢复 etcd snapshot。",
      command: `# k3s / rke2 示例：以发行版实际文档为准
# 不要直接套 kubeadm 的 /etc/kubernetes/manifests/etcd.yaml 路径

# 云厂商托管控制面：
# 通常无法接管控制面 etcd 数据目录
# 应使用云厂商备份恢复能力，或新建集群后用 Velero / 应用备份恢复资源`,
    },
    {
      icon: KeyRound,
      title: "恢复后检查",
      description: "确认 etcd 健康，再检查 Kubernetes API 和节点状态。证书路径按实际集群调整。",
      command: `etcdctl \\
  --endpoints=https://127.0.0.1:2379 \\
  --cacert=/etc/kubernetes/pki/etcd/ca.crt \\
  --cert=/etc/kubernetes/pki/etcd/server.crt \\
  --key=/etc/kubernetes/pki/etcd/server.key \\
  endpoint health

kubectl get nodes
kubectl get pods -A`,
    },
  ];
  const steps = manualTab === "view" ? viewSteps : restoreSteps;

  useEffect(() => {
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", closeOnEscape);
    return () => window.removeEventListener("keydown", closeOnEscape);
  }, [onClose]);

  async function copyCommand(command: string, title: string) {
    setCopiedCommand(title);
    try {
      await copyText(command);
      window.setTimeout(() => setCopiedCommand((current) => current === title ? "" : current), 1600);
    } catch {
      setCopiedCommand("复制失败");
      window.setTimeout(() => setCopiedCommand((current) => current === "复制失败" ? "" : current), 1600);
    }
  }

  return (
    <div className="modal-backdrop artifact-manual-backdrop" onMouseDown={onClose}>
      <div className="modal artifact-manual-modal" role="dialog" aria-modal="true" aria-labelledby="artifact-manual-title" onMouseDown={(event) => event.stopPropagation()}>
        <div className="modal-head artifact-manual-modal-head">
          <div className="artifact-manual-title">
            <span><DatabaseBackup size={20} /></span>
            <div>
              <h3 id="artifact-manual-title">查看 etcd snapshot 备份</h3>
              <p>.db 文件是 etcd snapshot 备份，不能像普通文档一样直接打开查看。可先查看内容，也可按离线流程恢复到新的 etcd 集群。</p>
            </div>
          </div>
          <button type="button" className="plain-icon" aria-label="关闭操作手册" onClick={onClose}><X size={18} /></button>
        </div>
        <div className="artifact-manual-tabs" role="tablist" aria-label="etcd snapshot 操作类型">
          <button type="button" className={manualTab === "view" ? "active" : ""} role="tab" aria-selected={manualTab === "view"} onClick={() => setManualTab("view")}>查看备份</button>
          <button type="button" className={manualTab === "restore" ? "active" : ""} role="tab" aria-selected={manualTab === "restore"} onClick={() => setManualTab("restore")}>恢复到新集群</button>
        </div>
        <div className="artifact-manual-warning">
          <AlertTriangle size={16} />
          <span>{manualTab === "view" ? "不要把 snapshot 直接恢复覆盖到生产 etcd 数据目录。临时查看统一使用 /tmp 目录。" : "恢复到新集群会重建该集群的 etcd 状态，不是导入到现有运行中的集群。执行前请停止目标 etcd，并确认节点名、IP、证书和数据目录都属于新集群。"}</span>
        </div>
        <div className="artifact-manual-steps">
          {steps.map(({ icon: Icon, title, description, notes, command }, index) => (
            <section className="artifact-manual-step" key={title}>
              <div className="artifact-manual-step-head">
                <span>{index + 1}</span>
                <Icon size={17} />
                <div><strong>{title}</strong><small>{description}</small></div>
              </div>
              {notes?.length ? (
                <ul className="artifact-manual-notes">
                  {notes.map((note) => <li key={note.term}><code>{note.term}</code><span>：{note.description}</span></li>)}
                </ul>
              ) : null}
              <div className="artifact-manual-command-wrap">
                <button type="button" className="artifact-command-copy" data-tooltip-content="复制命令" onClick={() => copyCommand(command, title)}>
                  {copiedCommand === title ? <Clipboard size={14} /> : <Copy size={14} />}
                  <span>{copiedCommand === title ? "已复制" : copiedCommand === "复制失败" ? "失败" : "复制"}</span>
                </button>
                <pre className="manual-command artifact-manual-command"><code>{command}</code></pre>
              </div>
            </section>
          ))}
        </div>
        <div className="artifact-manual-footer">
          <CheckCircle2 size={16} />
          <span>{manualTab === "view" ? "查看完成后关闭启动本地 etcd 的终端，确认 12379 端口不再使用后，可以删除 /tmp/etcd-restore 和 /tmp/etcd-snapshot-view。" : "多节点恢复时所有节点必须使用同一个 snapshot、同一个 initial-cluster 成员列表和同一个 token；每个节点只修改自己的 --name 与 --initial-advertise-peer-urls。Kubernetes 发行版集群优先遵循发行版恢复流程。"}</span>
        </div>
      </div>
    </div>
  );
}

async function copyText(value: string) {
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
  if (ok) return;
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(value);
    return;
  }
  throw new Error("copy failed");
}

function artifactFileName(artifact: BackupArtifactItem) {
  const parts = artifact.name.split("/");
  return parts[parts.length - 1] || "databridge-backup.tar.gz";
}
