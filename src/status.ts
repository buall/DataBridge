import type { MigrationJob } from "./types";

export function migrationStatusLabel(status: MigrationJob["status"]) {
  return status === "completed"
    ? "已完成"
    : status === "failed"
      ? "已失败"
      : status === "cancelled"
        ? "已取消"
        : status === "pending"
          ? "待开始"
          : "进行中";
}

export function migrationStatusTone(status: MigrationJob["status"]) {
  return status === "completed" ? "green" : status === "failed" || status === "cancelled" ? "red" : "blue";
}
