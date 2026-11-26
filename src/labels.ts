import type { ConnectionEnvironment, DatabaseConnection } from "./types";

export const migrationStageLabels: Record<string, string> = {
  pending: "待开始",
  precheck: "连接检查",
  "target-prep": "目标处理",
  schema: "结构准备",
  dump: "源端导出",
  data: "数据传输",
  restore: "目标导入",
  constraints: "约束索引",
  validation: "结果校验",
  completed: "已完成",
  failed: "已失败",
  cancelled: "已取消",
};

export function migrationStageLabel(stage: string) {
  return migrationStageLabels[stage] ?? stage;
}

export const CONNECTION_ENVIRONMENT_OPTIONS: { value: Exclude<ConnectionEnvironment, "">; label: string }[] = [
  { value: "prod", label: "生产 (prod)" },
  { value: "staging", label: "预发 (staging)" },
  { value: "test", label: "测试 (test)" },
  { value: "dev", label: "开发 (dev)" },
];

export function connectionEnvironmentLabel(environment?: string): string {
  switch (environment) {
    case "prod":
      return "生产";
    case "staging":
      return "预发";
    case "test":
      return "测试";
    case "dev":
      return "开发";
    default:
      return "";
  }
}

export function connectionEnvironmentTone(environment?: string): "red" | "indigo" | "blue" | "green" | "gray" {
  switch (environment) {
    case "prod":
      return "red";
    case "staging":
      return "indigo";
    case "test":
      return "blue";
    case "dev":
      return "green";
    default:
      return "gray";
  }
}

export function connectionOptionMeta(connection: Pick<DatabaseConnection, "environment" | "project">): string {
  const envLabel = connectionEnvironmentLabel(connection.environment);
  const parts = [envLabel, connection.project?.trim() || ""].filter(Boolean);
  return parts.length > 0 ? ` [${parts.join(" · ")}]` : "";
}
