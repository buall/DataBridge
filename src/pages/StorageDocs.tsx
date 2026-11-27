import { BookOpen, Check, Cloud, Copy, ExternalLink } from "lucide-react";
import { useState } from "react";
import { Button, Card } from "../components/UI";

export type StorageDocType = "google_drive" | "onedrive";

export function StorageDocs({ docType }: { docType: StorageDocType }) {
  const params = new URLSearchParams(window.location.search);
  const storageId = params.get("storageId")?.trim() || "<存储 ID>";
  const initialGoogleTab = params.get("tab") === "service_account" ? "service_account" : "oauth";

  const [activeDoc, setActiveDoc] = useState<StorageDocType>(docType);
  const [googleTab, setGoogleTab] = useState<"oauth" | "service_account" | "faq">(initialGoogleTab);
  const [oneDriveTab, setOneDriveTab] = useState<"oauth" | "faq">("oauth");
  const [copiedKey, setCopiedKey] = useState("");

  function copyText(key: string, text: string) {
    void navigator.clipboard?.writeText(text);
    setCopiedKey(key);
    window.setTimeout(() => setCopiedKey(""), 2000);
  }

  function switchDoc(next: StorageDocType) {
    setActiveDoc(next);
    const nextPath = next === "google_drive" ? "/docs/google-drive" : "/docs/onedrive";
    const nextSearch = storageId !== "<存储 ID>" ? `?storageId=${encodeURIComponent(storageId)}` : "";
    window.history.replaceState(null, "", nextPath + nextSearch);
  }

  const googleCallbackURI = `${window.location.origin}/api/storages/${storageId}/google-drive/callback`;
  const oneDriveCallbackURI = `${window.location.origin}/api/storages/${storageId}/onedrive/callback`;

  return (
    <div style={{ minHeight: "100vh", background: "var(--bg)", color: "var(--text)", padding: "28px 20px 64px" }}>
      <div style={{ maxWidth: 920, margin: "0 auto", display: "grid", gap: 18 }}>
        <header
          style={{
            display: "flex",
            justifyContent: "space-between",
            alignItems: "center",
            gap: 16,
            flexWrap: "wrap",
            paddingBottom: 14,
            borderBottom: "1px solid var(--border)",
          }}
        >
          <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
            <div
              style={{
                width: 40,
                height: 40,
                borderRadius: 12,
                background: "var(--blue-soft)",
                color: "var(--blue)",
                display: "grid",
                placeItems: "center",
                flexShrink: 0,
              }}
            >
              <BookOpen size={20} />
            </div>
            <div>
              <h1 style={{ margin: 0, fontSize: 20, fontWeight: 700, letterSpacing: "-0.01em" }}>
                远端存储接入说明文档
              </h1>
              <div style={{ fontSize: 13, color: "var(--muted)", marginTop: 2 }}>
                DataBridge 云端存储配置指南 · 支持 Google Drive 与 Microsoft OneDrive
              </div>
            </div>
          </div>

          <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
            <Button
              type="button"
              variant={activeDoc === "google_drive" ? "primary" : "secondary"}
              onClick={() => switchDoc("google_drive")}
            >
              <Cloud size={14} />
              Google Drive
            </Button>
            <Button
              type="button"
              variant={activeDoc === "onedrive" ? "primary" : "secondary"}
              onClick={() => switchDoc("onedrive")}
            >
              <Cloud size={14} />
              OneDrive
            </Button>
            <Button
              type="button"
              variant="secondary"
              onClick={() => {
                window.location.assign("/storages");
              }}
            >
              返回远端存储
            </Button>
          </div>
        </header>

        {activeDoc === "google_drive" ? (
          <Card style={{ padding: "22px 24px", display: "grid", gap: 16, lineHeight: 1.7, fontSize: 13.5 }}>
            <div>
              <h2 style={{ margin: "0 0 6px", fontSize: 17, fontWeight: 650 }}>Google Drive 接入指南</h2>
              <div style={{ fontSize: 13, color: "var(--muted)" }}>
                支持 OAuth 2.0 授权（个人账号推荐）与服务账号（Google Workspace 共享云端硬盘推荐）两种认证方式。
              </div>
            </div>

            <div className="warn-callout" style={{ flexDirection: "column", gap: 4 }}>
              <div style={{ fontWeight: 600 }}>选型建议与配额限制说明</div>
              <div>
                • <strong>个人 Google 账号（@gmail.com）</strong>：请优先使用 <strong>方式一：OAuth 授权</strong>，备份文件计入你自己的 Google Drive 存储配额。
              </div>
              <div>
                • <strong>Google Workspace 共享云端硬盘（Shared Drive）</strong>：推荐使用 <strong>方式二：服务账号</strong>，无需浏览器交互即可全自动运行。
              </div>
              <div>
                • <strong>注意</strong>：Google 对个人免费版云盘中的服务账号写入有配额限制，服务账号向个人共享文件夹上传文件通常会触发{" "}
                <code className="mono">storageQuotaExceeded</code>。
              </div>
            </div>

            <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
              <Button
                type="button"
                variant={googleTab === "oauth" ? "primary" : "secondary"}
                onClick={() => setGoogleTab("oauth")}
              >
                方式一：OAuth 授权
              </Button>
              <Button
                type="button"
                variant={googleTab === "service_account" ? "primary" : "secondary"}
                onClick={() => setGoogleTab("service_account")}
              >
                方式二：服务账号
              </Button>
              <Button
                type="button"
                variant={googleTab === "faq" ? "primary" : "secondary"}
                onClick={() => setGoogleTab("faq")}
              >
                常见问题排查
              </Button>
            </div>

            {googleTab === "oauth" ? (
              <div style={{ display: "grid", gap: 14 }}>
                <DocStep
                  title="第 1 步：选择项目并启用 Google Drive API"
                  linkLabel="打开 Google Drive API 库"
                  linkHref="https://console.cloud.google.com/apis/library/drive.googleapis.com"
                >
                  <div>
                    1. 登录{" "}
                    <a href="https://console.cloud.google.com/" target="_blank" rel="noreferrer" style={{ color: "var(--blue)" }}>
                      Google Cloud Console
                    </a>
                    进入欢迎页，在顶部栏左侧点击项目选择器（如 <code className="mono">Default Gemini Project</code>）选择已有项目，或在弹窗中点击{" "}
                    <strong>新建项目</strong>。
                  </div>
                  <div>
                    2. 在欢迎页下方的 <strong>快速访问</strong> 卡片区点击 <strong>API 和服务</strong>，进入「已启用的 API 和服务」页面。
                  </div>
                  <div>
                    3. 点击页面顶部的 <strong>+ 启用 API 和服务</strong> 按钮（或左侧菜单 <strong>库</strong>），搜索{" "}
                    <strong>Google Drive API</strong>，进入后点击 <strong>启用</strong>，直到详情页显示「状态：已启用」（也可直接点击上方右侧「打开 Google Drive API 库」链接）。
                  </div>
                </DocStep>

                <DocStep
                  title="第 2 步：配置「Google Auth Platform」（品牌、目标对象、客户端与数据访问）"
                  linkLabel="打开 Google Auth Platform"
                  linkHref="https://console.cloud.google.com/auth/overview"
                >
                  <div>
                    1. 在左侧菜单点击 <strong>OAuth 权限请求页面</strong>，会自动进入 <strong>Google Auth Platform → 概览</strong>。若显示「尚未配置 Google Auth Platform」，点击页面中央的 <strong>开始</strong> 按钮。
                  </div>
                  <div>
                    2. 完成 <strong>品牌塑造</strong>（填写 <strong>应用名称</strong> 如 <code className="mono">DataBridge</code> 和 <strong>用户支持邮箱</strong>，「应用徽标」「应用网域」可留空）与 <strong>目标对象</strong>（个人账号选 <strong>外部</strong>，Workspace 组织内使用可选 <strong>内部</strong>；若应用处于「测试中」，务必在目标对象中添加 <strong>测试用户</strong>）。
                  </div>
                  <div>
                    3. 在左侧菜单{" "}
                    <a href="https://console.cloud.google.com/auth/clients" target="_blank" rel="noreferrer" style={{ color: "var(--blue)" }}>
                      <strong>客户端</strong>
                    </a>{" "}
                    中点击 <strong>+ 创建客户端</strong>（应用类型选 <strong>Web 应用</strong>），获取 <strong>客户端 ID</strong> 与 <strong>客户端密钥</strong>，并在 <strong>已获授权的重定向 URI</strong> 中添加回调地址（若刚创建存储，需先在 DataBridge 保存以生成 <code className="mono">&lt;存储 ID&gt;</code>，再将下方完整地址填入此处并保存）：
                    <CopyRow
                      code={googleCallbackURI}
                      copied={copiedKey === "gd-callback"}
                      buttonLabel="复制 URI"
                      onCopy={() => copyText("gd-callback", googleCallbackURI)}
                    />
                    <div style={{ fontSize: 12.5, color: "var(--muted)", marginTop: 4 }}>
                      提示：若生产环境部署在反向代理后且设置了 <code className="mono">DATABRIDGE_PUBLIC_URL</code>，请将上述域名替换为实际公网访问地址。
                    </div>
                  </div>
                  <div>
                    4. 在左侧菜单 <strong>数据访问</strong> 中点击 <strong>添加或移除范围</strong>，手动填入以下 Scope 并保存：
                    <CopyRow
                      code="https://www.googleapis.com/auth/drive"
                      copied={copiedKey === "gd-scope"}
                      buttonLabel="复制 Scope"
                      onCopy={() => copyText("gd-scope", "https://www.googleapis.com/auth/drive")}
                    />
                  </div>
                </DocStep>

                <DocStep title="第 3 步：在 DataBridge 保存配置、开始授权并测试">
                  <div>
                    1. 在 DataBridge <strong>远端存储</strong> 表单中填入上一步获取的 <strong>客户端 ID</strong>、<strong>客户端密钥</strong> 与 <strong>默认目录</strong>（如 <code className="mono">DataBridge Backups</code>，留空则存放在根目录；支持多级路径），点击 <strong>保存存储</strong>。
                  </div>
                  <div>
                    2. 确保第 2 步中 Google Auth Platform → <strong>客户端</strong> 已保存包含当前真实 <code className="mono">&lt;存储 ID&gt;</code> 的重定向 URI（保存后通常 1 分钟内生效）。
                  </div>
                  <div>
                    3. 打开该存储的 <strong>编辑配置</strong>（底部按钮）或列表操作菜单，点击 <strong>开始授权</strong> 完成 Google 登录同意；回调成功后状态变为 <strong>可用</strong>，再点击 <strong>测试配置</strong> 验证读写删除是否正常。
                  </div>
                </DocStep>
              </div>
            ) : null}

            {googleTab === "service_account" ? (
              <div style={{ display: "grid", gap: 14 }}>
                <DocStep
                  title="第 1 步：启用 Google Drive API"
                  linkLabel="打开 Google Drive API 库"
                  linkHref="https://console.cloud.google.com/apis/library/drive.googleapis.com"
                >
                  <div>
                    登录{" "}
                    <a href="https://console.cloud.google.com/" target="_blank" rel="noreferrer" style={{ color: "var(--blue)" }}>
                      Google Cloud Console
                    </a>
                    ，选择或新建项目，进入 <strong>API 和服务 → 库</strong> 搜索并启用 <strong>Google Drive API</strong>。
                  </div>
                </DocStep>

                <DocStep
                  title="第 2 步：创建服务账号并下载 JSON 密钥"
                  linkLabel="打开服务账号管理"
                  linkHref="https://console.cloud.google.com/iam-admin/serviceaccounts"
                >
                  <div>
                    1. 点击 <strong>+ 创建服务账号（Create service account）</strong>，填写名称（如 <code className="mono">databridge-backup</code>）后点击「创建并继续」；后续「授予此服务账号对项目的访问权限」可留空直接完成。
                  </div>
                  <div>
                    2. 在服务账号列表中点击刚创建的账号，切换到顶部 <strong>密钥（Keys）</strong> 标签页。
                  </div>
                  <div>
                    3. 点击 <strong>添加密钥 → 创建新密钥 → 选择 JSON → 创建</strong>，浏览器会自动下载一个 <code className="mono">.json</code> 密钥文件（内含 <code className="mono">client_email</code> 与 <code className="mono">private_key</code>）。
                  </div>
                </DocStep>

                <DocStep
                  title="第 3 步：在 Google Drive 中授予目录访问权限"
                  linkLabel="打开 Google Drive"
                  linkHref="https://drive.google.com/"
                >
                  <div>
                    1. 打开下载的 JSON 文件，复制其中的 <code className="mono">client_email</code>（形如 <code className="mono">databridge-backup@xxx.iam.gserviceaccount.com</code>）。
                  </div>
                  <div>
                    2. 在 Google Workspace 的目标 <strong>共享云端硬盘（Shared Drive）</strong> 上点击 <strong>管理成员</strong>，添加该 <code className="mono">client_email</code> 并授予 <strong>内容管理员（Content manager）</strong> 或 <strong>管理员</strong> 权限。
                  </div>
                  <div>
                    3. 若测试普通共享文件夹，请右键该文件夹 → <strong>共享</strong> → 添加 <code className="mono">client_email</code> 并设为 <strong>编辑者（Editor）</strong>，并确保文件夹名称与 DataBridge「默认目录」一致。
                  </div>
                </DocStep>

                <DocStep title="第 4 步：在 DataBridge 中填入密钥并测试">
                  <div>
                    1. 在 DataBridge「授权方式」中选择 <strong>服务账号</strong>，用文本编辑器打开 <code className="mono">.json</code> 文件并粘贴<strong>完整 JSON 内容</strong>到「服务账号 JSON 密钥」输入框。
                  </div>
                  <div>
                    2. 填写 <strong>默认目录</strong>（如 <code className="mono">DataBridge Backups</code>），点击 <strong>保存存储</strong>（密钥会在后端加密持久化）。
                  </div>
                  <div>
                    3. 点击卡片操作菜单中的 <strong>测试配置</strong>，系统将使用 RS256 JWT 自动换取访问令牌并执行写入/删除校验，通过后即可用于备份。
                  </div>
                </DocStep>
              </div>
            ) : null}

            {googleTab === "faq" ? (
              <div style={{ display: "grid", gap: 12 }}>
                <DocStep title="Error 400: redirect_uri_mismatch">
                  <div>
                    Google Cloud Console 中的「已获授权的重定向 URI」必须与当前存储的回调地址逐字匹配（包含协议、域名、端口和存储 ID，末尾不能有多余斜杠）。修改后通常需要等待 1~5 分钟生效；生产环境若使用反向代理，请设置环境变量 <code className="mono">DATABRIDGE_PUBLIC_URL</code>。
                  </div>
                </DocStep>
                <DocStep title="Error 403: access_denied">
                  <div>
                    OAuth 同意屏幕处于「测试中（Testing）」状态时，仅允许在{" "}
                    <a href="https://console.cloud.google.com/apis/credentials/consent" target="_blank" rel="noreferrer" style={{ color: "var(--blue)" }}>
                      OAuth 同意屏幕 → 测试用户
                    </a>{" "}
                    中显式添加的 Google 账号完成授权。
                  </div>
                </DocStep>
                <DocStep title="storageQuotaExceeded（Service Accounts do not have storage quota）">
                  <div>
                    个人免费版 Google Drive 中，服务账号没有独立存储空间。请改用 <strong>方式一：OAuth 授权</strong>，或将目标目录迁移至 <strong>Google Workspace 共享云端硬盘</strong>。
                  </div>
                </DocStep>
                <DocStep title="测试中应用 7 天授权过期（invalid_grant）">
                  <div>
                    测试状态的 OAuth 应用颁发的 <code className="mono">refresh_token</code> 会在 7 天后失效。建议在 Google Auth Platform 中将应用发布状态切换为 <strong>正式版（In production）</strong>（自用场景无需提交公开验证），或在过期后重新点击「开始授权」。
                  </div>
                </DocStep>
                <DocStep title="国内网络环境连接超时">
                  <div>
                    请确保运行 DataBridge 后端的机器能够访问 <code className="mono">oauth2.googleapis.com</code> 与{" "}
                    <code className="mono">www.googleapis.com</code>（可在启动后端前配置 <code className="mono">HTTPS_PROXY</code> 环境变量）。
                  </div>
                </DocStep>
              </div>
            ) : null}
          </Card>
        ) : (
          <Card style={{ padding: "22px 24px", display: "grid", gap: 16, lineHeight: 1.7, fontSize: 13.5 }}>
            <div>
              <h2 style={{ margin: "0 0 6px", fontSize: 17, fontWeight: 650 }}>Microsoft OneDrive 接入指南</h2>
              <div style={{ fontSize: 13, color: "var(--muted)" }}>
                支持 Microsoft OneDrive 公有云（国际版）与世纪互联（中国区）OAuth 2.0 授权，支持 ≤4 MiB 直传与 &gt;4 MiB 分片断点续传。
              </div>
            </div>

            <div className="info-callout" style={{ flexDirection: "column", gap: 4 }}>
              <div style={{ fontWeight: 600 }}>端点与控制台对照</div>
              <div>
                • <strong>公有云（国际版）</strong>：端点 <code className="mono">https://graph.microsoft.com/v1.0</code>，控制台{" "}
                <a
                  href="https://portal.azure.com/#view/Microsoft_AAD_RegisteredApps/ApplicationsListBlade"
                  target="_blank"
                  rel="noreferrer"
                  style={{ color: "var(--blue)", textDecoration: "underline" }}
                >
                  Azure / Entra ID（国际版）
                </a>
              </div>
              <div>
                • <strong>世纪互联（中国区）</strong>：端点 <code className="mono">https://microsoftgraph.chinacloudapi.cn/v1.0</code>，控制台{" "}
                <a
                  href="https://portal.azure.cn/#view/Microsoft_AAD_RegisteredApps/ApplicationsListBlade"
                  target="_blank"
                  rel="noreferrer"
                  style={{ color: "var(--blue)", textDecoration: "underline" }}
                >
                  Azure（中国区）
                </a>
              </div>
            </div>

            <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
              <Button
                type="button"
                variant={oneDriveTab === "oauth" ? "primary" : "secondary"}
                onClick={() => setOneDriveTab("oauth")}
              >
                OAuth 接入步骤
              </Button>
              <Button
                type="button"
                variant={oneDriveTab === "faq" ? "primary" : "secondary"}
                onClick={() => setOneDriveTab("faq")}
              >
                常见问题排查
              </Button>
            </div>

            {oneDriveTab === "oauth" ? (
              <div style={{ display: "grid", gap: 14 }}>
                <DocStep
                  title="第 1 步：在 Microsoft Entra ID 注册应用"
                  linkLabel="打开应用注册"
                  linkHref="https://portal.azure.com/#view/Microsoft_AAD_RegisteredApps/ApplicationsListBlade"
                >
                  <div>
                    1. 登录{" "}
                    <a href="https://entra.microsoft.com/" target="_blank" rel="noreferrer" style={{ color: "var(--blue)" }}>
                      Microsoft Entra 管理中心
                    </a>
                    （中国区请登录{" "}
                    <a href="https://portal.azure.cn/" target="_blank" rel="noreferrer" style={{ color: "var(--blue)" }}>
                      Azure 中国区
                    </a>
                    ），进入 <strong>应用注册（App registrations） → 新注册</strong>。
                  </div>
                  <div>
                    2. 填写名称（如 <code className="mono">DataBridge</code>），「受支持的账户类型」如需支持个人账号（Outlook / Hotmail）请选{" "}
                    <strong>任何组织目录中的账户和个人 Microsoft 账户</strong>。
                  </div>
                  <div>
                    3. 点击注册后，在应用 <strong>概述（Overview）</strong> 复制 <strong>应用程序（客户端）ID</strong> 填入 DataBridge。
                  </div>
                </DocStep>

                <DocStep title="第 2 步：创建客户端密码（Client Secret）">
                  <div>
                    1. 进入应用的 <strong>证书和密码（Certificates &amp; secrets） → 客户端密码 → + 新客户端密码</strong>。
                  </div>
                  <div>
                    2. 创建后务必复制 <strong>「值（Value）」</strong> 列的内容填入 DataBridge「客户端密钥」（注意：<strong>不要复制「机密 ID」</strong>）。
                  </div>
                </DocStep>

                <DocStep title="第 3 步：授予 Microsoft Graph 委托权限">
                  <div>
                    进入 <strong>API 权限 → + 添加权限 → Microsoft Graph → 委托的权限（Delegated permissions）</strong>，勾选并添加：
                  </div>
                  <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap", marginTop: 2 }}>
                    <code className="mono" style={{ padding: "3px 8px", borderRadius: 6, background: "var(--surface-2, rgba(15, 23, 42, 0.05))" }}>
                      Files.ReadWrite.All
                    </code>
                    <code className="mono" style={{ padding: "3px 8px", borderRadius: 6, background: "var(--surface-2, rgba(15, 23, 42, 0.05))" }}>
                      offline_access
                    </code>
                  </div>
                  <div style={{ fontSize: 12.5, color: "var(--muted)" }}>
                    提示：<code className="mono">offline_access</code> 用于下发 <code className="mono">refresh_token</code>，确保后台定时备份可持续自动刷新令牌。
                  </div>
                </DocStep>

                <DocStep title="第 4 步：保存配置并添加 Web 重定向 URI">
                  <div>
                    1. 在 DataBridge 填写应用程序 ID、客户端密钥及默认目录（如 <code className="mono">DataBridge Backups</code>，留空存放在根目录）并点击 <strong>保存存储</strong>。
                  </div>
                  <div>
                    2. 在 Entra ID 应用的 <strong>身份验证（Authentication） → + 添加平台 → Web</strong> 中添加以下重定向 URI：
                    <CopyRow
                      code={oneDriveCallbackURI}
                      copied={copiedKey === "od-callback"}
                      buttonLabel="复制 URI"
                      onCopy={() => copyText("od-callback", oneDriveCallbackURI)}
                    />
                  </div>
                  <div>
                    3. 在 DataBridge 存储卡片菜单点击 <strong>开始授权</strong> 完成登录同意，再点击 <strong>测试配置</strong> 验证上传与删除正常即可。
                  </div>
                </DocStep>
              </div>
            ) : null}

            {oneDriveTab === "faq" ? (
              <div style={{ display: "grid", gap: 12 }}>
                <DocStep title="AADSTS50011：重定向 URI 不匹配">
                  <div>
                    检查 Entra ID「身份验证 → Web → 重定向 URI」是否与当前存储 ID、协议和端口完全一致；反向代理部署请配置 <code className="mono">DATABRIDGE_PUBLIC_URL</code>。
                  </div>
                </DocStep>
                <DocStep title="AADSTS7000215：无效的客户端密码">
                  <div>
                    常见原因是误复制了「机密 ID（Secret ID）」而非「值（Value）」，或密码已过期。请重新创建客户端密码并复制「值」列。
                  </div>
                </DocStep>
                <DocStep title="AADSTS50020：个人账号无法登录">
                  <div>
                    应用注册时受支持的账户类型选成了「仅此组织目录」，请调整为包含个人 Microsoft 账号，或使用组织账号授权。
                  </div>
                </DocStep>
                <DocStep title="令牌过期后无法自动刷新">
                  <div>
                    请确认在 API 权限中已添加 <code className="mono">offline_access</code>，并在 DataBridge 中重新点击一次「开始授权」。
                  </div>
                </DocStep>
              </div>
            ) : null}
          </Card>
        )}
      </div>
    </div>
  );
}

