import {
  Activity,
  Archive,
  Cable,
  Cloud,
  Database,
  LayoutDashboard,
  LogOut,
  ListTree,
  Menu,
  PanelLeftClose,
  PanelLeftOpen,
  ScrollText,
  Settings,
  SlidersHorizontal,
} from "lucide-react";
import { useEffect, useState } from "react";
import type { AppView } from "../types";

const mainNav = [
  { id: "overview" as const, label: "总览", icon: LayoutDashboard },
  { id: "connections" as const, label: "数据连接", icon: Cable },
  { id: "backups" as const, label: "备份任务", icon: Archive },
  { id: "migrations" as const, label: "迁移任务", icon: Activity },
  { id: "storages" as const, label: "远端存储", icon: Cloud },
  { id: "logs" as const, label: "运行日志", icon: ScrollText },
  { id: "settings" as const, label: "系统设置", icon: SlidersHorizontal },
];

interface LayoutProps {
  activeView: AppView;
  title: string;
  children: React.ReactNode;
  paths: Record<AppView, string>;
  onNavigate: (view: AppView) => void;
  onLogout: () => void;
}

export function Layout({ activeView, title, children, paths, onNavigate, onLogout }: LayoutProps) {
  const [isMobile, setIsMobile] = useState(() => {
    if (typeof window === "undefined") return false;
    return window.matchMedia("(max-width: 960px)").matches;
  });
  const [sidebarCollapsed, setSidebarCollapsed] = useState(() => {
    if (typeof window === "undefined") return false;
    try {
      return window.localStorage.getItem("databridge:sidebar") === "collapsed";
    } catch {
      return false;
    }
  });
  const [mobileNavOpen, setMobileNavOpen] = useState(false);

  useEffect(() => {
    if (typeof window === "undefined") return;
    const media = window.matchMedia("(max-width: 960px)");
    const update = (event?: MediaQueryListEvent) => {
      const nextIsMobile = event ? event.matches : media.matches;
      setIsMobile(nextIsMobile);
      if (!nextIsMobile) {
        setMobileNavOpen(false);
      }
    };
    update();
    if (typeof media.addEventListener === "function") {
      media.addEventListener("change", update);
      return () => media.removeEventListener("change", update);
    }
    media.addListener(update);
    return () => media.removeListener(update);
  }, []);

  useEffect(() => {
    try {
      window.localStorage.setItem("databridge:sidebar", sidebarCollapsed ? "collapsed" : "expanded");
    } catch {
      // The toggle still works when persistent browser storage is unavailable.
    }
  }, [sidebarCollapsed]);

  useEffect(() => {
    if (!isMobile) return;
    setMobileNavOpen(false);
  }, [activeView, isMobile]);

  useEffect(() => {
    if (!isMobile) return;
    const previousOverflow = document.body.style.overflow;
    if (mobileNavOpen) {
      document.body.style.overflow = "hidden";
    }
    return () => {
      document.body.style.overflow = previousOverflow;
    };
  }, [isMobile, mobileNavOpen]);

  const shellClassName = [
    "app-shell",
    sidebarCollapsed && !isMobile ? "sidebar-collapsed" : "",
    isMobile ? "mobile-shell" : "",
    mobileNavOpen ? "mobile-nav-open" : "",
  ].filter(Boolean).join(" ");

  return (
    <div className={shellClassName}>
      {isMobile && mobileNavOpen ? <button type="button" className="sidebar-scrim" aria-label="关闭导航菜单" onClick={() => setMobileNavOpen(false)} /> : null}
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-mark" data-label="DataBridge">DB</div>
          <div className="brand-copy">
            <strong>DataBridge</strong>
            <span>DATABASE MIGRATION</span>
          </div>
        </div>

        <div className="sidebar-scroll">
          <nav className="primary-nav" aria-label="主导航">
            {mainNav.map(({ id, label, icon: Icon }) => (
              <a
                className={activeView === id ? "nav-item active" : "nav-item"}
                href={paths[id]}
                key={id}
                data-label={label}
                data-path={paths[id]}
                title={label}
                aria-current={activeView === id ? "page" : undefined}
                onClick={(event) => {
                  event.preventDefault();
                  onNavigate(id);
                }}
              >
                <Icon size={18} />
                <span>{label}</span>
              </a>
            ))}
          </nav>

          <div className="resource-nav">
            <span className="nav-caption">资源管理</span>
            <button title="数据库类型" data-label="数据库类型"><Database size={16} /><span>数据库类型</span></button>
            <button title="安全与凭证" data-label="安全与凭证"><Settings size={16} /><span>安全与凭证</span></button>
            <button title="Agent 节点" data-label="Agent 节点"><ListTree size={16} /><span>Agent 节点</span></button>
          </div>
        </div>

        <div className="sidebar-user">
          <button
            type="button"
            className="sidebar-user-toggle"
            aria-label={isMobile ? "关闭左侧菜单" : sidebarCollapsed ? "展开左侧菜单" : "收起左侧菜单"}
            aria-expanded={isMobile ? mobileNavOpen : !sidebarCollapsed}
            title={isMobile ? "关闭左侧菜单" : sidebarCollapsed ? "展开左侧菜单" : "收起左侧菜单"}
            data-label={isMobile ? "关闭" : sidebarCollapsed ? "展开" : "收起"}
            onClick={() => {
              if (isMobile) {
                setMobileNavOpen(false);
                return;
              }
              setSidebarCollapsed((current) => !current);
            }}
          >
            {isMobile ? <PanelLeftClose size={16} /> : sidebarCollapsed ? <PanelLeftOpen size={16} /> : <PanelLeftClose size={16} />}
            <span>{isMobile ? "关闭" : sidebarCollapsed ? "展开" : "收起"}</span>
          </button>
        </div>
      </aside>

      <main className="main-area">
        <header className="topbar">
          <div className="topbar-leading">
            {isMobile ? (
              <button
                type="button"
                className="icon-button mobile-nav-button"
                aria-label={mobileNavOpen ? "关闭导航菜单" : "打开导航菜单"}
                aria-expanded={mobileNavOpen}
                onClick={() => setMobileNavOpen((current) => !current)}
              >
                <Menu size={18} />
              </button>
            ) : null}
            <div className="topbar-copy">
              <h1>{title}</h1>
            </div>
          </div>
          <div className="topbar-actions">
            <button className="icon-button" aria-label="退出登录" title="退出登录" onClick={onLogout}><LogOut size={18} /></button>
            <div className="avatar dark">DB</div>
          </div>
        </header>
        <div className="content">{children}</div>
      </main>
    </div>
  );
}
