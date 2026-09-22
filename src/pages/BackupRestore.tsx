import { AlertTriangle, ArrowLeft, Check, ChevronDown, DatabaseBackup, ShieldCheck } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { api } from "../api";
import { Badge, Button, Card, ConfirmDialog, SelectField } from "../components/UI";
import type { BackupJob, DatabaseConnection, RestoreBackupConfig, RestorePrecheckResult } from "../types";
import { artifactVersionsForRestore, backupArtifacts, canRestoreFromJob, formatBytes, formatDateTime, RestoreConnectionSelect, storageTypeLabel, totalArtifactSize } from "./BackupMonitoring";

export function BackupRestore({
  job,
  connections,
  onBack,
  onRestoreCreated,
}: {
  job: BackupJob;
  connections: DatabaseConnection[];
  onBack: () => void;
  onRestoreCreated: (job: BackupJob) => void;
}) {
  const versions = useMemo(() => artifactVersionsForRestore(job), [job]);
  const [versionId, setVersionId] = useState(versions[0]?.id ?? "current");
  const selectedVersion = versions.find((version) => version.id === versionId) ?? versions[0];
  const artifacts = selectedVersion?.artifacts ?? [];
  const mongoOplogRestore = job.sourceType === "MongoDB" && job.backupMode === "mongo_oplog";
  const sourceDatabases = useMemo(() => restoreSourceDatabasesFromArtifacts(job, artifacts), [job, artifacts]);
  const targets = connections.filter((connection) => connection.type === job.sourceType && (!mongoOplogRestore || connection.id !== job.sourceId));
  const [targetId, setTargetId] = useState(targets[0]?.id ?? "");
  const [selectedDatabases, setSelectedDatabases] = useState<string[]>(sourceDatabases);
  const [targetDatabases, setTargetDatabases] = useState<Record<string, string>>(() => Object.fromEntries(sourceDatabases.map((database) => [database, database])));
  const [strategy, setStrategy] = useState<"append" | "recreate">(mongoOplogRestore ? "recreate" : "append");
  const [precheck, setPrecheck] = useState<RestorePrecheckResult | null>(null);
  const [prechecking, setPrechecking] = useState(false);
  const [restoring, setRestoring] = useState(false);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [error, setError] = useState("");
  const [versionMenuOpen, setVersionMenuOpen] = useState(false);
  const versionMenuRef = useRef<HTMLDivElement | null>(null);
  const sourceConnection = useMemo(() => connections.find((connection) => connection.name === job.sourceName && connection.type === job.sourceType) ?? null, [connections, job.sourceName, job.sourceType]);
  const targetConnection = targets.find((connection) => connection.id === targetId);
  const selectedSet = new Set(selectedDatabases);
  const precheckWarnings = precheck?.warnings ?? [];
  const precheckNeedsConfirm = Boolean(precheck?.ok && (strategy === "recreate" || precheckWarnings.length > 0));
  const sourceVersion = sourceConnection?.version?.trim() ?? "";
  const targetVersion = targetConnection?.version?.trim() ?? "";
  const sourceMajor = majorVersion(sourceVersion);
  const targetMajor = majorVersion(targetVersion);
  const showVersionComparison = Boolean(targetConnection && (sourceVersion || targetVersion));
  const crossMajorVersionRisk = job.sourceType === "Neo4j" && sourceMajor && targetMajor && sourceMajor !== targetMajor;
  const ready = Boolean(selectedVersion) && selectedVersion.status === "completed" && Boolean(targetId) && selectedDatabases.length > 0 && selectedDatabases.every((database) => targetDatabases[database]?.trim());

  useEffect(() => {
    if (!targetId && targets[0]) setTargetId(targets[0].id);
  }, [targetId, targets]);

  useEffect(() => {
    setSelectedDatabases((current) => current.filter((database) => sourceDatabases.includes(database)).length ? current.filter((database) => sourceDatabases.includes(database)) : sourceDatabases);
    setTargetDatabases((current) => {
      const next = { ...current };
      sourceDatabases.forEach((database) => {
        if (!next[database]) next[database] = database;
      });
      Object.keys(next).forEach((database) => {
        if (!sourceDatabases.includes(database)) delete next[database];
      });
      return next;
    });
  }, [sourceDatabases]);

  useEffect(() => {
    if (mongoOplogRestore) setStrategy("recreate");
  }, [mongoOplogRestore]);

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

  function toggleDatabase(database: string) {
    setPrecheck(null);
    setSelectedDatabases((current) => current.includes(database) ? current.filter((item) => item !== database) : [...current, database]);
  }

  function restoreConfig(): RestoreBackupConfig {
    const targetDatabasesForSelected = Object.fromEntries(selectedDatabases.map((database) => [database, targetDatabases[database]?.trim() ?? ""]));
    return {
      targetId,
      targetDatabase: targetDatabasesForSelected[selectedDatabases[0] ?? ""] ?? "",
      artifactVersionId: versionId === "current" ? undefined : versionId,
      selectedDatabases,
      targetDatabases: targetDatabasesForSelected,
      strategy,
    };
  }

  async function runPrecheck() {
    setPrechecking(true);
    setError("");
    setConfirmOpen(false);
    setPrecheck(null);
    try {
      const result = await api.restorePrecheck(job.id, restoreConfig());
      setPrecheck(result);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "恢复前检查失败");
    } finally {
      setPrechecking(false);
    }
  }

  async function handleRestoreAction() {
    if (!precheck) {
      await runPrecheck();
      return;
    }
    if (!precheck.ok) {
      await runPrecheck();
      return;
    }
    if (precheckNeedsConfirm) {
      setConfirmOpen(true);
      return;
    }
    await createRestore();
  }

  async function createRestore() {
    setRestoring(true);
    setError("");
    try {
      const next = await api.restoreBackup(job.id, restoreConfig());
      setConfirmOpen(false);
      onRestoreCreated(next);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "创建恢复任务失败");
    } finally {
      setRestoring(false);
    }
  }

  return (
    <div className="page-stack restore-wizard-page">
      <div className="page-heading">
        <div><h2>数据还原</h2><p>{job.name} · 任务 ID {job.id}</p></div>
        <Button variant="secondary" onClick={onBack}><ArrowLeft size={16} />返回备份详情</Button>
      </div>
      {error && <div className="error-banner">{error}</div>}
      {!versions.length && <div className="error-banner detail-error"><strong>当前任务没有可恢复的备份产物</strong><span>请确认备份已成功生成文件，且任务不在执行中。</span></div>}
      <div className="restore-step-grid">
        <RestoreStep index={1} title="备份版本" active done={Boolean(selectedVersion && artifacts.length > 0)} />
        <RestoreStep index={2} title="还原范围" active={artifacts.length > 0} done={selectedDatabases.length > 0} />
        <RestoreStep index={3} title="目标位置" active={selectedDatabases.length > 0} done={Boolean(targetId)} />
        <RestoreStep index={4} title="检查执行" active={Boolean(targetId)} done={precheck?.ok ?? false} />
      </div>
      <div className="restore-wizard-grid">
        <Card className="restore-version-card">
          <div className="section-title compact"><div><h3>备份版本</h3><p>{versions.length > 1 ? "选择要恢复的某一次备份。" : "当前任务只有一个可恢复版本。"}</p></div><Badge tone="gray">{storageTypeLabel(job.destinationType)}</Badge></div>
          {versions.length > 1 && <div className="artifact-version-picker field select-field restore-version-picker" ref={versionMenuRef}><span>备份版本</span><button type="button" className="select-trigger artifact-version-trigger" aria-haspopup="listbox" aria-expanded={versionMenuOpen} onClick={() => setVersionMenuOpen((current) => !current)}><span>{selectedVersion ? `${selectedVersion.label} · ${selectedVersion.artifacts.length} 个数据库 · ${formatBytes(totalArtifactSize(selectedVersion.artifacts))}` : "选择备份版本"}</span><ChevronDown size={16} /></button>{versionMenuOpen && <div className="select-menu artifact-version-menu" role="listbox" aria-label="备份版本">{versions.map((version) => <button type="button" key={version.id} className={version.id === selectedVersion?.id ? "active" : ""} role="option" aria-selected={version.id === selectedVersion?.id} onClick={() => { setVersionId(version.id); setVersionMenuOpen(false); setPrecheck(null); }}><strong>{version.label}</strong><small>{version.artifacts.length} 个数据库 · {formatBytes(totalArtifactSize(version.artifacts))}{version.current ? " · 最新产物" : ""}</small></button>)}</div>}</div>}
          <div className="artifact-manager-summary restore-version-summary-block">
            <strong>{selectedVersion ? selectedVersion.label : "当前最新产物"}</strong>
            <span>{artifacts.length} 个数据库 · {formatBytes(totalArtifactSize(artifacts))}</span>
            <small>{selectedVersion?.current ? "当前展示最新一次备份版本，可继续选择还原范围和目标位置。" : "当前展示所选备份版本，可继续选择还原范围和目标位置。"}</small>
          </div>
          <div className="restore-artifact-mini-list">
            {artifacts.map((artifact) => <div key={artifact.database}><strong>{artifact.database}</strong><span>{formatBytes(artifact.size ?? 0)}</span><small>{artifact.path || artifact.name}</small></div>)}
          </div>
        </Card>
        <Card className="restore-scope-card">
          <div className="section-title compact"><div><h3>还原范围</h3><p>{mongoOplogRestore ? "该备份会恢复整个 MongoDB 副本集。" : "勾选要从该版本恢复的数据库。"}</p></div><Badge tone="blue">{selectedDatabases.length} / {sourceDatabases.length}</Badge></div>
          <div className="restore-database-map restore-scope-map">
            <div className="restore-database-map-head"><span>恢复</span><span>源数据库</span><span>备份文件</span></div>
            {sourceDatabases.map((database) => {
              const artifact = artifacts.find((item) => item.database === database);
              return (
                <div className={`restore-database-row ${selectedSet.has(database) ? "selected" : "muted"}`} key={database} role={mongoOplogRestore ? undefined : "button"} tabIndex={mongoOplogRestore ? undefined : 0} aria-pressed={mongoOplogRestore ? undefined : selectedSet.has(database)} onClick={() => { if (!mongoOplogRestore) toggleDatabase(database); }} onKeyDown={(event) => { if (!mongoOplogRestore && (event.key === " " || event.key === "Enter")) { event.preventDefault(); toggleDatabase(database); } }}>
                  <span className={`checkbox restore-database-check ${selectedSet.has(database) ? "checked" : ""}`}>{selectedSet.has(database) && <Check size={13} />}</span>
                  <strong>{database}</strong>
                  <small>{artifact ? formatBytes(artifact.size ?? 0) : "未找到产物"}</small>
                </div>
              );
            })}
          </div>
        </Card>
        <Card className="restore-target-card">
          <div className="section-title compact"><div><h3>目标位置</h3><p>选择同类型连接，并确认源库到目标库的映射。</p></div><Badge tone="gray">{job.sourceType}</Badge></div>
          <div className="restore-target-form">
            <RestoreConnectionSelect connectionId={targetId} connections={targets} onChange={(value) => { setTargetId(value); setPrecheck(null); }} />
            {showVersionComparison && (
              <div className={`restore-version-compare ${crossMajorVersionRisk ? "warning" : "info"}`}>
                <strong>版本对比</strong>
                <span>{`${job.sourceName} ${sourceVersion || "版本未知"} -> ${targetConnection?.name ?? "目标端"} ${targetVersion || "版本未知"}`}</span>
                <small>{crossMajorVersionRisk ? "跨大版本恢复有失败风险，建议优先恢复到相同大版本后再升级。" : "当前显示源端与目标端版本，建议优先使用相同大版本恢复。"}</small>
              </div>
            )}
            <div className="restore-database-map restore-target-map">
              <div className="restore-database-map-head"><span>恢复</span><span>源数据库</span><span>目标数据库</span></div>
              {sourceDatabases.map((database) => (
                <div className={`restore-database-row ${selectedSet.has(database) ? "selected" : "muted"}`} key={database}>
                  <span className={`checkbox restore-database-check ${selectedSet.has(database) ? "checked" : ""}`}>{selectedSet.has(database) && <Check size={13} />}</span>
                  <strong>{database}</strong>
                  <input disabled={mongoOplogRestore || !selectedSet.has(database)} value={targetDatabases[database] ?? ""} onChange={(event) => { setPrecheck(null); setTargetDatabases((current) => ({ ...current, [database]: event.target.value })); }} placeholder={database} />
                </div>
              ))}
            </div>
          </div>
        </Card>
        <Card className="restore-execute-card">
          <div className="section-title compact"><div><h3>检查执行</h3><p>执行前检查备份文件、客户端工具、目标连接和权限。</p></div><ShieldCheck size={20} /></div>
          <SelectField label="恢复策略" value={strategy} onChange={(value) => { setPrecheck(null); setStrategy(value as "append" | "recreate"); }}>
            {!mongoOplogRestore && <option value="append">追加导入，冲突则失败</option>}
            <option value="recreate">重建目标库后恢复</option>
          </SelectField>
          {strategy === "recreate" && <div className="restore-strategy-warning">将先删除并重新创建目标数据库，再导入备份文件。目标库现有数据会被清空。</div>}
          {precheck ? <RestorePrecheckSummary result={precheck} /> : <div className="restore-precheck-placeholder"><AlertTriangle size={16} /><span>启动前会先运行恢复前检查。</span></div>}
          <Button disabled={!ready || prechecking || restoring} onClick={() => void handleRestoreAction()}>{restoring ? "创建中..." : prechecking ? "检查中..." : precheckNeedsConfirm ? "确认继续恢复" : precheck?.ok ? "启动恢复" : "检查并启动恢复"}</Button>
        </Card>
      </div>
      {confirmOpen && <ConfirmDialog title={strategy === "recreate" ? "重建目标库后恢复" : "确认恢复"} description={strategy === "recreate" ? "这是高危操作，确认后会先删除并重新创建目标数据库。" : "恢复前检查已通过，但存在需要重点关注的风险提示。"} tone={strategy === "recreate" ? "danger" : "warning"} confirmVariant="danger" confirmText={strategy === "recreate" ? "确认重建并恢复" : "确认继续恢复"} submitting={restoring} onCancel={() => setConfirmOpen(false)} onConfirm={createRestore} warnings={precheckWarnings.map((warning) => `${warning.label}：${warning.message}`)} details={[`目标连接：${targetConnection?.name ?? "未选择"} · ${targetConnection?.host ?? ""}`, `目标数据库：${selectedDatabases.map((database) => targetDatabases[database]?.trim() || database).join(", ")}`, strategy === "recreate" ? "只会处理已勾选的数据库；目标库现有数据会被清空，该操作不可撤销。" : "请先确认以上风险提示可接受，再继续恢复。"]} />}
    </div>
  );
}

