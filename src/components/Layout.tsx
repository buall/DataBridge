import {
  Activity,
  Bell,
  Cable,
  Database,
  FileCheck2,
  LayoutDashboard,
  ListTree,
  ScrollText,
  Settings,
} from "lucide-react";
import type { AppView } from "../types";

const mainNav = [
  { id: "overview" as const, label: "总览", icon: LayoutDashboard },
  { id: "connections" as const, label: "数据连接", icon: Cable },
  { id: "migrations" as const, label: "迁移任务", icon: Activity },
  { id: "reports" as const, label: "校验报告", icon: FileCheck2 },
  { id: "logs" as const, label: "运行日志", icon: ScrollText },
];

interface LayoutProps {
  activeView: AppView;
  title: string;
  subtitle: string;
  children: React.ReactNode;
  onNavigate: (view: AppView) => void;
}

export function Layout({ activeView, title, subtitle, children, onNavigate }: LayoutProps) {
  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-mark">DB</div>
          <div>
            <strong>DataBridge</strong>
            <span>DATABASE MIGRATION</span>
          </div>
        </div>

        <nav className="primary-nav" aria-label="主导航">
          {mainNav.map(({ id, label, icon: Icon }) => (
            <button
              className={activeView === id ? "nav-item active" : "nav-item"}
              key={id}
              onClick={() => onNavigate(id)}
            >
              <Icon size={18} />
              <span>{label}</span>
            </button>
          ))}
        </nav>

        <div className="resource-nav">
          <span className="nav-caption">资源管理</span>
          <button><Database size={16} />数据库类型</button>
          <button><Settings size={16} />安全与凭证</button>
          <button><ListTree size={16} />Agent 节点</button>
        </div>

        <div className="sidebar-user">
          <div className="avatar">DB</div>
          <div><strong>本地工作区</strong><span>单机运行</span></div>
          <Settings size={16} />
        </div>
      </aside>

      <main className="main-area">
        <header className="topbar">
          <div>
            <h1>{title}</h1>
            <p>{subtitle}</p>
          </div>
          <div className="topbar-actions">
            <span className="environment"><i />服务在线</span>
            <button className="icon-button" aria-label="通知"><Bell size={18} /></button>
            <div className="avatar dark">DB</div>
          </div>
        </header>
        <div className="content">{children}</div>
      </main>
    </div>
  );
}
