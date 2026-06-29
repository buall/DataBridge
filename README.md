# DataBridge

DataBridge 是一个面向 PostgreSQL、MySQL、MongoDB 和 Neo4j 的数据库迁移工作台，包含 React 前端控制台和 Go 编写的迁移编排服务。当前版本适合小型数据库、受控测试环境和迁移流程验证，重点覆盖连接管理、数据库发现、备份恢复、迁移任务编排、运行日志和结果校验。

## 当前支持

### 数据库类型

- PostgreSQL
- MySQL
- MongoDB
- Neo4j

### 迁移方向

- PostgreSQL -> PostgreSQL
- MySQL -> MySQL
- MongoDB -> MongoDB
- Neo4j -> Neo4j

当前不支持跨类型迁移：

- PostgreSQL -> MySQL
- MySQL -> PostgreSQL
- MongoDB -> PostgreSQL
- Neo4j -> MySQL

## 兼容性验证摘要

基于当前仓库内串行兼容性 runner，已完成首轮 PostgreSQL、MySQL、MongoDB、Neo4j 主矩阵验证。测试全部通过 DataBridge API 驱动执行，DataBridge 常驻运行，被测数据库按“每次只启动一组源库 + 目标库容器”的方式串行验证，case 结束后清理容器和 volume，并保留本地报告。

- PostgreSQL：5 个主组合中 4 个 `passed`，`18 -> 13` 为 `conditional`
- MySQL：4 个主组合全部 `passed`
- MongoDB：4 个主组合全部 `passed`
- Neo4j：3 个主组合全部 `passed`

当前唯一明确的版本边界是 PostgreSQL `18 -> 13`。失败原因已定位为上游 PostgreSQL 工具链降级限制：`pg_dump-18` 生成的目录格式头 `1.16` 无法被 `pg_restore-13` 读取，不是 DataBridge 连接、发现或 API 链路失败。

Neo4j `4.4 -> 5.x` 已验证通过。DataBridge 当前已在 Neo4j restore 链路中兼容 4.4 导出到 5.x 时的旧式约束语法差异。

详细测试计划与逐组合结果见：

- [docs/compatibility-test-plan.md](docs/compatibility-test-plan.md)
- [docs/compatibility-matrix.md](docs/compatibility-matrix.md)

### 迁移引擎矩阵

| 源端类型 | 目标端类型 | 原生工具引擎 | 内置引擎 |
| --- | --- | --- | --- |
| PostgreSQL | PostgreSQL | 支持 | 支持 |
| MySQL | MySQL | 支持 | 暂不支持 |
| MongoDB | MongoDB | 支持 | 暂不支持 |
| Neo4j | Neo4j | 支持 | 暂不支持 |

## PostgreSQL 迁移原理

DataBridge 当前支持两种 PostgreSQL 迁移引擎：

- 原生工具引擎：使用 `psql`、`pg_dump`、`pg_restore` 完成结构和数据迁移，适合 PostgreSQL 到 PostgreSQL 的全量迁移。
- 内置 COPY 引擎：使用 Go 进程内的 `pgx` 和 PostgreSQL binary `COPY` 传输数据，适合需要更细粒度控制、调试单表迁移流程或原生工具不可用的场景。

两种引擎都会在任务完成后执行逐表精确行数校验，并将任务状态、事件日志和校验报告持久化到本地 SQLite。

### PostgreSQL 原生工具引擎

PostgreSQL 原生工具引擎由 Go API 负责任务编排、进度记录、目标端预处理和结果校验，真正的对象和数据搬运交给 PostgreSQL 官方客户端工具完成。

整体流程如下：

1. 连接源端和目标端  
   后端根据保存的连接信息分别连接 `sourceDatabase` 和 `targetDatabase`，用于检查表是否存在、统计源端行数和执行最终校验。

2. 读取源端元数据和精确行数  
   引擎根据用户选择的 `schema.table` 列表确认表存在，并对每张选中表执行 `count(*)`，得到总行数和校验基线。

3. 预处理目标端同名对象  
   迁移开始前会先使用 `psql` 执行目标端预处理：
   - `backup`：将目标端同名表重命名为带 `databridge_backup` 后缀的备份表，并同步重命名旧索引和旧序列
   - `overwrite`：删除目标端同名表
   - `skip`：如果目标端已存在同名表，则任务失败

4. 同步目标端扩展  
   引擎会读取源库中除 `plpgsql` 外的扩展，并在目标库执行 `CREATE EXTENSION IF NOT EXISTS`。

5. 使用 `pg_dump` 导出源端对象  
   全选时执行整库 `pg_dump`；部分选择时通过多个 `--table` 参数仅导出所选表及其依赖对象。