function RestoreStep({ index, title, active, done }: { index: number; title: string; active?: boolean; done?: boolean }) {
  return <div className={`restore-step ${active ? "active" : ""} ${done ? "done" : ""}`}><span>{done ? <Check size={13} /> : index}</span><strong>{title}</strong></div>;
}

function RestorePrecheckSummary({ result }: { result: RestorePrecheckResult }) {
  return (
    <div className={`restore-precheck ${result.ok ? "passed" : "failed"}`}>
      <strong>{result.ok ? "恢复前检查通过" : "恢复前检查未通过"}</strong>
      {result.warnings?.length ? (
        <div className="restore-precheck-warning-banner">
          <AlertTriangle size={14} />
          <span>{result.warnings.length === 1 ? result.warnings[0].message : `发现 ${result.warnings.length} 项恢复风险，请确认后再继续。`}</span>
        </div>
      ) : null}
      <div>
        {result.checks.map((check) => (
          <span className={check.status} key={check.key}>
            {check.status === "passed" ? <Check size={12} /> : <AlertTriangle size={12} />}
            <em>{check.label}</em>
            {check.message}
          </span>
        ))}
      </div>
    </div>
  );
}

function restoreSourceDatabasesFromArtifacts(job: BackupJob, artifacts: ReturnType<typeof backupArtifacts>) {
  const names = artifacts.map((artifact) => artifact.database).filter(Boolean);
  if (names.length) return Array.from(new Set(names));
  return job.sourceDatabase.split(",").map((item) => item.trim()).filter(Boolean);
}

function majorVersion(version?: string) {
  const value = version?.trim();
  if (!value) return 0;
  const major = Number.parseInt(value.split(".")[0] ?? "", 10);
  return Number.isFinite(major) ? major : 0;
}
