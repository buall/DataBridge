# DataBridge

DataBridge 是一个 PostgreSQL 到 PostgreSQL 的数据迁移 MVP，包含 React 前端工作台和 Go 编写的迁移编排服务。当前版本面向小型数据库、受控测试环境和迁移流程验证，重点覆盖连接管理、对象选择、结构创建、数据复制、任务进度、运行日志和行数校验报告。

## PostgreSQL 迁移原理

DataBridge 当前支持两种 PostgreSQL 迁移引擎：

- 原生工具引擎：使用 `psql`、`pg_dump`、`pg_restore` 完成结构和数据迁移，适合 PostgreSQL 到 PostgreSQL 的全量迁移，也是前端新建任务的默认模式。
- 内置 COPY 引擎：使用 Go 进程内的 `pgx` 和 PostgreSQL binary `COPY` 传输数据，适合需要更细粒度控制、调试单表迁移流程或原生工具不可用的场景。

两种引擎都会在任务完成后执行逐表精确行数校验，并将任务状态、事件日志和校验报告持久化到本地 SQLite。

### 原生工具引擎

原生工具引擎由 Go API 负责任务编排、进度记录、目标端预处理和结果校验，真正的 PostgreSQL 结构与数据搬运交给官方客户端工具完成。

整体流程如下：

1. 连接源端和目标端
   后端根据保存的连接信息分别连接 `sourceDatabase` 和 `targetDatabase`，用于检查表是否存在、统计源端行数和执行最终校验。连接信息中的密码会从本地 SQLite 解密后用于本次任务。

2. 读取源端元数据和精确行数
   引擎根据用户选择的 `schema.table` 列表确认表存在，并对每张选中表执行 `count(*)`，得到总行数和校验基线。

3. 预处理目标端同名对象
   迁移开始前会先使用 `psql` 执行目标端预处理：
   - `backup`：将目标端同名表重命名为带 `databridge_backup` 后缀的备份表，并同步重命名旧索引和旧序列，避免与恢复对象重名
   - `overwrite`：删除目标端同名表
   - `skip`：如果目标端已存在同名表，则任务失败

4. 同步目标端扩展
   引擎会读取源库中除 `plpgsql` 外的扩展，并在目标库执行 `CREATE EXTENSION IF NOT EXISTS`。这可以提前补齐 `pg_trgm` 等索引操作符类依赖，避免恢复索引时出现 `gin_trgm_ops does not exist` 之类的错误。

5. 使用 `pg_dump` 导出源端对象
   引擎调用 `pg_dump --format=directory` 生成目录格式归档，并通过 `--jobs` 并发导出多张表。如果用户选择了当前源库中所有可迁移用户表，DataBridge 会直接执行整库 `pg_dump`；如果只选择部分表，则使用多个 `--table` 参数仅导出所选表及其依赖对象。导出日志会写入运行日志。

6. 使用 `pg_restore` 恢复到目标端
   引擎先生成 restore list，并过滤 schema 和 extension 自身的创建项，因为 schema 和 extension 会由 DataBridge 提前创建。随后调用 `pg_restore --jobs` 并发恢复表结构、数据、索引、约束和序列等对象。

7. 执行行数校验并生成报告
   恢复完成后，引擎对目标端每张表执行 `count(*)`，与源端精确行数对比。每张表会保存源端行数、目标端行数、复制行数和校验结果，用于前端报告。

原生工具引擎依赖运行环境中存在以下命令：

```bash
psql
pg_dump
pg_restore
```

本地开发环境可以通过 Homebrew 安装 `libpq`，生产环境建议将这些客户端工具直接打包到 API 镜像中。

并发度默认取本机 CPU、表数量和上限 4 的较小值。需要更高并发时可以通过环境变量调整，最大限制为 16：

```bash
DATABRIDGE_PG_JOBS=8 npm run dev:api
```

### 内置 COPY 引擎

内置 COPY 引擎不依赖外部命令行工具。后端通过 `pgx` 同时连接源端数据库和目标端数据库，在 Go 进程内编排结构迁移、数据流转和校验。

整体流程如下：

1. 连接源端和目标端
   后端根据保存的连接信息分别连接 `sourceDatabase` 和 `targetDatabase`。连接信息中的密码会从本地 SQLite 解密后用于本次连接。

