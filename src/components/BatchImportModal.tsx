import { AlertTriangle, Check, CheckCircle2, Download, FileSpreadsheet, LoaderCircle, Upload, X } from "lucide-react";
import { useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { api } from "../api";
import type { BatchImportPreview, BatchImportResult } from "../types";
import { Badge, Button } from "./UI";

const templatePath = "/databridge-batch-import-template.xlsx";
const maxFileSize = 6 * 1024 * 1024;

export function BatchImportModal({
  onClose,
  onImported,
}: {
  onClose: () => void;
  onImported: (result: BatchImportResult) => void;
}) {
  const inputRef = useRef<HTMLInputElement | null>(null);
  const [file, setFile] = useState<File | null>(null);
  const [preview, setPreview] = useState<BatchImportPreview | null>(null);
  const [result, setResult] = useState<BatchImportResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [dragging, setDragging] = useState(false);
  const [error, setError] = useState("");

  function chooseFile(nextFile?: File) {
    setError("");
    setPreview(null);
    setResult(null);
    if (!nextFile) return;
    if (!nextFile.name.toLowerCase().endsWith(".xlsx")) {
      setFile(null);
      setError("仅支持 .xlsx 格式的 Excel 文件。");
      return;
    }
    if (nextFile.size > maxFileSize) {
      setFile(null);
      setError("Excel 文件不能超过 6 MB。");
      return;
    }
    setFile(nextFile);
  }

  async function validateFile() {
    if (!file) return;
    setLoading(true);
    setError("");
    try {
      setPreview(await api.previewExcelImport(file));
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : "文件校验失败");
    } finally {
      setLoading(false);
    }
  }

  async function executeImport() {
    if (!file || !preview?.valid) return;
    setLoading(true);
    setError("");
    try {
      const imported = await api.executeExcelImport(file);
      setResult(imported);
      setPreview(imported.preview);
      if (imported.success) onImported(imported);
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : "批量导入失败");
    } finally {
      setLoading(false);
    }
  }

  const stage = result?.success ? 3 : preview ? 2 : 1;

  return createPortal(
    <div className="modal-backdrop" onMouseDown={() => !loading && onClose()}>
      <div className="modal wide" role="dialog" aria-modal="true" onMouseDown={(event) => event.stopPropagation()}>
        <div className="modal-h">
          <div style={{ flex: 1 }}>
            <h3>批量导入连接与定时任务</h3>
            <div className="ms">上传 Excel，校验通过后一次性创建数据库连接和定时备份计划。</div>
          </div>
          <button type="button" className="icon-btn ghost" disabled={loading} onClick={onClose} aria-label="关闭批量导入">
            <X size={17} />
          </button>
        </div>

        <div className="steps" style={{ margin: "16px 24px 0" }} aria-label="导入进度">
          {["上传文件", "校验预览", "完成导入"].map((label, index) => {
            const step = index + 1;
            return (
              <div key={label} className={`step ${step < stage ? "done" : step === stage ? "on" : ""}`}>
                <span className="sn">{step < stage ? <Check size={12} /> : step}</span>
                <span>{label}</span>
              </div>
            );
          })}
        </div>

        <div className="modal-b">
          {result?.success ? (
            <div style={{ textAlign: "center", padding: "22px 12px" }}>
              <div
                style={{
                  width: 48,
                  height: 48,
                  borderRadius: "50%",
                  background: "var(--ok-soft)",
                  color: "var(--ok)",
                  display: "grid",
                  placeItems: "center",
                  margin: "0 auto 12px",
                }}
              >
                <CheckCircle2 size={26} />
              </div>
              <h4 style={{ fontSize: 16, fontWeight: 650, marginBottom: 6 }}>批量导入完成</h4>
              <p style={{ color: "var(--muted)", marginBottom: 14 }}>
                已创建 {result.connections.length} 个数据库连接和 {result.backups.length} 个定时备份任务。
              </p>
              <div className="chiprow" style={{ justifyContent: "center" }}>
                <span className="tag">
                  数据库连接 <b className="num">&nbsp;{result.connections.length}</b>
                </span>
                <span className="tag">
                  定时备份任务 <b className="num">&nbsp;{result.backups.length}</b>
                </span>
                <span className="tag">
                  失败项 <b className="num">&nbsp;0</b>
                </span>
              </div>
            </div>
          ) : !preview ? (
            <div style={{ display: "flex", flexDirection: "column", gap: 14 }}>
              <div
                className="card pad"
                style={{ background: "var(--surface-2)", display: "flex", alignItems: "center", gap: 12 }}
              >
                <span className="tglyph pg">
                  <FileSpreadsheet size={18} />
                </span>
                <div style={{ flex: 1 }}>
                  <div style={{ fontWeight: 600 }}>先下载标准模板</div>
                  <div style={{ fontSize: 12.5, color: "var(--muted)" }}>
                    模板包含“数据库连接”“定时备份任务”和“填写说明”三个工作表。
                  </div>
                </div>
                <a className="button secondary sm" href={templatePath} download="databridge-batch-import-template.xlsx">
                  <Download size={14} />
                  下载模板
                </a>
              </div>

              <div
                className="empty"
                style={{
                  borderColor: dragging || file ? "var(--indigo)" : undefined,
                  background: dragging ? "var(--indigo-faint)" : undefined,
                  cursor: "pointer",
                }}
                onDragEnter={(event) => {
                  event.preventDefault();
                  setDragging(true);
                }}
                onDragOver={(event) => event.preventDefault()}
                onDragLeave={(event) => {
                  event.preventDefault();
                  setDragging(false);
                }}
                onDrop={(event) => {
                  event.preventDefault();
                  setDragging(false);
                  chooseFile(event.dataTransfer.files[0]);
                }}
              >
                <input
                  ref={inputRef}
                  type="file"
                  hidden
                  accept=".xlsx,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
                  onChange={(event) => chooseFile(event.target.files?.[0])}
                />
                <div style={{ marginBottom: 8, color: file ? "var(--indigo-ink)" : "var(--muted)" }}>
                  {file ? <FileSpreadsheet size={28} /> : <Upload size={28} />}
                </div>
                {file ? (
                  <>
                    <div className="e-t">{file.name}</div>
                    <div className="e-s" style={{ marginBottom: 12 }}>
                      {formatFileSize(file.size)} · 已准备校验
                    </div>
                  </>
                ) : (
                  <>
                    <div className="e-t">拖入 Excel 文件，或点击选择</div>
                    <div className="e-s" style={{ marginBottom: 12 }}>
                      仅支持系统模板生成的 .xlsx 文件，最大 6 MB
                    </div>
                  </>
                )}
                <Button type="button" variant="secondary" className="sm" onClick={() => inputRef.current?.click()}>
                  {file ? "重新选择" : "选择文件"}
                </Button>
              </div>
            </div>
          ) : (
            <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
              <div className="chiprow">
                <span className="tag">
                  数据库连接 <b className="num">&nbsp;{preview.summary.connections}</b>
                </span>
                <span className="tag">
                  定时备份任务 <b className="num">&nbsp;{preview.summary.schedules}</b>
                </span>
                <Badge tone={preview.summary.errors ? "red" : "green"}>校验错误 {preview.summary.errors}</Badge>
              </div>

              <div className={preview.valid ? "success-banner" : "error-banner"}>
                {preview.valid ? <CheckCircle2 size={16} /> : <AlertTriangle size={16} />}
                <div>
                  <strong>{preview.valid ? "校验通过，可以导入" : "发现错误，请修正 Excel 后重新上传"}</strong>
                  <span style={{ marginLeft: 8, opacity: 0.8 }}>{preview.fileName}</span>
                </div>
              </div>

              {preview.issues.length > 0 ? (
                <div className="ledger" style={{ maxHeight: 160, overflowY: "auto" }}>
                  {preview.issues.map((issue, index) => (
                    <div className="lrow" style={{ cursor: "default", padding: "8px 14px" }} key={`${issue.sheet}-${issue.row}-${issue.field}-${index}`}>
                      <Badge tone="red">
                        {issue.sheet}
                        {issue.row ? ` · 第 ${issue.row} 行` : ""}
                      </Badge>
                      <span style={{ fontSize: 13 }}>
                        {issue.field ? `${issue.field}：` : ""}
                        {issue.message}
                      </span>
                    </div>
                  ))}
                </div>
              ) : null}

              {preview.connections.length > 0 ? (
                <ImportPreviewList title="数据库连接" count={preview.connections.length}>
                  {preview.connections.map((item) => (
                    <div className="lrow" style={{ cursor: "default", padding: "9px 14px" }} key={`${item.row}-${item.key}`}>
                      <span className="tag mono-t">#{item.row}</span>
                      <div className="lr-main">
                        <div className="lr-t">
                          <span>{item.name || "—"}</span>
                          <span className="tag mono-t">{item.key || "—"}</span>
                        </div>
                        <div className="lr-s">
                          <span>{item.type || "—"}</span>
                          <span className="mono">{item.host ? `${item.host}:${item.port || "—"}` : "—"}</span>
                        </div>
                      </div>
                      <Badge tone={item.errors.length ? "red" : "green"}>{item.errors.length ? "需修正" : "就绪"}</Badge>
                    </div>
                  ))}
                </ImportPreviewList>
              ) : null}

              {preview.schedules.length > 0 ? (
                <ImportPreviewList title="定时备份任务" count={preview.schedules.length}>
                  {preview.schedules.map((item) => (
                    <div className="lrow" style={{ cursor: "default", padding: "9px 14px" }} key={`${item.row}-${item.name}`}>
                      <span className="tag mono-t">#{item.row}</span>
                      <div className="lr-main">
                        <div className="lr-t">
                          <span>{item.name || "—"}</span>
                          <span className="tag mono-t">{item.connectionKey || "—"}</span>
                        </div>
                        <div className="lr-s">
                          <span>{databaseListLabel(item.databases)}</span>
                          <span>{scheduleLabel(item.frequency, item.time, item.weekday, item.monthDay)}</span>
                        </div>
                      </div>
                      <Badge tone={item.errors.length ? "red" : "green"}>{item.errors.length ? "需修正" : "就绪"}</Badge>
                    </div>
                  ))}
                </ImportPreviewList>
              ) : null}
            </div>
          )}
          {error ? (
            <div className="error-banner" style={{ marginTop: 12 }}>
              {error}
            </div>
          ) : null}
        </div>

        <div className="modal-f">
          <span className="note" />
          {result?.success ? (
            <Button type="button" onClick={onClose}>
              完成
            </Button>
          ) : preview ? (
            <>
              <Button
                type="button"
                variant="secondary"
                disabled={loading}
                onClick={() => {
                  setPreview(null);
                  setResult(null);
                  setError("");
                }}
              >
                重新选择文件
              </Button>
              <Button type="button" disabled={loading || !preview.valid} onClick={executeImport}>
                {loading ? (
                  <>
                    <LoaderCircle size={15} className="spin" />
                    正在测试并导入...
                  </>
                ) : (
                  "确认导入"
                )}
              </Button>
            </>
          ) : (
            <>
              <Button type="button" variant="secondary" disabled={loading} onClick={onClose}>
                取消
              </Button>
              <Button type="button" disabled={loading || !file} onClick={validateFile}>
                {loading ? (
                  <>
                    <LoaderCircle size={15} className="spin" />
                    正在校验...
                  </>
                ) : (
                  "校验文件"
                )}
              </Button>
            </>
          )}
        </div>
      </div>
    </div>,
    document.body,
  );
}

function ImportPreviewList({ title, count, children }: { title: string; count: number; children: ReactNode }) {
  return (
    <div>
      <div className="h-row" style={{ marginBottom: 6 }}>
        <span style={{ fontSize: 12.5, fontWeight: 600, color: "var(--muted)" }}>{title}</span>
        <span className="tag">{count} 项</span>
      </div>
      <div className="ledger" style={{ maxHeight: 190, overflowY: "auto" }}>
        {children}
      </div>
    </div>
  );
}

function formatFileSize(bytes: number) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

function scheduleLabel(frequency: string, time: string, weekday?: string, monthDay?: number) {
  if (frequency === "每周") return `${weekday || "每周"} ${time}`;
  if (frequency === "每月") return `每月 ${monthDay || "—"} 日 ${time}`;
  return `每天 ${time}`;
}

function databaseListLabel(databases: string[]) {
  if (databases.length === 1 && databases[0].toLowerCase() === "all") return "全部数据库（all）";
  return databases.join(", ") || "—";
}
