# DataBridge Compatibility Test Plan

## 测试目标

验证 DataBridge 对 PostgreSQL、MySQL、MongoDB、Neo4j 不同版本的连接、版本识别、数据库发现、对象发现、迁移、备份和恢复兼容性，并产出可持续复跑的兼容性矩阵。

测试按 4C8G Linux 服务器设计：DataBridge 服务常驻运行，每次只启动一组被测数据库容器（源库 + 目标库），case 结束后无论成功失败都删除容器和 volume。

## 测试范围

P0 必测：

- 被测数据库容器启动并 ready
- DataBridge API 可连接源库和目标库
- 版本识别成功
- 数据库发现成功
- 对象发现成功

P1 必测：

- 同类型迁移成功
- 本地目录备份成功
- 从备份恢复到目标端或恢复测试库成功
- 迁移/恢复后的结果校验符合当前 DataBridge 能力边界

P2 后续补充：

- TLS
- 低权限账号
- 异常路径
- PostgreSQL binary COPY
- MongoDB 副本集/分片
- Neo4j 集群

## 版本矩阵

PostgreSQL 版本冒烟：13、14、15、16、17、18。

PostgreSQL 首轮迁移组合：

| Source | Target |
| --- | --- |
| 13 | 13 |
| 13 | 18 |
| 18 | 13 |
| 15 | 15 |
| 14 | 16 |

PostgreSQL 默认使用 DataBridge native 引擎，即 `pg_dump` / `pg_restore` 主路径。binary COPY 作为 P2 专项，不阻塞首轮。

MySQL 版本冒烟：5.7、8.0。

MySQL 首轮迁移组合：

| Source | Target |
| --- | --- |
| 5.7 | 5.7 |
| 5.7 | 8.0 |
| 8.0 | 5.7 |
| 8.0 | 8.0 |

MongoDB 版本冒烟：5.0、6.0、7.0、8.0。

MongoDB 首轮迁移组合：

| Source | Target |
| --- | --- |
| 5.0 | 5.0 |
| 5.0 | 8.0 |
| 8.0 | 5.0 |
| 6.0 | 7.0 |

MongoDB 默认使用 `admin` 作为 `authenticationDatabase`。

Neo4j 版本冒烟：4.4、5.x。

Neo4j 首轮迁移组合：

| Source | Target |
| --- | --- |
| 4.4 | 4.4 |
| 4.4 | 5.x |
| 5.x | 5.x |

Neo4j 重点验证 `cypher-shell` + APOC。APOC 或 `cypher-shell` 失败时记录为工具链/配置问题，不笼统标记版本不兼容。

## 单 Case 流程

1. 清理同名旧容器和旧 volume。
2. 启动源库容器。
3. 启动目标库容器。
4. 等待源库和目标库 ready。
5. 初始化源端测试数据。
6. 初始化目标端空数据库。
7. 调用 DataBridge API 创建源连接和目标连接。
8. 验证连接测试成功。
9. 验证数据库列表发现成功。
10. 验证对象发现成功。
11. 发起迁移任务。
12. 轮询迁移任务直到成功或失败。
13. 校验迁移结果。
14. 创建备份任务并触发执行。
15. 轮询备份任务直到成功或失败。
16. 执行恢复到目标端或恢复测试库。
17. 轮询恢复任务直到成功或失败。
18. 收集 DataBridge API 响应、任务详情、任务事件、源/目标容器日志、runner 输出。
19. 写入 `docs/compatibility-matrix.md`。
20. 删除源库、目标库容器和 volume。

## 数据集设计

每组数据控制在约 1000 条，适配 4C8G 服务器。

PostgreSQL 覆盖普通表、主键、唯一索引、普通索引、外键、identity、json/jsonb、timestamp、中文和 emoji。

MySQL 覆盖 InnoDB 表、主键、索引、外键、auto_increment、json、decimal、datetime、utf8mb4 中文和 emoji。

MongoDB 覆盖多集合、嵌套文档、数组、ObjectId、日期、中文和 emoji。

