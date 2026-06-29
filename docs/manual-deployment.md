# DataBridge 手动部署指南

本文档面向不使用 Docker Compose 或 Kubernetes 的裸机/虚拟机部署。生产环境建议优先使用发布镜像；如需手动部署，需要同时准备 DataBridge 二进制、前端静态资源、数据目录和数据库客户端工具。

## 运行环境

建议环境：

- Linux x86_64 或 arm64
- Node.js 22，用于构建前端
- Go 1.26，用于构建 API
- systemd，用于托管服务
- 至少 2 CPU / 2 GiB 内存，迁移大库时按数据量提高配置

DataBridge 的原生迁移/备份引擎依赖外部命令，请按需要安装：

- PostgreSQL：`psql`、`pg_dump`、`pg_restore`
- MySQL：`mysql`、`mysqldump`
- MongoDB：`mongodump`、`mongorestore`
- Neo4j：`cypher-shell`、`java`

如果只使用 PostgreSQL 内置 COPY 引擎，可以不安装 PostgreSQL 客户端工具；其他原生引擎仍需要对应客户端。

## 构建产物

在源码目录执行：

```bash
npm ci
npm run build
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/databridge-api ./cmd/api
```

构建完成后需要发布：

- `bin/databridge-api`
- `dist/`

后端会从当前工作目录读取 `dist/index.html` 并提供前端静态资源，因此 systemd 的 `WorkingDirectory` 必须指向包含 `dist/` 的目录。

## 安装目录

以下示例使用 `/opt/databridge` 保存程序，`/var/lib/databridge` 保存持久化数据：

```bash
sudo install -d -o root -g root -m 0755 /opt/databridge
sudo install -d -o root -g root -m 0755 /opt/databridge/dist
sudo install -d -o databridge -g databridge -m 0700 /var/lib/databridge
sudo install -m 0755 bin/databridge-api /opt/databridge/databridge-api
sudo cp -a dist/. /opt/databridge/dist/
```

如系统中还没有运行用户：

```bash
sudo useradd --system --home /var/lib/databridge --shell /usr/sbin/nologin databridge
```

## 环境变量

创建 `/etc/databridge.env`：

```bash
DATABRIDGE_ADDR=:8081
DATABRIDGE_DB_PATH=/var/lib/databridge/databridge.db
DATABRIDGE_KEY_PATH=/var/lib/databridge/databridge.key
DATABRIDGE_AUTH_USER=admin
DATABRIDGE_AUTH_PASSWORD=替换为高强度密码
DATABRIDGE_SESSION_SECRET=替换为至少32字节的随机字符串
DATABRIDGE_PG_JOBS=4
TZ=Asia/Shanghai
```

保护配置文件权限：

```bash
sudo chown root:databridge /etc/databridge.env
sudo chmod 0640 /etc/databridge.env
```

生成密钥示例：

```bash
openssl rand -base64 32
```

如果通过 HTTPS 反向代理暴露服务，建议额外设置：

```bash
DATABRIDGE_COOKIE_SECURE=true
```

如果前端和 API 分域部署，按需设置：

```bash
DATABRIDGE_CORS_ORIGIN=https://databridge.example.com
```

## systemd 服务

创建 `/etc/systemd/system/databridge.service`：

```ini
[Unit]
Description=DataBridge database migration workbench
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=databridge
Group=databridge
WorkingDirectory=/opt/databridge
EnvironmentFile=/etc/databridge.env
ExecStart=/opt/databridge/databridge-api
Restart=on-failure
RestartSec=5s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ReadWritePaths=/var/lib/databridge

[Install]
WantedBy=multi-user.target
```

启动服务：

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now databridge
sudo systemctl status databridge
```

查看日志：

```bash
journalctl -u databridge -f
```

健康检查：

```bash
curl -fsS http://127.0.0.1:8081/api/healthz
```

## 反向代理

如果使用 Nginx：

```nginx
server {
    listen 80;
    server_name databridge.example.com;

    location / {
        proxy_pass http://127.0.0.1:8081;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

生产环境建议启用 HTTPS，并在 `/etc/databridge.env` 中设置 `DATABRIDGE_COOKIE_SECURE=true`。

## 备份与升级

必须同时备份：

- `/var/lib/databridge/databridge.db`
- `/var/lib/databridge/databridge.key`

`databridge.key` 丢失后，已保存连接中的加密密码无法恢复。

升级步骤：

```bash
sudo systemctl stop databridge
sudo cp /var/lib/databridge/databridge.db /var/lib/databridge/databridge.db.bak
sudo cp /var/lib/databridge/databridge.key /var/lib/databridge/databridge.key.bak
sudo install -m 0755 bin/databridge-api /opt/databridge/databridge-api
sudo rm -rf /opt/databridge/dist
sudo install -d -o root -g root -m 0755 /opt/databridge/dist
sudo cp -a dist/. /opt/databridge/dist/
sudo systemctl start databridge
curl -fsS http://127.0.0.1:8081/api/healthz
```

如果要变更监听端口、存储路径、登录密码或 PostgreSQL 并行恢复数，修改 `/etc/databridge.env` 后执行：

```bash
sudo systemctl restart databridge
```
