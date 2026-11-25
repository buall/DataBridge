# DataBridge Docker Compose 部署指南

Docker Compose 适合在单台服务器上长期运行 DataBridge，通过配置文件统一管理镜像、登录凭证、端口和数据卷。仓库根目录的 [docker-compose.yml](../docker-compose.yml) 已包含自动重启策略和健康检查。

该配置只启动 DataBridge，源数据库、目标数据库和远程备份存储需要提前准备。前端、API 和数据库客户端工具都包含在 DataBridge 镜像中，使用发布镜像时无需在宿主机安装 Node.js、Go 或数据库客户端。

其他部署方式见 [README 部署方式](../README.md#部署方式)；裸机部署见 [手动部署指南](manual-deployment.md)。

## 1. 部署前准备

- 安装 Docker Engine 和支持 `docker compose` 命令的 Compose 插件；Docker Desktop 用户可直接使用其内置 Compose。
- 确认宿主机能拉取镜像，容器网络能访问源数据库、目标数据库和备份存储。
- 确认宿主机上的 `8081` 端口可用；也可以在配置中改用其他端口。
- 为 `/data` 预留空间：除了 SQLite 和凭证密钥，备份、恢复的临时文件也会使用该目录。容量应结合数据库大小和并发任务数评估。

检查安装：

```bash
docker version
docker compose version
```

以下命令均在仓库根目录执行。如果只使用发布镜像，也可以将 `docker-compose.yml` 和 `.env.example` 复制到服务器上的同一部署目录，在该目录中执行命令；只有自行构建镜像时才需要完整源码。

## 2. 创建配置文件

首次部署时复制示例文件；已有 `.env` 时直接编辑，避免覆盖原有配置：

```bash
cp .env.example .env
chmod 600 .env
```

分别生成登录密码和会话签名密钥：

```bash
openssl rand -hex 24
openssl rand -base64 32
```

编辑 `.env`，将两次生成的结果分别填入 `DATABRIDGE_AUTH_PASSWORD` 和 `DATABRIDGE_SESSION_SECRET`。不要把 `$(openssl ...)` 命令本身写进 `.env`，Compose 不会在该文件中执行 Shell 命令。

配置项说明：

| 配置项 | 默认值 | 用途 |
| --- | --- | --- |
| `DATABRIDGE_IMAGE` | `hkccr.ccs.tencentyun.com/buall/databridge:latest` | 镜像地址；需要可重复部署时，使用已发布的固定标签或镜像 digest |
| `DATABRIDGE_BIND_ADDRESS` | `0.0.0.0` | 宿主机监听地址；仅允许本机反向代理访问时改为 `127.0.0.1` |
| `DATABRIDGE_PORT` | `8081` | 宿主机端口；容器内服务和健康检查仍使用 `8081` |
| `DATABRIDGE_AUTH_USER` | `admin` | 控制台登录用户名 |
| `DATABRIDGE_AUTH_PASSWORD` | 无，必填 | 控制台登录密码 |
| `DATABRIDGE_SESSION_SECRET` | 无，必填 | 登录会话签名密钥；保持固定，避免重建容器后原有会话失效 |
| `DATABRIDGE_PG_JOBS` | `4` | PostgreSQL 原生工具引擎的并行恢复数 |
| `DATABRIDGE_COOKIE_SECURE` | `false` | 浏览器通过 HTTPS 访问时设为 `true` |
| `DATABRIDGE_PUBLIC_URL` | 空 | 对外访问地址，例如 `https://databridge.example.com`，用于 OneDrive OAuth 回调 |
| `DATABRIDGE_CORS_ORIGIN` | 空 | 前端独立部署时允许的来源；使用镜像内置前端时无需设置 |

`.env` 为 Compose 提供变量，只有 `docker-compose.yml` 中映射到容器的配置项才会传入 DataBridge。宿主机监听地址、映射端口和镜像地址由 Compose 使用，不是 API 进程的监听配置。容器内的数据路径固定为：

- `DATABRIDGE_DB_PATH=/data/databridge.db`
- `DATABRIDGE_KEY_PATH=/data/databridge.key`
- `DATABRIDGE_BACKUP_WORK_DIR=/data/tmp`

当前 Shell 中已导出的同名变量会覆盖 `.env`。如果修改文件后结果不符，检查是否曾执行过 `export DATABRIDGE_...`。`.env` 已加入 Git 和 Docker 构建上下文的忽略列表，实际文件仍应按凭证文件保管。

## 3. 拉取并启动

先验证配置，再拉取镜像并启动：

```bash
docker compose config --quiet
docker compose pull databridge
docker compose up -d
docker compose ps
docker compose logs --tail=100 databridge
```

必填密码或会话密钥为空时，配置检查会直接报错。`config --quiet` 只验证配置，不输出包含密码的完整配置。

首次启动会初始化 SQLite 并生成凭证加密密钥。容器启动后稍等健康检查执行，`docker compose ps` 中应显示 `healthy`。也可以主动检查 API：

```bash
curl -fsS http://127.0.0.1:8081/api/healthz
```

预期返回 `{"status":"ok"}`。如果改过宿主机端口，请相应修改 URL。健康检查不需要登录，只表示 API 进程可响应，不代表数据库连接、迁移或远程备份存储已经验证成功。

浏览器访问 `http://服务器地址:8081`，使用 `.env` 中配置的用户名和密码登录；本机部署可访问 `http://127.0.0.1:8081`。如果监听地址设置为 `127.0.0.1`，远程浏览器需要通过反向代理访问。

登录后先创建并测试数据库连接，再配置备份存储。迁移前应在目标端准备好目标数据库，并确认数据库账号权限满足对应操作要求。

## 4. 日常管理与配置变更

| 操作 | 命令 |
| --- | --- |
| 查看状态 | `docker compose ps` |
| 持续查看日志 | `docker compose logs -f --tail=100 databridge` |
| 临时停止 | `docker compose stop databridge` |
| 启动已停止的容器 | `docker compose start databridge` |
| 重启当前容器 | `docker compose restart databridge` |
| 应用 `.env` 或 Compose 配置变更 | `docker compose up -d` |
| 删除容器和默认网络，保留命名数据卷 | `docker compose down` |

修改密码、端口、会话密钥或 HTTPS 配置后，执行 `docker compose config --quiet` 和 `docker compose up -d`。仅执行 `restart` 不会更新容器环境变量。

`restart: unless-stopped` 会在进程退出或 Docker 重启后重新启动服务；手动停止的容器需要显式启动。重建或停止服务前，先等待正在运行的迁移、备份和恢复任务结束，进程重启会中断任务。

## 5. 使用本地构建的镜像

在包含完整源码的仓库根目录构建：

```bash
docker build -t databridge:local .
```

将 `.env` 中的镜像改为：

```dotenv
DATABRIDGE_IMAGE=databridge:local
```

使用本地镜像启动，跳过远程拉取：

```bash
docker compose config --quiet
docker compose up -d --pull never
```

当前 Compose 文件没有 `build` 配置，因此需要先执行 `docker build`。后续源码更新后，重新构建并执行上述启动命令。

## 6. 连接宿主机或其他容器中的数据库

在控制台填写的数据库地址由 DataBridge 容器访问。`localhost` / `127.0.0.1` 指向 DataBridge 容器自身，不能用来连接宿主机上的数据库。

- 数据库在其他服务器：填写容器可访问的 IP 或域名，检查数据库监听地址、防火墙、访问白名单和 TLS 配置。
- 数据库在 Docker Desktop 宿主机：可使用 `host.docker.internal`。
- 数据库在 Linux 宿主机：可在部署目录新建 `docker-compose.override.yml`，添加以下映射，再执行 `docker compose up -d`：

```yaml
services:
  databridge:
    extra_hosts:
      - "host.docker.internal:host-gateway"
```

数据库还必须监听容器可达的宿主机地址；仅监听宿主机 `127.0.0.1` 的数据库通常无法通过 Docker 网关访问。

如果数据库位于另一个 Docker 网络，将 DataBridge 加入相同网络后，可通过数据库容器的服务名或网络别名连接。例如，现有数据库网络名为 `database-network` 时，可将以下内容合并到 `docker-compose.override.yml`：

```yaml
services:
  databridge:
    networks:
      - default
      - database

networks:
  database:
    external: true
    name: database-network
```

该网络必须已经存在。容器之间使用数据库容器的内部端口，而非宿主机发布端口；例如 PostgreSQL 通常使用 `5432`。如果同时需要宿主机映射和共享网络，将两个示例合并到同一个 `services.databridge` 配置下。

## 7. HTTPS 与反向代理

如果 Nginx、Caddy 或其他代理已经提供 HTTPS，将 `.env` 调整为：

```dotenv
DATABRIDGE_BIND_ADDRESS=127.0.0.1
DATABRIDGE_PORT=8081
DATABRIDGE_COOKIE_SECURE=true
DATABRIDGE_PUBLIC_URL=https://databridge.example.com
```

代理运行在同一宿主机上时，转发到 `http://127.0.0.1:8081`，并保留 `Host`、`X-Forwarded-For`、`X-Forwarded-Proto` 请求头。代理规则可参考 [手动部署指南的反向代理示例](manual-deployment.md#反向代理)，证书和 HTTPS 监听需要在代理侧配置。

如果代理本身也运行在容器中，应让两个容器加入同一个 Docker 网络，并转发到 `http://databridge:8081`。代理容器中的 `127.0.0.1` 指向代理自身。

重新执行 `docker compose up -d` 后，通过 HTTPS 域名登录。设置 `DATABRIDGE_COOKIE_SECURE=true` 后，应使用 HTTPS 访问；普通 HTTP 访问可能无法保持登录状态。该选项只控制 Cookie 属性，不会为 DataBridge 的 HTTP 服务自动启用 TLS。

使用 OneDrive 时，还需在 Entra ID 应用中登记 `https://databridge.example.com/api/storages/<存储 ID>/onedrive/callback` 为 Web 重定向 URI。

## 8. 持久化与数据卷

Compose 将命名卷 `databridge-data` 挂载到 `/data`，实际 Docker 卷名通常带有项目名前缀，例如 `databridge_databridge-data`。默认项目名来自部署目录名；使用 `-p` 或 `COMPOSE_PROJECT_NAME` 时，以指定值为准。

查看当前容器实际挂载的卷：

```bash
docker inspect databridge --format '{{range .Mounts}}{{println .Name "->" .Destination}}{{end}}'
```

请保持部署目录和项目名一致。更换目录名或项目名可能创建新的空卷，表现为已保存连接和任务记录消失。Docker Run 示例中的 `databridge-data` 与 Compose 创建的带前缀卷也不会自动共用；切换部署方式时需要显式复用原卷或备份恢复。

`/data/databridge.db` 保存应用状态，`/data/databridge.key` 用来解密已保存凭证，两者必须一起保留。会话签名密钥 `DATABRIDGE_SESSION_SECRET` 不能替代 `databridge.key`。在控制台创建本地备份存储节点时，建议将路径设为 `/data/backups`，以便产物随数据卷持久化；容器内未挂载的其他目录会在重建时丢失。

`docker compose down` 会保留命名卷；`docker compose down -v` 会删除数据卷及其内容，仅在确认不再需要数据时使用。当前 SQLite 和本地任务执行方式适合单实例运行，不应通过增加副本来并行共享同一数据目录。

如需将命名卷改为宿主机目录挂载，先迁移原卷数据，再将 Compose 中的 `databridge-data:/data` 改为绝对路径挂载，例如 `/srv/databridge/data:/data`。镜像以非 root 用户（UID `10001`）运行，目录需要允许该用户读写；新建空目录可使用：

```bash
sudo install -d -o 10001 -m 0700 /srv/databridge/data
```

## 9. 备份与恢复

### 备份应用数据

以下命令适用于默认命名卷配置。先等待任务结束，再停止服务，对整个 `/data` 归档，避免在 SQLite 写入期间单独复制数据库文件。归档包含数据库、凭证密钥及 `/data` 下的本地备份产物；对象存储、WebDAV 等外部存储中的文件需要另外保护。

```bash
umask 077
mkdir -p backups
backup_file="backups/databridge-data-$(date +%Y%m%d-%H%M%S).tar.gz"
docker compose stop databridge
docker compose run --rm --no-deps -T --user 0:0 --entrypoint tar \
  databridge -C /data -czf - . > "$backup_file"
```

确认归档命令成功后，检查归档可读，并保存本次使用的配置和镜像 ID：

```bash
tar -tzf "$backup_file" > /dev/null
cp .env "${backup_file}.env"
docker inspect databridge --format '{{.Image}}' > "${backup_file}.image-id"
docker compose start databridge
```

将备份复制到其他主机或存储，并保留本次部署使用的 Compose 文件及 override 文件。配置和归档都包含敏感信息，应限制读取权限。备份失败时先排查磁盘空间和命令输出，避免将不完整归档作为可恢复备份。

### 恢复到新数据卷

使用新卷恢复可以保留原卷，避免将旧的 SQLite 辅助文件或旧产物混入备份。先将备份对应的 `.env` 恢复到部署目录，并确保其中的镜像版本可用；如果使用本地镜像，需要先构建或导入该版本。

先停止服务并创建一个尚未使用过的卷名：

```bash
docker compose stop databridge
docker volume create databridge-restored-data
```

新建 `docker-compose.restore.yml`，内容如下：

```yaml
volumes:
  databridge-data:
    external: true
    name: databridge-restored-data
```

确认新卷为空；`docker volume create` 遇到同名卷会复用它，不会清空数据。将备份路径替换为实际文件后执行：

```bash
backup_file=backups/databridge-data-20261120-120000.tar.gz
docker compose -f docker-compose.yml -f docker-compose.restore.yml config --quiet
docker compose -f docker-compose.yml -f docker-compose.restore.yml \
  run --rm --no-deps -T --user 0:0 --entrypoint tar \
  databridge -C /data -xzf - < "$backup_file"
docker compose -f docker-compose.yml -f docker-compose.restore.yml up -d
docker compose -f docker-compose.yml -f docker-compose.restore.yml ps
```

归档提取会保留原有文件权限和属主。恢复后验证健康检查、登录、已保存连接和备份记录。后续管理继续携带这两个 `-f` 参数；如需恢复其他 override 中的网络设置，也要显式加入对应的 `-f` 文件。确认恢复有效后，可将卷配置合并回主 Compose 文件。原数据卷不会被上述恢复过程删除。

## 10. 升级与回退

1. 等待任务结束，按上一节备份数据和 `.env`，记录旧镜像版本。
2. 修改 `.env` 中的 `DATABRIDGE_IMAGE`，设置为目标版本或 digest。
3. 验证配置、拉取镜像并重建服务：

```bash
docker compose config --quiet
docker compose pull databridge
docker compose up -d
docker compose ps
docker compose logs --tail=100 databridge
curl -fsS http://127.0.0.1:8081/api/healthz
```

如果仍使用 `latest`，也要先执行 `pull` 才能获取远端更新；仅执行 `restart` 不会切换镜像。使用本地镜像时，按本地构建章节操作。

升级后检查登录、连接发现和备份记录。需要回退时，恢复旧的 `.env` 和对应旧镜像，并使用升级前的数据归档恢复到新卷。数据库结构发生变化时，不能假定旧镜像可以直接读取升级后的 SQLite 数据。

## 11. 常见问题

| 现象 | 检查方式 |
| --- | --- |
| 提示必须设置密码或会话密钥 | 检查 `.env` 是否与 Compose 文件同目录、必填值是否为空；指定其他文件时使用 `docker compose --env-file /path/to/databridge.env ...` |
| 端口占用或容器名称冲突 | 修改 `DATABRIDGE_PORT`；如已使用 Docker Run 启动同名容器，先停止并移除旧容器，保留其数据卷，再切换部署方式 |
| 修改 `.env` 没有生效 | 检查 Shell 同名变量，然后执行 `docker compose up -d` |
| 容器 `unhealthy` 或反复重启 | 查看 `docker compose logs --tail=100 databridge`，检查磁盘空间、SQLite 初始化和 `/data` 写入权限 |
| 宿主机访问正常，远程无法访问 | 检查绑定地址是否为 `127.0.0.1`，以及服务器防火墙、安全组和代理配置 |
| HTTP 页面登录后仍提示未登录 | 检查是否错误开启 `DATABRIDGE_COOKIE_SECURE`；开启后使用 HTTPS 域名访问 |
| 数据库连接超时 | 检查是否填写了容器自己的 `localhost`、网络是否互通、数据库是否允许容器网段访问 |
| 容器重建后没有原来的连接和任务 | 检查项目名和实际挂载卷是否变化；不要直接删除旧卷 |
| OneDrive 回调地址不匹配 | 检查 `DATABRIDGE_PUBLIC_URL`、代理协议头和 Entra ID 登记的重定向 URI |