Neo4j 覆盖多 label、多 relationship type、节点属性、关系属性、中文属性值。

## 执行方式

```bash
./scripts/compatibility/run.sh postgres
./scripts/compatibility/run.sh mysql
./scripts/compatibility/run.sh mongo
./scripts/compatibility/run.sh neo4j
./scripts/compatibility/run.sh all
./scripts/compatibility/run.sh postgres 13 18
```

常用环境变量：

- `DATABRIDGE_API_BASE`：默认 `http://127.0.0.1:8081/api`
- `DATABRIDGE_AUTH_USER` / `DATABRIDGE_AUTH_PASSWORD`：DataBridge 启用认证时用于 API 登录
- `DATABRIDGE_NETWORK`：默认 `databridge-compat`
- `DATABRIDGE_BACKUP_DIR`：默认 `/tmp/databridge-compat-backups`
- `DATABRIDGE_CONTAINER`：默认 `databridge`
- `DATABRIDGE_IMAGE`：默认 `databridge-compat:local`
- `DATABRIDGE_START_MODE`：默认 `auto`，可设为 `external` 只使用已运行服务
- `DATABRIDGE_REBUILD`：默认 `0`，设为 `1` 时强制重建常驻 DataBridge 容器，适合验证容器内最新代码改动

## 通过标准

单项状态使用 `ok`、`failed`、`skipped` 或 `conditional` 记录。每个组合最终状态只能为：

- `passed`
- `failed`
- `conditional`
- `not-supported`

P0 任一失败时最终状态为 `failed` 或 `not-supported`。P1 因 DataBridge 当前能力边界、上游官方不支持降级恢复或工具链配置问题导致失败时，可记录为 `conditional`，但必须写明原因。

## 当前结论

- PostgreSQL：首轮 5 个主组合中 4 个 `passed`，`18 -> 13` 因 `pg_dump-18` 目录格式头 `1.16` 无法被 `pg_restore-13` 读取而记为 `conditional`；这是上游 PostgreSQL 工具链降级限制，不是 DataBridge 连接或发现失败。
- MySQL：`5.7` 和 `8.0` 首轮 4 个主组合全部 `passed`。当前使用单套 `default-mysql-client` / `mariadb-client` 工具执行验证，未按服务端大版本拆分客户端。
- MongoDB：`5.0`、`6.0`、`7.0`、`8.0` 首轮 4 个主组合全部 `passed`。当前使用单套 MongoDB Database Tools `100.17.0` 执行验证，认证库固定为 `admin`。
- Neo4j：`4.4` 和 `5.x` 首轮 3 个主组合全部 `passed`。为支持 `4.4 -> 5.x`，DataBridge 已在 Neo4j 恢复链路中对 4.4 导出的旧式约束语法做 5.x 兼容转换。

## 已知限制

- 首轮只验证同类型迁移，不验证跨类型迁移。
- PostgreSQL 首轮不以 binary COPY 作为阻塞路径。
- 当前 DataBridge 运行镜像内置 `postgresql-client-13` 到 `postgresql-client-18`，并按 PostgreSQL 源/目标大版本选择对应工具；MySQL 与 MongoDB 仍使用单套客户端工具。
- 已确认 PostgreSQL `18 -> 13` 会因 `pg_dump-18` 目录格式头 `1.16` 不被 `pg_restore-13` 支持而失败；这类问题应记录为工具链兼容性问题，而不是简单归类为数据库版本不兼容。
- MongoDB 首轮不覆盖副本集和分片。
- Neo4j 首轮不覆盖集群。
- Neo4j `4.4 -> 5.x` 已验证需要处理旧式 `CREATE/DROP CONSTRAINT ... ON ... ASSERT ...` 语法；当前仓库内 DataBridge 已在 restore 前做兼容转换。
- 旧版本数据库镜像可能受上游 EOL、平台架构或工具链兼容性影响，需在矩阵备注中明确区分版本不兼容与工具链/配置问题。