6. 使用 `pg_restore` 恢复到目标端  
   引擎会过滤 schema 和 extension 自身的 TOC 项，再调用 `pg_restore --jobs` 并发恢复结构、数据、索引、约束和序列等对象。

7. 执行行数校验并生成报告  
   恢复完成后，对目标端每张表执行 `count(*)`，与源端精确行数对比。

原生工具引擎依赖运行环境中存在以下命令：

```bash
psql
pg_dump
pg_restore
```

### PostgreSQL 内置 COPY 引擎

内置 COPY 引擎不依赖外部命令行工具。后端通过 `pgx` 同时连接源端数据库和目标端数据库，在 Go 进程内编排结构迁移、数据流转和校验。

整体流程如下：

1. 连接源端和目标端
2. 从 PostgreSQL 系统目录读取表结构、默认值、identity/generated 信息
3. 对每张表执行 `count(*)` 获取精确源端行数
4. 创建目标端 schema、enum、sequence 和表结构
5. 按冲突策略处理目标端同名表
6. 通过 PostgreSQL binary `COPY` 流式复制数据
7. 在数据复制完成后创建主键、唯一约束、check、外键和普通索引
8. 同步序列值
9. 执行逐表行数校验

## MySQL 迁移原理

MySQL 当前仅支持原生工具引擎。

- 原生工具引擎：使用 `mysqldump` 导出、`mysql` 导入，适合 MySQL 到 MySQL 的结构和数据全量迁移。

MySQL 迁移任务同样会写入运行日志，并在恢复完成后执行逐表精确行数校验。

### MySQL 原生工具引擎

MySQL 原生工具引擎由 Go API 负责任务编排、进度记录、目标端预处理和行数校验，真正的结构和数据搬运交给 MySQL 客户端工具完成。

整体流程如下：

1. 连接源端和目标端  
   后端分别连接 `sourceDatabase` 和 `targetDatabase`，用于读取表列表、统计源端行数和执行校验。

2. 读取源端表列表和精确行数  
   引擎根据用户选择的表名确认表存在，并对每张选中表执行 `count(*)`，得到总行数和校验基线。

3. 判断导出范围  
   如果用户选择了当前数据库中的全部普通表，则执行整库 `mysqldump`；如果只选择部分表，则仅导出所选表。

4. 预处理目标端同名表  
   根据冲突策略处理目标端已存在对象：
   - `backup`：将目标端同名表重命名为带 `databridge_backup` 后缀的备份表
   - `overwrite`：删除目标端同名表
   - `skip`：如果目标端已存在同名表，则任务失败

5. 使用 `mysqldump` 导出结构和数据  
   当前导出参数包含：
   - `--single-transaction`
   - `--skip-lock-tables`
   - `--default-character-set=utf8mb4`
   - `--routines`
   - `--triggers`
   - `--events`

6. 使用 `mysql` 导入到目标端  
   导出的 SQL 文件会直接导入目标库。

7. 执行逐表行数校验  
   恢复完成后，对目标端每张已选表执行 `count(*)`，与源端精确行数逐表对比。

MySQL 原生工具引擎依赖运行环境中存在以下命令：

```bash
mysql
mysqldump
```

## MongoDB 迁移原理

MongoDB 当前支持原生工具引擎。

- 原生工具引擎：使用 `mongodump` 按集合导出、`mongorestore` 导入，适合 MongoDB 到 MongoDB 的全量或按集合迁移。

整体流程如下：

1. 读取源端集合列表，并按用户选择确定迁移范围。
2. 根据冲突策略处理目标库：
   - `backup` / `overwrite`：当前会重建目标数据库后导入所选集合
   - `skip`：如果目标库已有集合，则任务失败
3. 对每个选中集合执行 `mongodump --collection`。
4. 使用 `mongorestore --nsFrom --nsTo` 导入到目标数据库。
5. 恢复完成后读取目标集合列表，校验所选集合是否已恢复。

MongoDB 原生工具引擎依赖运行环境中存在以下命令：

```bash
mongodump
mongorestore
```

## Neo4j 迁移原理

Neo4j 当前支持基于 `cypher-shell` 和 APOC 的原生工具引擎，按整库图数据迁移。

- 备份阶段：调用 `CALL apoc.export.cypher.all(null, {stream: true, ...})`，通过 `cypher-shell` 将 Cypher 结果流式写入本地临时文件并压缩。
- 还原阶段：解压 `.tar.gz` 中的 `.cypher` 文件，使用 `cypher-shell -f` 导入目标库。
- 校验阶段：对比源端和目标端节点数、关系数。

冲突策略：

- `skip`：目标库已有节点或关系则任务失败。
- `backup` / `overwrite`：当前都会先清空目标图数据，再导入源端图数据。

