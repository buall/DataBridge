# Google Drive 远端存储接入指南

DataBridge 支持通过 **OAuth 授权** 与 **服务账号（Service Account）** 两种方式接入 Google Drive，用于数据库备份上传、备份下载、恢复以及过期备份清理。

---

## 一、两种授权方式对比与选型建议

| 对比项 | 方式一：OAuth 授权 | 方式二：服务账号（Service Account） |
| --- | --- | --- |
| **适用账号** | 个人 Google 账号（`@gmail.com`）及 Google Workspace | Google Workspace（配合共享云端硬盘 Shared Drive） |
| **授权流程** | 需保存配置后在浏览器跳转 Google 登录授权一次 | 粘贴 JSON 密钥即可，无需浏览器跳转 |
| **存储配额归属** | 使用授权用户的 Google Drive 配额 | 使用服务账号自身配额（个人云盘场景受限，详见下方说明） |
| **自动化/无头部署** | 首次授权后通过 `refresh_token` 自动刷新，无需重复登录 | 全程非交互式 JWT 认证，适合自动化运维 |
| **推荐场景** | **个人账号备份首选** | **企业级 Workspace 共享云端硬盘首选** |

> **重要提示（关于服务账号的配额限制）**：
> Google 对个人免费版 Google Drive（My Drive）中的服务账号写入存在严格配额限制（普通服务账号默认无独立存储配额，向个人共享文件夹写入文件可能触发 `storageQuotaExceeded` 错误）。如果你使用的是**个人 Google 账号**，强烈建议选择 **OAuth 授权**；如果你使用的是 **Google Workspace 共享云端硬盘（Shared Drive）**，则推荐使用 **服务账号**。

---

## 二、前置准备：选择项目并启用 Google Drive API

两种方式均需在 Google Cloud Console 中完成以下准备：