2. 读取源端元数据
   迁移引擎根据用户选择的 `schema.table` 列表，从 PostgreSQL 系统目录读取字段、类型、默认值、非空约束、identity/generated 信息等表结构。表发现阶段使用 `pg_class`、`pg_namespace`、`pg_attribute`、`pg_attrdef` 等系统目录。

3. 预估和统计行数
   引擎对每张选中表执行 `count(*)`，得到精确源端行数，用于进度计算和最终校验。

4. 创建目标端基础结构
   引擎会先在目标端创建需要的 schema、枚举类型和序列：
   - schema 使用 `CREATE SCHEMA IF NOT EXISTS`
   - enum 类型从 `pg_type`、`pg_enum`、`pg_namespace` 读取后创建
   - sequence 从 `pg_sequences` 读取后创建

5. 处理目标端同名表
   用户可选择冲突策略：
   - `backup`：将目标端已有表重命名为带 `databridge_backup` 后缀的备份表
   - `overwrite`：删除目标端已有表并重新创建
   - `skip`：如果目标端表已存在，则任务失败

6. 创建目标端表结构
   引擎按源端字段定义在目标端生成 `CREATE TABLE`。当前会处理字段类型、默认值、非空、identity 序列默认值和 stored generated column。

7. 使用 PostgreSQL binary COPY 流式复制数据
   数据复制阶段不把整张表加载到内存中。引擎使用 `io.Pipe` 将源端：

   ```sql
   COPY schema.table (columns...) TO STDOUT BINARY
   ```

   直接流式接到目标端：

   ```sql
   COPY schema.table (columns...) FROM STDIN BINARY
   ```

   generated column 不参与复制，由目标端按定义生成。每张表复制完成后记录复制行数、耗时和事件日志。

8. 复制完成后创建约束和索引
   为降低数据导入阶段的约束和索引维护成本，引擎在数据复制后再创建：
   - 主键
   - 唯一约束
   - check 约束
   - 外键
   - 非约束索引

   约束定义通过 `pg_get_constraintdef` 获取，普通索引通过 `pg_indexes.indexdef` 获取。

9. 同步序列值
   数据复制后，引擎读取源端每个序列的 `last_value` 和 `is_called`，在目标端执行 `setval`，避免后续插入时序列值回退或冲突。

10. 执行行数校验并生成报告
    引擎对目标端每张表执行 `count(*)`，与源端精确行数对比。每张表会保存源端行数、目标端行数、复制行数、耗时和校验结果，用于前端校验报告。

11. 持久化任务状态和事件
    任务进度、运行日志、表级结果和报告会保存到本地 SQLite。API 进程重启后仍可查看已完成任务和报告；如果任务运行中 API 进程重启，该任务会被标记为失败。

## 当前功能

- 测试并保存 PostgreSQL 连接，凭证使用 AES-256-GCM 加密存储。
- 保存连接名称、主机、端口、用户、SSL 模式、延迟和 PostgreSQL 版本号。
- 发现 PostgreSQL 数据库和用户表。
- 选择源端、目标端、数据库和待迁移表。
- 支持原生工具迁移模式：`psql` 预处理目标对象，`pg_dump` 导出，`pg_restore` 恢复。
- 支持内置 COPY 迁移模式：由 Go 进程读取元数据、创建对象并执行 binary `COPY`。
- 创建 schema、enum、sequence 和表结构。
- 使用 PostgreSQL binary `COPY` 流式复制数据。
- 复制主键、唯一约束、check 约束、外键和普通索引。
- 同步序列值。
- 展示任务列表、任务进度、运行日志和校验报告。
- 将迁移任务、事件、表级校验结果和报告持久化到 SQLite。
- 支持取消运行中的迁移任务。

## 持久化与凭证安全

默认启用本地 SQLite 持久化：

- 数据库文件：`data/databridge.db`
- 凭证密钥：`data/databridge.key`

首次启动时会自动生成 32 字节密钥文件，并使用仅当前用户可读写的权限保存。连接密码会用 AES-256-GCM 加密后写入 SQLite。

备份时请同时备份 `databridge.db` 和 `databridge.key`。如果密钥文件丢失，已保存连接的加密密码无法恢复。

可以通过环境变量调整路径：

