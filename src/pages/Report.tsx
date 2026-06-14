import { Check, Download, Search, ShieldCheck, X } from "lucide-react";
import type { MigrationJob } from "../types";
import { Badge, Button, Card } from "../components/UI";

export function Report({ job }: { job: MigrationJob }) {
  const validCount = job.results.filter((item) => item.valid).length;
  const total = job.results.length;
  const score = total ? Math.round(validCount / total * 100) : 0;
  const allValid = total > 0 && validCount === total;
  return <div className="page-stack">
    <div className="runtime-heading"><div><div><Badge tone={allValid ? "green" : "orange"}>{allValid ? "校验通过" : "存在差异"}</Badge><h2>数据一致性 {score}%</h2></div><p>{total} 张表中，{validCount} 张行数完全一致</p></div><Button variant="secondary" onClick={() => window.print()}><Download size={16}/>导出 / 打印报告</Button></div>
    <div className="report-summary">
      <Card className="quality-score"><span>迁移质量评分</span><div><strong>{score}</strong><small>/ 100</small></div><div className="score-bar"><i style={{width:`${score}%`}} /></div><p>{allValid ? "全部所选表的源端与目标端行数一致" : "请检查存在差异或失败的表"}</p></Card>
      <CheckCard title="结构迁移" value={job.status === "completed" ? "完成" : "异常"} detail={`${job.tablesTotal} 张表`} ok={job.status === "completed"} />
      <CheckCard title="行数一致性" value={`${validCount}/${total}`} detail={`${formatNumber(job.rowsCopied)} 行已复制`} ok={allValid} />
      <CheckCard title="任务耗时" value={duration(job)} detail={`${job.sourceDatabase} → ${job.targetDatabase}`} ok blue />
    </div>
    <Card className="validation-card">
      <div className="section-title compact"><div><h3>逐表校验明细</h3><p>源端与目标端执行精确 count(*) 后的结果</p></div><label className="search-box small"><Search size={15}/><input placeholder="搜索对象"/></label></div>
      <div className="validation-table">
        <div className="validation-row head"><span>对象</span><span>源端行数</span><span>目标端行数</span><span>差异</span><span>耗时</span><span>结果</span><span>详情</span></div>
        {job.results.map((row)=><div className="validation-row" key={row.table}><strong>{row.table}</strong><span>{formatNumber(row.sourceRows)}</span><span>{formatNumber(row.targetRows)}</span><span className={row.valid?"":"difference"}>{formatNumber(Math.abs(row.sourceRows-row.targetRows))}</span><span>{formatDuration(row.durationMs)}</span><Badge tone={row.valid?"green":"orange"}>{row.valid?"一致":"需确认"}</Badge><span>{row.error || "—"}</span></div>)}
      </div>
      <div className="table-footer"><span>任务 ID {job.id}</span><Badge tone={allValid?"green":"orange"}>{allValid?"迁移完成":"需要处理"}</Badge></div>
    </Card>
  </div>;
}

function CheckCard({title,value,detail,ok,blue=false}:{title:string;value:string;detail:string;ok:boolean;blue?:boolean}) {
  return <Card className="check-card"><span className={blue?"blue":ok?"":"warning"}>{blue?<ShieldCheck/>:ok?<Check/>:<X/>}</span><small>{title}</small><strong>{value}</strong><p>{detail}</p><Badge tone={ok?"green":"orange"}>{ok?"通过":"异常"}</Badge></Card>;
}
function formatNumber(value:number){return new Intl.NumberFormat("zh-CN").format(value);}
function formatDuration(ms:number){const seconds=Math.max(0,Math.round(ms/1000));return seconds<60?`${seconds}s`:`${Math.floor(seconds/60)}m ${seconds%60}s`;}
function duration(job:MigrationJob){return job.finishedAt?formatDuration(new Date(job.finishedAt).getTime()-new Date(job.startedAt).getTime()):"—";}
