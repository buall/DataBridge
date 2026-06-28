import { AlertTriangle, Check, CheckCircle2, Download, FileSpreadsheet, LoaderCircle, Upload, X } from "lucide-react";
import { useRef, useState, type ReactNode } from "react";
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
  return (
    <div className="modal-backdrop batch-import-backdrop" onMouseDown={() => !loading && onClose()}>
      <div className="modal batch-import-modal" onMouseDown={(event) => event.stopPropagation()}>
        <div className="modal-head">
          <div><h3>批量导入连接与定时任务</h3><p>上传 Excel，校验通过后一次性创建数据库连接和定时备份计划。</p></div>
          <button type="button" className="plain-icon" disabled={loading} onClick={onClose} aria-label="关闭批量导入"><X /></button>
        </div>

        <div className="batch-import-steps" aria-label="导入进度">
          {["上传文件", "校验预览", "完成导入"].map((label, index) => {
            const step = index + 1;
            return <div key={label} className={step < stage ? "done" : step === stage ? "active" : ""}><span>{step < stage ? <Check size={13} /> : step}</span><strong>{label}</strong></div>;
          })}
        </div>

        {result?.success ? (
          <div className="batch-import-success">
            <span><CheckCircle2 size={30} /></span>
            <h4>批量导入完成</h4>
            <p>已创建 {result.connections.length} 个数据库连接和 {result.backups.length} 个定时备份任务。</p>
            <div className="batch-import-summary success-summary">
              <div><strong>{result.connections.length}</strong><span>数据库连接</span></div>
              <div><strong>{result.backups.length}</strong><span>定时备份任务</span></div>
              <div><strong>0</strong><span>失败项</span></div>
            </div>
          </div>
        ) : !preview ? (
          <div className="batch-import-upload-stage">
            <div className="template-download-card">
              <span><FileSpreadsheet size={24} /></span>
              <div><strong>先下载标准模板</strong><p>模板包含“数据库连接”“定时备份任务”和“填写说明”三个工作表。</p></div>
              <a className="button secondary" href={templatePath} download="databridge-batch-import-template.xlsx"><Download size={16} />下载模板</a>
            </div>
            <div
              className={`batch-import-dropzone ${dragging ? "dragging" : ""} ${file ? "selected" : ""}`}
              onDragEnter={(event) => { event.preventDefault(); setDragging(true); }}
              onDragOver={(event) => event.preventDefault()}
              onDragLeave={(event) => { event.preventDefault(); setDragging(false); }}
              onDrop={(event) => { event.preventDefault(); setDragging(false); chooseFile(event.dataTransfer.files[0]); }}
            >
              <input ref={inputRef} type="file" accept=".xlsx,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" onChange={(event) => chooseFile(event.target.files?.[0])} />
              <span className="dropzone-icon">{file ? <FileSpreadsheet size={30} /> : <Upload size={30} />}</span>
              {file ? <><strong>{file.name}</strong><p>{formatFileSize(file.size)} · 已准备校验</p></> : <><strong>拖入 Excel 文件，或点击选择</strong><p>仅支持系统模板生成的 .xlsx 文件，最大 6 MB</p></>}
              <Button type="button" variant="secondary" onClick={() => inputRef.current?.click()}>{file ? "重新选择" : "选择文件"}</Button>
            </div>
          </div>
        ) : (
          <div className="batch-import-preview-stage">
            <div className="batch-import-summary">
              <div><strong>{preview.summary.connections}</strong><span>数据库连接</span></div>
              <div><strong>{preview.summary.schedules}</strong><span>定时备份任务</span></div>
              <div className={preview.summary.errors ? "has-errors" : "ok"}><strong>{preview.summary.errors}</strong><span>校验错误</span></div>
            </div>
            <div className={`batch-import-status ${preview.valid ? "valid" : "invalid"}`}>
              {preview.valid ? <CheckCircle2 size={18} /> : <AlertTriangle size={18} />}
              <div><strong>{preview.valid ? "校验通过，可以导入" : "发现错误，请修正 Excel 后重新上传"}</strong><span>{preview.fileName}</span></div>
            </div>
            {preview.issues.length > 0 && (
              <div className="batch-import-issues">
                {preview.issues.map((issue, index) => <div key={`${issue.sheet}-${issue.row}-${issue.field}-${index}`}><Badge tone="red">{issue.sheet}{issue.row ? ` · 第 ${issue.row} 行` : ""}</Badge><span>{issue.field ? `${issue.field}：` : ""}{issue.message}</span></div>)}
              </div>
            )}
            {preview.connections.length > 0 && (
              <ImportPreviewTable title="数据库连接" count={preview.connections.length}>
                <thead><tr><th>行</th><th>连接标识</th><th>连接名称</th><th>类型</th><th>主机</th><th>状态</th></tr></thead>
                <tbody>{preview.connections.map((item) => <tr key={`${item.row}-${item.key}`} className={item.errors.length ? "invalid-row" : ""}><td>{item.row}</td><td>{item.key || "—"}</td><td>{item.name || "—"}</td><td>{item.type || "—"}</td><td>{item.host ? `${item.host}:${item.port || "—"}` : "—"}</td><td><Badge tone={item.errors.length ? "red" : "green"}>{item.errors.length ? "需修正" : "就绪"}</Badge></td></tr>)}</tbody>
              </ImportPreviewTable>
            )}
            {preview.schedules.length > 0 && (
              <ImportPreviewTable title="定时备份任务" count={preview.schedules.length}>
                <thead><tr><th>行</th><th>任务名称</th><th>连接标识</th><th>数据库</th><th>计划</th><th>状态</th></tr></thead>
                <tbody>{preview.schedules.map((item) => <tr key={`${item.row}-${item.name}`} className={item.errors.length ? "invalid-row" : ""}><td>{item.row}</td><td>{item.name || "—"}</td><td>{item.connectionKey || "—"}</td><td>{databaseListLabel(item.databases)}</td><td>{scheduleLabel(item.frequency, item.time, item.weekday, item.monthDay)}</td><td><Badge tone={item.errors.length ? "red" : "green"}>{item.errors.length ? "需修正" : "就绪"}</Badge></td></tr>)}</tbody>
              </ImportPreviewTable>
            )}
          </div>
        )}

        {error && <div className="form-error left">{error}</div>}
        <div className="modal-actions batch-import-actions">
          {result?.success ? <Button type="button" onClick={onClose}>完成</Button> : preview ? <><Button type="button" variant="secondary" disabled={loading} onClick={() => { setPreview(null); setResult(null); setError(""); }}>重新选择文件</Button><Button type="button" disabled={loading || !preview.valid} onClick={executeImport}>{loading ? <><LoaderCircle size={16} className="spin" />正在测试并导入...</> : "确认导入"}</Button></> : <><Button type="button" variant="secondary" disabled={loading} onClick={onClose}>取消</Button><Button type="button" disabled={loading || !file} onClick={validateFile}>{loading ? <><LoaderCircle size={16} className="spin" />正在校验...</> : "校验文件"}</Button></>}
        </div>
      </div>
    </div>
  );
}

function ImportPreviewTable({ title, count, children }: { title: string; count: number; children: ReactNode }) {
  return <section className="batch-import-table"><div><h4>{title}</h4><span>{count} 项</span></div><div className="batch-import-table-scroll"><table>{children}</table></div></section>;
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
