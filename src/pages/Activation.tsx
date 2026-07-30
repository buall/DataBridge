import { Check, Clipboard, Download, FileKey, KeyRound, ShieldCheck, Upload } from "lucide-react";
import { FormEvent, useState } from "react";
import { api, type LicenseStatus } from "../api";
import { Button } from "../components/UI";

type ActivationMode = "online" | "offline";

export function Activation({ status, onActivated }: { status: LicenseStatus; onActivated: (status: LicenseStatus) => void }) {
  const [mode, setMode] = useState<ActivationMode>(status.onlineActivation ? "online" : "offline");
  const [activationCode, setActivationCode] = useState("");
  const [selectedFile, setSelectedFile] = useState<File | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState(status.state === "invalid" || status.state === "expired" ? status.message : "");
  const [copied, setCopied] = useState(false);

  async function activateOnline(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSubmitting(true);
    setError("");
    try {
      onActivated(await api.activateLicense(activationCode.trim()));
    } catch (activationError) {
      setError(activationError instanceof Error ? activationError.message : "在线激活失败");
    } finally {
      setSubmitting(false);
    }
  }

  async function importOffline(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!selectedFile) return;
    setSubmitting(true);
    setError("");
    try {
      onActivated(await api.importLicense(selectedFile));
    } catch (activationError) {
      setError(activationError instanceof Error ? activationError.message : "许可证导入失败");
    } finally {
      setSubmitting(false);
    }
  }

  async function downloadRequest() {
    setError("");
    try {
      const { blob, fileName } = await api.licenseRequest();
      const href = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = href;
      anchor.download = fileName;
      anchor.click();
      URL.revokeObjectURL(href);
    } catch (downloadError) {
      setError(downloadError instanceof Error ? downloadError.message : "申请文件下载失败");
    }
  }

  async function copyDeploymentId() {
    await navigator.clipboard.writeText(status.deploymentId);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1600);
  }

  return (
    <main className="activation-page">
      <section className="activation-shell" aria-labelledby="activation-title">
        <header className="activation-header">
          <div className="login-brand">
            <div className="brand-mark" aria-hidden="true">DB</div>
            <div><strong>DataBridge</strong><span>DEPLOYMENT ACTIVATION</span></div>
          </div>
          <span className="activation-state"><ShieldCheck size={15} />等待授权</span>
        </header>

        <div className="activation-intro">
          <span className="activation-icon"><KeyRound size={22} /></span>
          <div>
            <h1 id="activation-title">激活当前部署</h1>
            <p>许可证将绑定这套部署。激活后可在不联网的状态下持续运行。</p>
          </div>
        </div>

        <div className="deployment-identity">
          <div><span>部署 ID</span><strong>{status.deploymentId}</strong></div>
          <button type="button" className="plain-icon deployment-copy" onClick={copyDeploymentId} title="复制部署 ID" aria-label="复制部署 ID">
            {copied ? <Check size={17} /> : <Clipboard size={17} />}
          </button>
        </div>

        <div className="activation-tabs" role="tablist" aria-label="激活方式">
          <button type="button" className={mode === "online" ? "active" : ""} onClick={() => { setMode("online"); setError(""); }} role="tab" aria-selected={mode === "online"}>在线激活</button>
          <button type="button" className={mode === "offline" ? "active" : ""} onClick={() => { setMode("offline"); setError(""); }} role="tab" aria-selected={mode === "offline"}>离线激活</button>
        </div>

        {mode === "online" ? (
          <form className="activation-form" onSubmit={activateOnline}>
            <div className="activation-section-heading"><KeyRound size={18} /><div><strong>输入一次性激活码</strong><span>激活码成功兑换后将与当前部署绑定</span></div></div>
            <label className="field activation-code-field">
              激活码
              <input autoComplete="off" spellCheck={false} value={activationCode} onChange={(event) => setActivationCode(event.target.value.toUpperCase())} placeholder="DB-XXXX-XXXX-XXXX" />
            </label>
            {!status.onlineActivation ? <div className="activation-notice">当前服务未配置在线激活地址，请切换到离线激活。</div> : null}
            {error ? <div className="form-error left">{error}</div> : null}
            <Button type="submit" className="activation-primary" disabled={submitting || !activationCode.trim() || !status.onlineActivation}>
              <ShieldCheck size={17} />{submitting ? "正在激活..." : "激活部署"}
            </Button>
          </form>
        ) : (
          <form className="activation-form" onSubmit={importOffline}>
            <div className="offline-steps">
              <div className="offline-step"><span>1</span><div><strong>导出申请文件</strong><small>将申请文件发送给许可证签发方</small></div><Button type="button" variant="secondary" onClick={downloadRequest}><Download size={16} />下载</Button></div>
              <div className="offline-step"><span>2</span><div><strong>导入许可证</strong><small>{selectedFile ? selectedFile.name : "选择签发后的 license.json"}</small></div><label className="button secondary activation-file-button"><Upload size={16} />选择文件<input type="file" accept="application/json,.json,.license" onChange={(event) => setSelectedFile(event.target.files?.[0] ?? null)} /></label></div>
            </div>
            {error ? <div className="form-error left">{error}</div> : null}
            <Button type="submit" className="activation-primary" disabled={submitting || !selectedFile}>
              <FileKey size={17} />{submitting ? "正在校验..." : "导入并激活"}
            </Button>
          </form>
        )}
      </section>
    </main>
  );
}
