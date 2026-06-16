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