```bash
DATABRIDGE_DB_PATH=/secure/path/databridge.db \
DATABRIDGE_KEY_PATH=/secure/path/databridge.key \
npm run dev:api
```

## MVP 边界

- 目标数据库必须已经存在，DataBridge 当前不会自动创建数据库。
- 当前仅支持 PostgreSQL 到 PostgreSQL。
- 内置 COPY 引擎当前迁移普通表和分区表父表发现；视图、物化视图、函数、过程、扩展、角色和授权不迁移。
- 原生工具引擎由 `pg_dump` 和 `pg_restore` 处理 PostgreSQL 对象，覆盖面更接近官方工具，但仍需要提前验证目标库权限、扩展依赖和对象冲突策略。
- 原生工具引擎同步扩展时要求目标库用户具备创建相应扩展的权限，且目标 PostgreSQL 环境已安装对应扩展包。
- 内置 COPY 引擎除 enum 外的自定义 PostgreSQL 类型暂未完整处理。
- 当前没有分布式 worker，也没有断点续传。
- 运行中的迁移如果遇到 API 进程重启，会被标记为失败。
- 当前适合小型数据库和受控测试环境；生产迁移前需要充分验证权限、数据规模、锁影响、外键顺序和业务停写方案。

## 运行方式

安装依赖并构建前端：

```bash
npm install
npm run build
```

启动 Go API 和生产前端静态服务，默认端口为 `8081`：

```bash
npm run dev:api
```

打开：

[http://127.0.0.1:8081](http://127.0.0.1:8081)

前端开发时，可以分别启动 API 和 Vite：

```bash
npm run dev:api
npm run dev
```

Vite 默认将 `/api` 代理到 `127.0.0.1:8081`。如需修改代理目标：

```bash
VITE_API_PROXY_TARGET=http://127.0.0.1:9000 npm run dev
```

## Docker 镜像

项目提供多阶段 `Dockerfile`，运行镜像包含：

- DataBridge Go API
- 已构建的 React 前端静态资源
- PostgreSQL 客户端：`psql`、`pg_dump`、`pg_restore`
- MySQL 客户端：`mysql`、`mysqldump`
- MongoDB Database Tools：`mongodump`、`mongorestore`、`mongoexport`、`mongoimport`、`mongostat`、`mongotop`

本地构建当前架构镜像：

```bash
docker build -t databridge:local .
```

运行容器：

```bash
docker run --rm \
  -p 8081:8081 \
  -v databridge-data:/data \
  -e DATABRIDGE_PG_JOBS=4 \
  ghcr.io/buall/databridge:latest
```

打开：

[http://127.0.0.1:8081](http://127.0.0.1:8081)

容器内默认持久化路径：

- SQLite 数据库：`/data/databridge.db`
- 凭证密钥：`/data/databridge.key`

请务必挂载 `/data` volume。删除该 volume 会导致已保存连接和加密凭证密钥丢失。

使用 Docker Compose 启动：

```bash
docker compose up -d
```

可通过环境变量调整镜像、端口和 PostgreSQL 并行任务数：

```bash
DATABRIDGE_IMAGE=ghcr.io/buall/databridge:latest \
DATABRIDGE_PORT=8081 \
DATABRIDGE_PG_JOBS=8 \
docker compose up -d
```

构建并推送 amd64/arm64 多架构镜像：

```bash
docker buildx create --use --name databridge-builder
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  -t your-registry/databridge:latest \
  --push .
```

MongoDB Database Tools 版本可以通过 build arg 调整：

```bash
docker build \
  --build-arg PG_MAJOR=18 \
  --build-arg MONGO_TOOLS_VERSION=100.17.0 \
  -t databridge:local .
```

GitHub Actions 会在 PR 中执行 Go 测试、前端构建和 Docker 镜像构建校验；推送到 `main`、`master` 或 `v*` tag 时，会构建并推送 `linux/amd64`、`linux/arm64` 多架构镜像到 GHCR：

```text
ghcr.io/<owner>/<repo>:latest
ghcr.io/<owner>/<repo>:sha-<commit>
ghcr.io/<owner>/<repo>:<tag>
```

## 验证命令

```bash
GOCACHE="$PWD/.gocache" go test ./...
GOCACHE="$PWD/.gocache" go vet ./...
npm run build
```
