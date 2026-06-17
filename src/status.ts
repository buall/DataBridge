import type { BackupJob, MigrationJob } from "./types";

export function migrationStatusLabel(status: MigrationJob["status"] | BackupJob["status"]) {
  return status === "completed"
    ? "已完成"
    : status === "failed"
      ? "已失败"
      : status === "cancelled"
        ? "已取消"
        : status === "scheduled"
          ? "已计划"
          : status === "pending"
            ? "待开始"
            : "进行中";
}

export function migrationStatusTone(status: MigrationJob["status"] | BackupJob["status"]) {
  return status === "completed" ? "green" : status === "failed" || status === "cancelled" ? "red" : status === "scheduled" ? "orange" : "blue";
}
