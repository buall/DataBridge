import { AlertTriangle, ArrowLeft, Check, DatabaseBackup, ShieldCheck } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { api } from "../api";
import { Badge, Button, Card, ConfirmDialog, DatabaseLogo } from "../components/UI";
import type { BackupJob, DatabaseConnection, RestoreBackupConfig, RestorePrecheckResult } from "../types";
import {
  artifactVersionsForRestore,
  backupArtifacts,
  BackupVersionSelector,
  formatBytes,
  RestoreConnectionSelect,
  storageTypeLabel,
  totalArtifactSize,
} from "./BackupMonitoring";

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
  const targets = connections.filter(
    (connection) => connection.type === job.sourceType && (!mongoOplogRestore || connection.id !== job.sourceId),
  );
  const [targetId, setTargetId] = useState(targets[0]?.id ?? "");
  const [selectedDatabases, setSelectedDatabases] = useState<string[]>(sourceDatabases);
  const [targetDatabases, setTargetDatabases] = useState<Record<string, string>>(() =>
    Object.fromEntries(sourceDatabases.map((database) => [database, database])),
  );
  const [strategy, setStrategy] = useState<"append" | "recreate">(mongoOplogRestore ? "recreate" : "append");
  const [precheck, setPrecheck] = useState<RestorePrecheckResult | null>(null);
  const [prechecking, setPrechecking] = useState(false);
  const [restoring, setRestoring] = useState(false);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [error, setError] = useState("");

  const sourceConnection = useMemo(
    () => connections.find((connection) => connection.name === job.sourceName && connection.type === job.sourceType) ?? null,
    [connections, job.sourceName, job.sourceType],
  );
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
  const ready =
    Boolean(selectedVersion) &&
    selectedVersion.status === "completed" &&
    Boolean(targetId) &&
    selectedDatabases.length > 0 &&
    selectedDatabases.every((database) => targetDatabases[database]?.trim());

  useEffect(() => {
    if (!targetId && targets[0]) setTargetId(targets[0].id);
  }, [targetId, targets]);

  useEffect(() => {
    setSelectedDatabases((current) => {
      const kept = current.filter((database) => sourceDatabases.includes(database));
      return kept.length ? kept : sourceDatabases;
    });
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

  function toggleDatabase(database: string) {
    setPrecheck(null);
    setSelectedDatabases((current) =>
      current.includes(database) ? current.filter((item) => item !== database) : [...current, database],
    );
  }

  function restoreConfig(): RestoreBackupConfig {
    const targetDatabasesForSelected = Object.fromEntries(
      selectedDatabases.map((database) => [database, targetDatabases[database]?.trim() ?? ""]),
    );
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
    if (!precheck || !precheck.ok) {
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

  const step1Done = Boolean(selectedVersion && artifacts.length > 0);
  const step2Done = selectedDatabases.length > 0;
  const step3Done = Boolean(targetId);
  const step4Done = precheck?.ok ?? false;

  return (
    <div className="page-stack">
      <div className="h-row">
        <div className="page-h">
          <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
            <DatabaseLogo type={job.sourceType} size="md" />
            <div>
              <h2>数据还原</h2>
              <p>
                {job.name} · <span className="mono">{job.id}</span>
              </p>
            </div>
          </div>
        </div>
        <div className="sp" />
        <Button variant="secondary" onClick={onBack}>
          <ArrowLeft size={15} />
          返回备份详情
        </Button>
      </div>

      {error ? <div className="error-banner">{error}</div> : null}

      {!versions.length ? (
        <div className="error-banner">
          <strong>当前任务没有可恢复的备份产物</strong>
          <div style={{ fontSize: 12.5, marginTop: 2 }}>请确认备份已成功生成文件，且任务不在执行中。</div>
        </div>
      ) : null}

      <div className="steps" aria-label="恢复步骤">
        <RestoreStep index={1} title="备份版本" active={!step1Done} done={step1Done} />
        <RestoreStep index={2} title="还原范围" active={step1Done && !step2Done} done={step2Done} />
        <RestoreStep index={3} title="目标位置" active={step2Done && !step3Done} done={step3Done} />
        <RestoreStep index={4} title="检查执行" active={step3Done} done={step4Done} />
      </div>

      <Card className="pad">
        <div className="h-row" style={{ marginBottom: 8 }}>
          <div>
            <div className="sec-title">1. 选择备份版本</div>
            <div className="sec-sub">
              {versions.length > 1 ? "选择要恢复的某一次备份版本" : "当前任务只有一个可恢复版本"}
            </div>
          </div>
          <div className="sp" />
          <Badge tone="gray">{storageTypeLabel(job.destinationType)}</Badge>
        </div>

        <BackupVersionSelector
          versions={versions}
          selectedId={selectedVersion?.id ?? ""}
          onSelect={(id) => {
            setVersionId(id);
            setPrecheck(null);
          }}
        />

        <div
          className="info-callout"
          style={{
            marginTop: 12,
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            flexWrap: "wrap",
            gap: 8,
          }}
        >
          <div>
            <strong>{selectedVersion ? selectedVersion.label : "当前最新产物"}</strong>
            <div style={{ fontSize: 12, marginTop: 2 }}>
              {selectedVersion?.current
                ? "当前展示最新一次备份版本，可继续选择还原范围和目标位置。"
                : "当前展示所选历史备份版本，可继续选择还原范围和目标位置。"}
            </div>
          </div>
          <span className="tag mono-t">
            {artifacts.length} 个数据库 · {formatBytes(totalArtifactSize(artifacts))}
          </span>
        </div>
      </Card>

      <div className="grid2" style={{ gap: 14, alignItems: "start" }}>
        <Card className="pad">
          <div className="h-row" style={{ marginBottom: 12 }}>
            <div>
              <div className="sec-title">2. 还原范围</div>
              <div className="sec-sub">
                {mongoOplogRestore ? "该备份会恢复整个 MongoDB 副本集" : "勾选要从该版本恢复的数据库"}
              </div>
            </div>
            <div className="sp" />
            <Badge tone="blue">
              {selectedDatabases.length} / {sourceDatabases.length}
            </Badge>
          </div>

          <div className="ledger">
            {sourceDatabases.map((database) => {
              const artifact = artifacts.find((item) => item.database === database);
              const checked = selectedSet.has(database);
              return (
                <div
                  key={database}
                  className="lrow"
                  role={mongoOplogRestore ? undefined : "button"}
                  tabIndex={mongoOplogRestore ? undefined : 0}
                  aria-pressed={mongoOplogRestore ? undefined : checked}
                  onClick={() => {
                    if (!mongoOplogRestore) toggleDatabase(database);
                  }}
                  onKeyDown={(event) => {
                    if (!mongoOplogRestore && (event.key === " " || event.key === "Enter")) {
                      event.preventDefault();
                      toggleDatabase(database);
                    }
                  }}
                  style={{
                    cursor: mongoOplogRestore ? "default" : "pointer",
                    opacity: checked ? 1 : 0.6,
                  }}
                >
                  <span
                    style={{
                      width: 18,
                      height: 18,
                      borderRadius: 5,
                      border: checked ? "1px solid var(--indigo)" : "1px solid var(--line)",
                      background: checked ? "var(--indigo)" : "transparent",
                      color: "#fff",
                      display: "inline-flex",
                      alignItems: "center",
                      justifyContent: "center",
                      flexShrink: 0,
                    }}
                  >
                    {checked ? <Check size={12} strokeWidth={2.5} /> : null}
                  </span>
                  <div className="lr-main">
                    <div className="lr-t">{database}</div>
                    <div className="lr-s mono">{artifact?.path || artifact?.name || "未找到产物路径"}</div>
                  </div>
                  <span className="tag mono-t">{artifact ? formatBytes(artifact.size ?? 0) : "未知大小"}</span>
                </div>
              );
            })}
          </div>
        </Card>

        <Card className="pad">
          <div className="h-row" style={{ marginBottom: 12 }}>
            <div>
              <div className="sec-title">3. 目标位置与映射</div>
              <div className="sec-sub">选择同类型连接，并确认源库到目标库的映射</div>
            </div>
            <div className="sp" />
            <Badge tone="gray">{job.sourceType}</Badge>
          </div>

          {mongoOplogRestore ? (
            <div className="warn-callout" style={{ marginBottom: 12 }}>
              MongoDB Oplog 增量恢复不可恢复到源实例自身，且仅支持重建目标副本集恢复。
            </div>
          ) : null}

          <RestoreConnectionSelect
            connectionId={targetId}
            connections={targets}
            onChange={(value) => {
              setTargetId(value);
              setPrecheck(null);
            }}
          />

          {showVersionComparison ? (
            <div
              className={crossMajorVersionRisk ? "warn-callout" : "info-callout"}
              style={{ marginBottom: 14 }}
            >
              <div style={{ fontWeight: 600, fontSize: 12.5 }}>
                版本对比：{`${job.sourceName} ${sourceVersion || "版本未知"} -> ${targetConnection?.name ?? "目标端"} ${targetVersion || "版本未知"}`}
              </div>
              <div style={{ fontSize: 12, marginTop: 3 }}>
                {crossMajorVersionRisk
                  ? "跨大版本恢复有失败风险，建议优先恢复到相同大版本后再升级。"
                  : "当前显示源端与目标端版本，建议优先使用相同大版本恢复。"}
              </div>
            </div>
          ) : null}

          <div className="ledger">
            {sourceDatabases.map((database) => {
              const checked = selectedSet.has(database);
              return (
                <div className="lrow" key={database} style={{ opacity: checked ? 1 : 0.55 }}>
                  <div className="lr-main" style={{ minWidth: 110 }}>
                    <div className="lr-t" style={{ fontSize: 13 }}>{database}</div>
                    <div className="lr-s">源数据库</div>
                  </div>
                  <span style={{ color: "var(--muted)", fontSize: 13 }}>→</span>
                  <div style={{ flex: 1, minWidth: 140 }}>
                    <input
                      className="inp"
                      style={{ height: 32, fontSize: 12.5 }}
                      disabled={mongoOplogRestore || !checked}
                      value={targetDatabases[database] ?? ""}
                      onChange={(event) => {
                        setPrecheck(null);
                        setTargetDatabases((current) => ({ ...current, [database]: event.target.value }));
                      }}
                      placeholder={database}
                      aria-label={`目标数据库 (${database})`}
                    />
                  </div>
                </div>
              );
            })}
          </div>
        </Card>
      </div>

      <Card className="pad">
        <div className="h-row" style={{ marginBottom: 12 }}>
          <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
            <ShieldCheck size={18} style={{ color: "var(--indigo-ink)" }} />
            <div>
              <div className="sec-title">4. 恢复策略与检查执行</div>
              <div className="sec-sub">执行前检查备份文件、客户端工具、目标连接与权限</div>
            </div>
          </div>
        </div>

        <div className="radio-cards" style={{ marginBottom: 14 }}>
          {!mongoOplogRestore ? (
            <button
              type="button"
              className={strategy === "append" ? "radio-card on" : "radio-card"}
              onClick={() => {
                setPrecheck(null);
                setStrategy("append");
              }}
            >
              <div className="rc-h">追加导入，冲突则失败</div>
              <div className="rc-d">保留目标库已有对象，若出现主键或表结构冲突则中止任务。</div>
            </button>
          ) : null}
          <button
            type="button"
            className={strategy === "recreate" ? "radio-card danger-card on" : "radio-card danger-card"}
            onClick={() => {
              setPrecheck(null);
              setStrategy("recreate");
            }}
          >
            <div className="rc-h">重建目标库后恢复</div>
            <div className="rc-d">先删除并重新创建已勾选的目标数据库，再导入备份文件。</div>
          </button>
        </div>

        {strategy === "recreate" ? (
          <div className="warn-callout" style={{ marginBottom: 14 }}>
            <AlertTriangle size={14} style={{ verticalAlign: "-2px", marginRight: 6 }} />
            将先删除并重新创建目标数据库，再导入备份文件。目标库现有数据会被清空。
          </div>
        ) : null}

        {precheck ? (
          <RestorePrecheckSummary result={precheck} />
        ) : (
          <div className="info-callout" style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 14 }}>
            <AlertTriangle size={15} />
            <span>启动前会先运行恢复前检查，确认备份产物、客户端工具及目标端权限就绪。</span>
          </div>
        )}

        <div className="h-row" style={{ marginTop: 14 }}>
          <Button variant="secondary" disabled={!ready || prechecking || restoring} onClick={() => void runPrecheck()}>
            {prechecking ? "检查中..." : "重新检查"}
          </Button>
          <div className="sp" />
          <Button disabled={!ready || prechecking || restoring} onClick={() => void handleRestoreAction()}>
            <DatabaseBackup size={15} />
            {restoring
              ? "创建中..."
              : prechecking
                ? "检查中..."
                : precheckNeedsConfirm
                  ? "确认继续恢复"
                  : precheck?.ok
                    ? "启动恢复"
                    : "检查并启动恢复"}
          </Button>
        </div>
      </Card>

      {confirmOpen ? (
        <ConfirmDialog
          title={strategy === "recreate" ? "重建目标库后恢复" : "确认恢复"}
          description={
            strategy === "recreate"
              ? "这是高危操作，确认后会先删除并重新创建目标数据库。"
              : "恢复前检查已通过，但存在需要重点关注的风险提示。"
          }
          tone={strategy === "recreate" ? "danger" : "warning"}
          confirmVariant="danger"
          confirmText={strategy === "recreate" ? "确认重建并恢复" : "确认继续恢复"}
          submitting={restoring}
          onCancel={() => setConfirmOpen(false)}
          onConfirm={createRestore}
          warnings={precheckWarnings.map((warning) => `${warning.label}：${warning.message}`)}
          details={[
            `目标连接：${targetConnection?.name ?? "未选择"} · ${targetConnection?.host ?? ""}`,
            `目标数据库：${selectedDatabases.map((database) => targetDatabases[database]?.trim() || database).join(", ")}`,
            strategy === "recreate"
              ? "只会处理已勾选的数据库；目标库现有数据会被清空，该操作不可撤销。"
              : "请先确认以上风险提示可接受，再继续恢复。",
          ]}
        />
      ) : null}
    </div>
  );
}

function RestoreStep({
  index,
  title,
  active,
  done,
}: {
  index: number;
  title: string;
  active?: boolean;
  done?: boolean;
}) {
  return (
    <div className={["step", done ? "done" : "", active ? "on" : ""].filter(Boolean).join(" ")}>
      <span className="sn">{done ? <Check size={12} strokeWidth={2.5} /> : index}</span>
      <span>{title}</span>
    </div>
  );
}

function RestorePrecheckSummary({ result }: { result: RestorePrecheckResult }) {
  return (
    <div style={{ display: "grid", gap: 10 }}>
      <div className={result.ok ? "success-banner" : "error-banner"}>
        <strong>{result.ok ? "恢复前检查通过" : "恢复前检查未通过"}</strong>
      </div>

      {result.warnings?.length ? (
        <div className="warn-callout" style={{ display: "flex", gap: 8, alignItems: "flex-start" }}>
          <AlertTriangle size={15} style={{ flexShrink: 0, marginTop: 2 }} />
          <span>
            {result.warnings.length === 1
              ? result.warnings[0].message
              : `发现 ${result.warnings.length} 项恢复风险，请确认后再继续。`}
          </span>
        </div>
      ) : null}

      <div className="ledger">
        {result.checks.map((check) => {
          const passed = check.status === "passed";
          return (
            <div className="lrow" key={check.key}>
              <Badge tone={passed ? "green" : check.status === "warning" ? "orange" : "red"}>
                {passed ? <Check size={11} /> : <AlertTriangle size={11} />}
                {check.label}
              </Badge>
              <div className="lr-main">
                <div className="lr-s" style={{ color: "var(--ink-2)" }}>{check.message}</div>
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

function restoreSourceDatabasesFromArtifacts(job: BackupJob, artifacts: ReturnType<typeof backupArtifacts>) {
  const names = artifacts.map((artifact) => artifact.database).filter(Boolean);
  if (names.length) return Array.from(new Set(names));
  return job.sourceDatabase
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

function majorVersion(version?: string) {
  const value = version?.trim();
  if (!value) return 0;
  const major = Number.parseInt(value.split(".")[0] ?? "", 10);
  return Number.isFinite(major) ? major : 0;
}