1. 登录 [Google Cloud Console](https://console.cloud.google.com/) 进入欢迎页。
2. 在顶部栏左侧点击项目选择器（如 `Default Gemini Project`）选择已有项目，或在弹窗中点击 **新建项目**。
3. 在欢迎页下方的 **快速访问** 卡片区点击 **API 和服务**，进入「已启用的 API 和服务」页面。
4. 点击页面顶部的 **+ 启用 API 和服务**（或左侧菜单 **库**），搜索 **Google Drive API** 并点击 **启用**，确认详情页显示「状态：已启用」（也可直接访问 [Google Drive API 库](https://console.cloud.google.com/apis/library/drive.googleapis.com)）。

---

## 三、方式一：OAuth 授权（推荐个人账号使用）

### 1. 配置「Google Auth Platform」（品牌、目标对象、客户端与数据访问）

1. 在左侧菜单点击 **OAuth 权限请求页面**，会自动进入 **Google Auth Platform → 概览**（也可直接访问 [Google Auth Platform](https://console.cloud.google.com/auth/overview)）。若提示「尚未配置 Google Auth Platform」，点击页面中央的 **开始** 按钮。
2. 完成 **品牌塑造** 与 **目标对象**：
   - **品牌塑造**：填写 **应用名称**（如 `DataBridge`）和 **用户支持邮箱**（`应用徽标`、`应用网域` 可留空）。
   - **目标对象**：Google Workspace 组织内使用可选择 **内部**；个人账号或跨账号使用选择 **外部**，并填写开发者联系邮箱。若应用处于「测试中」状态，务必在 **测试用户** 中添加你的 Google 账号邮箱，否则授权时会报 `403: access_denied`。
3. 在左侧菜单 **客户端**（`https://console.cloud.google.com/auth/clients`）中点击 **+ 创建客户端**，应用类型选择 **Web 应用**，获取 **客户端 ID** 与 **客户端密钥**；同时在 **已获授权的重定向 URI** 中配置回调地址：
   ```text
   <DataBridge 访问地址>/api/storages/<存储 ID>/google-drive/callback
   ```
   - 若尚未在 DataBridge 中创建存储，可先在 DataBridge 填入客户端 ID/密钥并点击 **保存存储** 生成 `<存储 ID>`，再回到此页面填入完整回调地址并保存。
   - 本地开发环境示例：`http://localhost:5173/api/storages/<存储 ID>/google-drive/callback`；生产环境如配置了 `DATABRIDGE_PUBLIC_URL`（如 `https://backup.example.com`），请使用该公网域名。
4. 在左侧菜单 **数据访问** 中点击 **添加或移除范围**，手动添加以下权限范围并保存：
   ```text
   https://www.googleapis.com/auth/drive
   ```

### 2. 在 DataBridge 保存配置、开始授权并测试

1. 在 DataBridge **远端存储** 表单中填入 **客户端 ID**、**客户端密钥** 与 **默认目录**（如 `DataBridge Backups`，留空存放在根目录），点击 **保存存储**。
2. 确认 Google Auth Platform → **客户端** 中已保存包含真实 `<存储 ID>` 的重定向 URI（保存后通常需等待约 1 分钟生效）。
3. 在编辑弹窗底部或列表操作菜单中点击 **开始授权** 完成 Google 登录与授权，回调成功后状态变为 **可用（connected）**；随后点击 **测试配置** 验证连通性即可。

---

## 四、方式二：服务账号（Service Account）

### 1. 创建服务账号并生成 JSON 密钥

1. 在 Google Cloud Console 中进入 **API 和服务 → 凭据（Credentials）**（或 **IAM 与管理 → 服务账号**）。
2. 点击 **创建凭据 → 服务账号（Service account）**，填写服务账号名称（如 `databridge-backup`）并完成创建。
3. 点击新建的服务账号进入详情页，切换到 **密钥（Keys）** 标签页。
4. 点击 **添加密钥 → 创建新密钥（Create new key）**，类型选择 **JSON**，下载生成的 `.json` 密钥文件。
5. 记录 JSON 中的 `client_email` 字段（形如 `databridge-backup@your-project.iam.gserviceaccount.com`）。

### 2. 在 Google Drive 中授权目标目录

1. 打开 Google Drive：
   - **Workspace 共享云端硬盘（推荐）**：进入目标共享云端硬盘，点击 **管理成员**，添加上述 `client_email` 并赋予 **内容管理员（Content manager）** 或 **管理员** 权限。
   - **普通文件夹**：右键目标文件夹（需与 DataBridge 中的「默认目录」名称一致，如 `DataBridge Backups`）→ **共享**，添加上述 `client_email` 并赋予 **编辑者（Editor）** 权限。

### 3. 在 DataBridge 中配置并测试

1. 打开 DataBridge 的 **远端存储** 页面，点击 **新建存储**。
2. **存储类型**选择 `Google Drive`，**授权方式**选择 `服务账号`。
3. 用文本编辑器打开下载的 `.json` 文件，将**完整 JSON 内容**粘贴到 **服务账号 JSON 密钥** 输入框中（系统会自动校验 `client_email` 和 `private_key` 格式）。
4. 填写 **默认目录**（如 `DataBridge Backups`），点击 **保存**。
5. 点击操作菜单中的 **测试配置**，通过后状态将更新为 **可用（connected）**。

---

## 五、常见问题排查

1. **授权时提示 `Error 400: redirect_uri_mismatch`**
   - 检查 Google Cloud Console 中填写的重定向 URI 是否与 DataBridge 实际使用的完全一致（协议 `http/https`、域名/端口、`<存储 ID>`、末尾无多余斜杠）。
   - 如果通过反向代理部署，建议显式设置环境变量 `DATABRIDGE_PUBLIC_URL` 为外部访问地址（例如 `https://databridge.example.com`）。

2. **授权时提示 `Error 403: access_denied`**
   - OAuth 同意屏幕处于「测试中」状态时，需将当前登录的 Google 账号添加到 **测试用户** 列表中。

3. **测试或备份时提示 `storageQuotaExceeded`（The user's Drive storage quota has been exceeded）**
   - 常见于使用**服务账号**向个人版 Google Drive（非 Workspace 共享云端硬盘）写入文件。请改用 **OAuth 授权**，或改用 Google Workspace 共享云端硬盘。

4. **OAuth 长时间未使用后提示令牌失效（`invalid_grant`）**
   - 如果 Google Cloud 的 OAuth 同意屏幕处于「测试中」状态，Google 颁发的 `refresh_token` 有效期为 **7 天**。
   - 长期稳定运行建议将应用发布为「正式版（In production）」（仅自己使用无需提交 Google 公开审核，或直接使用内部应用），或者在过期后重新点击一次 **开始授权**。

5. **国内网络环境下超时或无法连接 `oauth2.googleapis.com` / `www.googleapis.com`**
   - 请确保运行 DataBridge 后端服务的机器具备正常访问 Google API 的网络或代理环境（例如配置 `HTTPS_PROXY` 环境变量）。