function DocStep({
  title,
  linkLabel,
  linkHref,
  children,
}: {
  title: string;
  linkLabel?: string;
  linkHref?: string;
  children: React.ReactNode;
}) {
  return (
    <div style={{ padding: "14px 16px", borderRadius: 10, border: "1px solid var(--border)", display: "grid", gap: 6 }}>
      <div style={{ fontWeight: 600, display: "flex", justifyContent: "space-between", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
        <span>{title}</span>
        {linkHref && linkLabel ? (
          <a
            href={linkHref}
            target="_blank"
            rel="noreferrer"
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: 4,
              fontSize: 12.5,
              color: "var(--blue)",
              fontWeight: 500,
              textDecoration: "none",
            }}
          >
            {linkLabel} <ExternalLink size={13} />
          </a>
        ) : null}
      </div>
      <div style={{ display: "grid", gap: 4 }}>{children}</div>
    </div>
  );
}

function CopyRow({
  code,
  copied,
  buttonLabel,
  onCopy,
}: {
  code: string;
  copied: boolean;
  buttonLabel: string;
  onCopy: () => void;
}) {
  return (
    <div
      style={{
        marginTop: 6,
        padding: "8px 10px",
        borderRadius: 8,
        background: "var(--surface-2, rgba(15, 23, 42, 0.05))",
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        gap: 8,
        flexWrap: "wrap",
      }}
    >
      <code className="mono" style={{ wordBreak: "break-all" }}>
        {code}
      </code>
      <Button type="button" variant="secondary" style={{ height: 28, padding: "0 10px", fontSize: 12, flexShrink: 0 }} onClick={onCopy}>
        {copied ? <Check size={12} /> : <Copy size={12} />}
        {copied ? "已复制" : buttonLabel}
      </Button>
    </div>
  );
}
