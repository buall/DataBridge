# DataBridge Compatibility Matrix

Last updated: 2026-06-26 05:49:30 UTC

## 总结果摘要

- PostgreSQL：5 个首轮主组合中 4 个 `passed`，`18 -> 13` 为 `conditional`。阻塞原因已定位为上游 `pg_dump-18` / `pg_restore-13` 目录格式不兼容，不是连接、发现或 DataBridge API 链路失败。
- MySQL：4 个首轮主组合全部 `passed`。当前验证基于单套 MySQL/MariaDB 兼容客户端工具。
- MongoDB：4 个首轮主组合全部 `passed`。当前验证基于单套 MongoDB Database Tools `100.17.0`。
- Neo4j：3 个首轮主组合全部 `passed`。其中 `4.4 -> 5.x` 通过修复 DataBridge Neo4j restore 阶段的旧约束语法兼容处理后跑通。

| 数据库类型 | 源版本 | 目标版本 | 连接 | 发现 | 迁移 | 备份 | 恢复 | 最终结论 | 失败原因或备注 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| PostgreSQL | 13 | 13 | ok | ok | ok | ok | ok | passed | report: scripts/compatibility/reports/postgres-13-to-13-20260626-042025 |
| PostgreSQL | 13 | 18 | ok | ok | ok | ok | ok | passed | report: scripts/compatibility/reports/postgres-13-to-18-20260626-042535 |
| PostgreSQL | 18 | 13 | ok | ok | failed | not-run | not-run | conditional | `pg_dump-18` produced directory format header `1.16`, but `pg_restore-13` cannot read it: `unsupported version (1.16) in file header`; upstream toolchain downgrade limitation, report: `scripts/compatibility/reports/postgres-18-to-13-20260626-042555` |
| PostgreSQL | 15 | 15 | ok | ok | ok | ok | ok | passed | report: scripts/compatibility/reports/postgres-15-to-15-20260626-042656 |
| PostgreSQL | 14 | 16 | ok | ok | ok | ok | ok | passed | report: scripts/compatibility/reports/postgres-14-to-16-20260626-042732 |
| MySQL | 5.7 | 5.7 | ok | ok | ok | ok | ok | passed | report: scripts/compatibility/reports/mysql-5-7-to-5-7-20260626-045529 |
| MySQL | 5.7 | 8.0 | ok | ok | ok | ok | ok | passed | report: scripts/compatibility/reports/mysql-5-7-to-8-0-20260626-045642 |
| MySQL | 8.0 | 5.7 | ok | ok | ok | ok | ok | passed | report: scripts/compatibility/reports/mysql-8-0-to-5-7-20260626-045740 |
| MySQL | 8.0 | 8.0 | ok | ok | ok | ok | ok | passed | report: scripts/compatibility/reports/mysql-8-0-to-8-0-20260626-045418 |
| MongoDB | 5.0 | 5.0 | ok | ok | ok | ok | ok | passed | report: scripts/compatibility/reports/mongo-5-0-to-5-0-20260626-050354 |
| MongoDB | 5.0 | 8.0 | ok | ok | ok | ok | ok | passed | report: scripts/compatibility/reports/mongo-5-0-to-8-0-20260626-050809 |
| MongoDB | 8.0 | 5.0 | ok | ok | ok | ok | ok | passed | report: scripts/compatibility/reports/mongo-8-0-to-5-0-20260626-050929 |
| MongoDB | 6.0 | 7.0 | ok | ok | ok | ok | ok | passed | report: scripts/compatibility/reports/mongo-6-0-to-7-0-20260626-051014 |
| Neo4j | 4.4 | 4.4 | ok | ok | ok | ok | ok | passed | report: scripts/compatibility/reports/neo4j-4-4-to-4-4-20260626-052501 |
| Neo4j | 4.4 | 5.x | ok | ok | ok | ok | ok | passed | report: scripts/compatibility/reports/neo4j-4-4-to-5-x-20260626-054323 |
| Neo4j | 5.x | 5.x | ok | ok | ok | ok | ok | passed | report: scripts/compatibility/reports/neo4j-5-x-to-5-x-20260626-054642 |
