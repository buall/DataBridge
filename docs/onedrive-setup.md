# Microsoft OneDrive 远端存储接入指南

DataBridge 支持通过 **Microsoft Graph OAuth 2.0** 接入 **OneDrive 国际版（公有云）** 与 **OneDrive 世纪互联（中国区）**，用于数据库备份上传（≤ 4 MiB 直传，> 4 MiB 自动 10 MiB 分片断点续传）、下载、恢复及过期备份清理。

---

## 一、端点说明

在 DataBridge 中新建 OneDrive 存储时，请根据账号所属环境选择对应的 Microsoft Graph 端点：

| 环境 | Microsoft Graph 端点 | 登录/授权域名 | 控制台地址 |
| --- | --- | --- | --- |
| **公有云（国际版）** | `https://graph.microsoft.com/v1.0` | `login.microsoftonline.com` | [Microsoft Entra / Azure 国际版](https://portal.azure.com/#view/Microsoft_AAD_RegisteredApps/ApplicationsListBlade) |
| **世纪互联（中国区）** | `https://microsoftgraph.chinacloudapi.cn/v1.0` | `login.chinacloudapi.cn` | [Azure 中国区（世纪互联）](https://portal.azure.cn/#view/Microsoft_AAD_RegisteredApps/ApplicationsListBlade) |

---

## 二、接入步骤（OAuth 2.0 授权）

### 1. 注册 Microsoft Entra ID 应用

1. 登录 [Microsoft Entra 管理中心](https://entra.microsoft.com/) 或 [Azure 应用注册](https://portal.azure.com/#view/Microsoft_AAD_RegisteredApps/ApplicationsListBlade)（中国区请访问 `portal.azure.cn`）。
2. 进入 **应用注册（App registrations） → 新注册（New registration）**。
3. 填写应用名称（如 `DataBridge`）。
4. **受支持的账户类型**：
   - 若同时需要支持个人 Microsoft 账号（`@outlook.com` / `@hotmail.com`）与组织账号，请选择 **任何组织目录中的账户和个人 Microsoft 账户**。
   - 若仅限企业/学校内部租户使用，可选择 **任何组织目录中的账户** 或 **仅此组织目录中的账户**。
5. **重定向 URI** 可先留空，点击 **注册**。
6. 在应用 **概述（Overview）** 页面复制 **应用程序（客户端）ID**，填入 DataBridge 的对应输入框。

### 2. 创建客户端密钥（Client Secret）

1. 进入该应用的 **证书和密码（Certificates & secrets） → 客户端密码（Client secrets）**。
2. 点击 **+ 新客户端密码**，填写说明与有效期后点击 **添加**。
3. **立即复制「值（Value）」列的内容**（注意：**不要复制「机密 ID / Secret ID」**），填入 DataBridge 的 **客户端密钥** 输入框。

### 3. 授予委托权限（API Permissions）

1. 进入应用的 **API 权限（API permissions） → + 添加权限 → Microsoft Graph → 委托的权限（Delegated permissions）**。
2. 搜索并勾选以下两个权限：
   - `Files.ReadWrite.All`（读写 OneDrive 备份文件）
   - `offline_access`（获取 `refresh_token`，用于后台自动刷新访问令牌）
3. 点击 **添加权限**。若组织租户要求管理员同意，请点击 **代表管理员授予同意**。

### 4. 在 DataBridge 保存并配置重定向 URI

1. 在 DataBridge **远端存储** 表单中填入应用程序 ID、客户端密钥及 **默认目录**（如 `DataBridge Backups`，留空则存放在根目录），点击 **保存存储** 生成 `<存储 ID>`。
2. 回到 Entra ID 应用的 **身份验证（Authentication） → + 添加平台 → Web**。
3. 在 **重定向 URI** 中填入：
   ```text
   <DataBridge 访问地址>/api/storages/<存储 ID>/onedrive/callback
   ```
   - 本地开发环境示例：`http://localhost:5173/api/storages/<存储 ID>/onedrive/callback`
   - 生产环境如设置了 `DATABRIDGE_PUBLIC_URL`，请替换为对应的公网域名。
4. 点击 **配置** 保存。

### 5. 完成授权与连通性测试

1. 在 DataBridge 存储卡片右上角操作菜单中点击 **开始授权**。
2. 跳转到 Microsoft 登录与授权页，确认同意权限后将自动回调至 `/storages?onedrive=authorized`，状态变为 **可用（connected）**。
3. 点击操作菜单中的 **测试配置**，系统会向默认目录写入测试文件并立即删除，返回「OneDrive 存储测试通过」即可在备份任务中选用。

---

## 三、常见问题排查

1. **`AADSTS50011`：请求中指定的重定向 URI 与为应用程序配置的 URI 不匹配**
   - 检查 Entra ID「身份验证 → Web → 重定向 URI」是否与当前存储的回调地址完全一致（协议、域名、端口、`<存储 ID>`）。若使用反向代理，请配置 `DATABRIDGE_PUBLIC_URL`。

2. **`AADSTS7000215`：提供了无效的客户端密码**
   - 常见原因是误填了 **机密 ID（Secret ID）** 而不是 **值（Value）**，或客户端密钥已过期。请重新生成客户端密码并复制「值」列。

3. **`AADSTS50020`：用户账户不存在于租户中 / 不支持个人账号**
   - 注册应用时选择了「仅此组织目录中的账户」，导致个人 Microsoft 账号无法登录。可在应用清单或「身份验证」中将受支持的账户类型调整为包含个人 Microsoft 账号，或使用组织账号登录。

4. **长时间未运行后提示令牌刷新失败**
   - 检查是否已在 API 权限中添加 `offline_access`；缺少该权限时不会下发 `refresh_token`。添加后在 DataBridge 中重新点击一次 **开始授权** 即可。
