import { Archive, ArrowRight, FileCheck2, Plus, Search } from "lucide-react";
import { useMemo, useState } from "react";
import { migrationStatusLabel, migrationStatusTone } from "../status";
import type { BackupJob } from "../types";
import { Badge, Button, Card } from "../components/UI";

export function BackupList({
  backups,
  onCreate,
  onOpen,
}: {
  backups: BackupJob[];
  onCreate: () => void;
  onOpen: (job: BackupJob) => void;
}) {
  const [query, setQuery] = useState("");
  const filtered = useMemo(() => {
    const keyword = query.trim().toLowerCase();
    if (!keyword) return backups;
    return backups.filter((item) =>
      `${item.name} ${item.sourceName} ${item.sourceDatabase} ${item.destination} ${item.artifactName ?? ""}`.toLowerCase().includes(keyword),
    );
  }, [backups, query]);
  const running = backups.filter((item) => item.status === "running" || item.status === "pending").length;
  const completed = backups.filter((item) => item.status === "completed").length;
  const totalBytes = backups.reduce((sum, item) => sum + (item.artifactSize ?? 0), 0);

  return (
    <div className="page-stack">
      <div className="page-heading">
        <div><h2>备份任务</h2><p>创建数据库备份并同步到本地目录或远端存储。</p></div>
        <Button onClick={onCreate}><Plus size={17} />新建备份</Button>
      </div>
      <div className="task-summary-grid">
        <TaskSummary title="全部任务" value={backups.length} detail="任务记录已保存" />
        <TaskSummary title="进行中" value={running} detail="可查看实时进度" tone="blue" />
        <TaskSummary title="已完成" value={completed} detail={`累计 ${formatBytes(totalBytes)}`} tone="green" />
      </div>
      <Card className="task-table-card">
        <div className="section-title compact">
          <div><h3>任务列表</h3><p>备份完成后可查看文件名、大小和目标位置。</p></div>
          <label className="search-box small"><Search size={15} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索任务或数据库" /></label>
        </div>
        {filtered.length ? (
          <div className="task-table backup-list-table">
            <div className="table-row table-head"><span>任务名称</span><span>源端 / 数据库</span><span>目标存储</span><span>进度</span><span>状态</span><span>操作</span></div>
            {filtered.map((job) => (
              <button className="table-row task-row-button" key={job.id} onClick={() => onOpen(job)}>
                <strong className="task-name">{job.name}</strong>
                <div className="task-route"><strong>{job.sourceName}</strong><small>{job.sourceType} · {job.sourceDatabase}</small></div>
                <span className="task-name">{job.destination}</span>
                <div className="table-progress"><div><i style={{ width: `${job.progress}%` }} /></div><small>{job.progress}%</small></div>
                <Badge tone={migrationStatusTone(job.status)}>{migrationStatusLabel(job.status)}</Badge>
                <span className="row-action">{job.status === "running" || job.status === "pending" ? <Archive size={14} /> : <FileCheck2 size={14} />}{job.status === "running" || job.status === "pending" ? "查看进度" : "查看结果"}<ArrowRight size={14} /></span>
              </button>
            ))}
          </div>
        ) : (
          <div className="inline-empty">
            <Archive size={22} />
            <div><strong>{backups.length ? "未找到匹配任务" : "暂无备份任务"}</strong><span>{backups.length ? "换个关键词再试。" : "创建备份任务后，运行状态和备份文件会显示在这里。"}</span></div>
          </div>
        )}
      </Card>
    </div>
  );
}

function TaskSummary({ title, value, detail, tone = "gray" }: { title: string; value: number; detail: string; tone?: "gray" | "blue" | "green" }) {
  return <Card className={`task-summary-card ${tone}`}><span>{title}</span><strong>{value}</strong><small>{detail}</small></Card>;
}

function formatBytes(value: number) {
  if (!value) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  return `${(value / 1024 ** index).toFixed(index > 2 ? 1 : 0)} ${units[index]}`;
}