Neo4j 原生工具引擎依赖运行环境中存在以下条件：

```bash
cypher-shell
java
```

并且源端 Neo4j 需要安装 APOC，启用导出过程：

```text
NEO4J_PLUGINS='["apoc"]'
NEO4J_apoc_export_file_enabled=true
NEO4J_apoc_import_file_use__neo4j__config=false
```

## 当前功能

- 测试并保存 PostgreSQL / MySQL / MongoDB / Neo4j 连接，凭证使用 AES-256-GCM 加密存储。
- 保存连接名称、备注、主机、端口、用户、SSL 模式、延迟、节点地区和版本号。
- 支持编辑、删除、刷新状态、查看数据库、创建数据库和写入演示数据。
- 发现 PostgreSQL / MySQL 数据库和普通表，发现 MongoDB 数据库和集合，发现 Neo4j 数据库、节点标签和关系类型。
- 支持迁移向导中选择源端、目标端、数据库和待迁移对象；Neo4j 当前按整库图数据迁移。
- PostgreSQL 支持原生工具迁移：`psql` 预处理目标对象，`pg_dump` 导出，`pg_restore` 恢复。
- PostgreSQL 支持内置 COPY 迁移：由 Go 进程读取元数据、创建对象并执行 binary `COPY`。
- MySQL 支持原生工具迁移：`mysqldump` 导出，`mysql` 导入。
- MongoDB 支持原生工具迁移：`mongodump` 导出，`mongorestore` 导入。
- Neo4j 支持原生工具迁移：APOC stream 导出，`cypher-shell` 导入。
- 支持本地目录和对象存储备份，支持 PostgreSQL / MySQL / MongoDB / Neo4j 备份。
- 支持从备份产物恢复 PostgreSQL / MySQL / MongoDB / Neo4j。
- 支持冲突策略：`backup`、`overwrite`、`skip`。
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

## 产品边界与已知限制

### 通用限制

- 目标数据库必须已经存在，DataBridge 当前不会自动创建迁移任务所需的目标数据库，只会在连接管理页面提供手动创建数据库能力。
- 当前不支持跨类型迁移。
- 当前没有分布式 worker，也没有断点续传。
- 运行中的迁移如果遇到 API 进程重启，会被标记为失败。
- 当前更适合小型数据库和受控测试环境；生产迁移前需要充分验证权限、数据规模、锁影响、业务停写方案和回滚策略。

### PostgreSQL 限制

- 内置 COPY 引擎当前主要覆盖普通表和分区表父表发现；视图、物化视图、函数、过程、扩展、角色和授权不迁移。
- 原生工具引擎由 `pg_dump` 和 `pg_restore` 处理 PostgreSQL 对象，覆盖面更接近官方工具，但仍需要提前验证目标库权限、扩展依赖和对象冲突策略。
- 原生工具引擎同步扩展时要求目标库用户具备创建相应扩展的权限，且目标 PostgreSQL 环境已安装对应扩展包。
- 内置 COPY 引擎除 enum 外的自定义 PostgreSQL 类型暂未完整处理。
- PostgreSQL 内置 COPY 仅支持 PostgreSQL -> PostgreSQL。

### MySQL 限制

- MySQL 当前只支持原生工具引擎，不支持进程内 COPY 类引擎。
- MySQL 迁移当前聚焦普通表的结构和数据全量迁移；更复杂的对象覆盖范围依赖 `mysqldump` 导出结果与目标端权限，请在正式环境先做演练。
- 目标端预处理当前按表级冲突策略执行，不会做更细粒度的对象级冲突编排。
- 导入阶段直接执行 SQL 文件，导入行为和兼容性主要受源/目标版本差异、字符集、权限和客户端工具版本影响。
- 删除已保存连接不会自动删除历史迁移记录，但后续无法再基于该连接继续发现数据库或重新发起迁移。

### MongoDB 限制

- MongoDB 当前只支持原生工具引擎，不支持进程内 COPY 类引擎。
- MongoDB 迁移可按集合选择，但 `backup` / `overwrite` 策略当前都会重建目标数据库后导入所选集合。
- MongoDB 行数级校验当前以集合是否恢复为主，不逐文档比对内容。

### Neo4j 限制

- Neo4j 当前只支持 `cypher-shell` + APOC 的原生工具链。
- Neo4j 迁移和备份当前按整库图数据处理，暂不支持按标签或关系类型拆分。
- Neo4j `backup` / `overwrite` 策略当前都会先执行 `MATCH (n) DETACH DELETE n` 清空目标图数据，再导入备份 Cypher。
- Neo4j 校验当前对比节点数和关系数，不逐节点属性或关系属性比对。
- 跨 Neo4j 大版本恢复需要提前演练，尤其是约束、索引和 APOC 导出的 Cypher 兼容性。

