import { Activity, ArrowRight, FileCheck2, Plus, Search } from "lucide-react";
import { useMemo, useState } from "react";
import { migrationStatusLabel, migrationStatusTone } from "../status";
import type { MigrationJob } from "../types";
import { Badge, Button, Card } from "../components/UI";

export function MigrationList({
  title = "迁移任务",
  description = "查看任务进度、结果和校验报告。",
  migrations,
  onCreate,
  onOpen,
}: {
  title?: string;
  description?: string;
  migrations: MigrationJob[];
  onCreate: () => void;
  onOpen: (job: MigrationJob) => void;
}) {
  const [query, setQuery] = useState("");
  const filtered = useMemo(() => {
    const keyword = query.trim().toLowerCase();
    if (!keyword) return migrations;
    return migrations.filter((item) =>
      `${item.name} ${item.sourceDatabase} ${item.targetDatabase} ${item.sourceName} ${item.targetName}`
        .toLowerCase()
        .includes(keyword),
    );
  }, [migrations, query]);

  const running = migrations.filter((item) => item.status === "running" || item.status === "pending").length;
  const completed = migrations.filter((item) => item.status === "completed").length;

  return (
    <div className="page-stack">
      <div className="page-heading">
        <div><h2>{title}</h2><p>{description}</p></div>
        <Button onClick={onCreate}><Plus size={17} />新建迁移</Button>
      </div>

      <div className="task-summary-grid">
        <TaskSummary title="全部任务" value={migrations.length} detail="任务记录已保存" />
        <TaskSummary title="进行中" value={running} detail="可查看实时进度" tone="blue" />
        <TaskSummary title="已完成" value={completed} detail="可查看校验报告" tone="green" />
      </div>

      <Card className="task-table-card">
        <div className="section-title compact">
          <div><h3>任务列表</h3><p>任务结束后可继续查看结果和报告。</p></div>
          <label className="search-box small"><Search size={15} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索任务或数据库" /></label>
        </div>
        {filtered.length ? (
          <div className="task-table migration-list-table">
            <div className="table-row table-head"><span>任务名称</span><span>源端 / 目标端</span><span>开始时间</span><span>进度</span><span>状态</span><span>操作</span></div>
            {filtered.map((job) => (
              <button className="table-row task-row-button" key={job.id} onClick={() => onOpen(job)}>
                <strong className="task-name">{displayTaskName(job)}</strong>
                <div className="task-route">
                  <strong>{job.sourceName} → {job.targetName}</strong>
                  <small>{job.sourceDatabase} → {job.targetDatabase}</small>
                </div>
                <span>{formatDate(job.startedAt)}</span>
                <div className="table-progress"><div><i style={{ width: `${job.progress}%` }} /></div><small>{job.progress}%</small></div>
                <Badge tone={migrationStatusTone(job.status)}>{migrationStatusLabel(job.status)}</Badge>
                <span className="row-action">{job.status === "running" || job.status === "pending" ? <Activity size={14} /> : <FileCheck2 size={14} />}{job.status === "running" || job.status === "pending" ? "查看进度" : "查看结果"}<ArrowRight size={14} /></span>
              </button>
            ))}
          </div>
        ) : (
          <div className="inline-empty">
            <Activity size={22} />
            <div><strong>{migrations.length ? "未找到匹配任务" : "暂无迁移任务"}</strong><span>{migrations.length ? "换个关键词再试。" : "创建迁移任务后，运行状态和校验报告会显示在这里。"}</span></div>
          </div>
        )}
      </Card>
    </div>
  );
}

function displayTaskName(job: MigrationJob) {
  const databaseRoute = `${job.sourceDatabase} → ${job.targetDatabase}`;
  return job.name && job.name !== databaseRoute ? job.name : `${job.sourceName} 到 ${job.targetName}迁移`;
}

function TaskSummary({ title, value, detail, tone = "gray" }: { title: string; value: number; detail: string; tone?: "gray" | "blue" | "green" }) {
  return <Card className={`task-summary-card ${tone}`}><span>{title}</span><strong>{value}</strong><small>{detail}</small></Card>;
}

function formatDate(value: string) {
  return new Date(value).toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false });
}