## 运行方式

安装依赖并构建前端：

```bash
npm install
npm run build
```

启动 Go API 和生产前端静态服务，默认端口为 `8081`：

```bash
DATABRIDGE_AUTH_PASSWORD='替换为高强度密码' \
DATABRIDGE_SESSION_SECRET='替换为至少32字节的随机字符串' \
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

如果前端开发服务器和 API 分开运行，并且浏览器需要跨端口携带登录 Cookie，请在启动 API 时允许 Vite 来源：

```bash
DATABRIDGE_AUTH_PASSWORD='替换为高强度密码' \
DATABRIDGE_SESSION_SECRET='替换为至少32字节的随机字符串' \
DATABRIDGE_CORS_ORIGIN=http://127.0.0.1:5173 \
npm run dev:api
```

## 登录与公网部署

DataBridge 的控制台和业务 API 已加入登录保护。生产或公网部署时必须设置：

- `DATABRIDGE_AUTH_USER`：登录用户名，默认 `admin`
- `DATABRIDGE_AUTH_PASSWORD`：登录密码，建议使用长随机密码
- `DATABRIDGE_SESSION_SECRET`：会话签名密钥，建议使用 `openssl rand -base64 32` 生成
- `DATABRIDGE_COOKIE_SECURE`：HTTPS 部署时设置为 `true`

未设置 `DATABRIDGE_AUTH_PASSWORD` 时，服务只允许本机回环地址访问业务 API，方便本地开发，但不适合公网部署。

## Docker 镜像

项目提供多阶段 `Dockerfile`，运行镜像包含：

- DataBridge Go API
- 已构建的 React 前端静态资源
- PostgreSQL 客户端：`psql`、`pg_dump`、`pg_restore`
- MySQL 客户端：`mysql`、`mysqldump`
- MongoDB Database Tools：`mongodump`、`mongorestore`、`mongoexport`、`mongoimport`、`mongostat`、`mongotop`
- Neo4j Cypher Shell：`cypher-shell`
- Java 21 运行时：`temurin-21-jre`

Neo4j 备份、恢复和迁移要求 Neo4j 服务端已安装 APOC 并启用 export 过程。`cypher-shell` 需要 Java 运行时，Dockerfile 会显式安装 Temurin 21 JRE 和 Cypher Shell 客户端包，不安装 Neo4j Server。

本地构建当前架构镜像：

```bash
docker build -t databridge:local .
```

运行容器：

```bash
export DATABRIDGE_AUTH_PASSWORD='替换为高强度密码'
export DATABRIDGE_SESSION_SECRET="$(openssl rand -base64 32)"
docker compose up -d
```

或直接运行镜像：

```bash
docker run --rm \
  -p 8081:8081 \
  -v databridge-data:/data \
  -e DATABRIDGE_AUTH_USER=admin \
  -e DATABRIDGE_AUTH_PASSWORD="$DATABRIDGE_AUTH_PASSWORD" \
  -e DATABRIDGE_SESSION_SECRET="$DATABRIDGE_SESSION_SECRET" \
  -e DATABRIDGE_PG_JOBS=4 \
  hkccr.ccs.tencentyun.com/buall/databridge:latest
```

打开：

[http://127.0.0.1:8081](http://127.0.0.1:8081)

容器内默认持久化路径：

- SQLite 数据库：`/data/databridge.db`
- 凭证密钥：`/data/databridge.key`

请务必挂载 `/data` volume。删除该 volume 会导致已保存连接和加密凭证密钥丢失。

可通过环境变量调整镜像、端口和 PostgreSQL 并行任务数：

```bash
DATABRIDGE_IMAGE=hkccr.ccs.tencentyun.com/buall/databridge:latest \
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

## Kubernetes 部署

基础清单见 [deploy/kubernetes](deploy/kubernetes)，包含 Namespace、Secret、PVC、Deployment、Service 和 Ingress 示例；命名空间在 `kustomization.yaml` 的 `namespace` 字段中统一配置。

部署前请先替换 Secret、镜像地址、Ingress 域名和存储配置：

```bash
kubectl apply -k deploy/kubernetes
kubectl -n databridge rollout status deployment/databridge
```

## 手动部署

裸机或虚拟机部署见 [docs/manual-deployment.md](docs/manual-deployment.md)，包含构建产物、systemd 服务、反向代理、备份和升级步骤。

## 验证命令

```bash
GOCACHE="$PWD/.gocache" go test ./...
GOCACHE="$PWD/.gocache" go vet ./...
npm run build
```
