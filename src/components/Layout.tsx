import {
  ArrowLeftRight,
  Cloud,
  Database,
  LayoutDashboard,
  LogOut,
  Menu,
  Moon,
  PanelLeftClose,
  PanelLeftOpen,
  ScrollText,
  Search,
  Settings,
  ShieldCheck,
  Sun,
  type LucideIcon,
} from "lucide-react";
import { Fragment, useEffect, useState, type ReactNode } from "react";
import { migrationStageLabel } from "../labels";
import type { AppView, BackupJob, DatabaseConnection, MigrationJob, RemoteStorage } from "../types";
import { BridgeLogo, type ThemeMode } from "./UI";

type NavItem = { id: AppView; label: string; shortLabel: string; icon: LucideIcon };

const mainNav: NavItem[] = [
  { id: "overview", label: "工作台", shortLabel: "总览", icon: LayoutDashboard },
  { id: "connections", label: "数据连接", shortLabel: "连接", icon: Database },
  { id: "backups", label: "备份任务", shortLabel: "备份", icon: ShieldCheck },
  { id: "migrations", label: "迁移任务", shortLabel: "迁移", icon: ArrowLeftRight },
  { id: "storages", label: "远端存储", shortLabel: "存储", icon: Cloud },
  { id: "logs", label: "运行日志", shortLabel: "日志", icon: ScrollText },
  { id: "settings", label: "系统设置", shortLabel: "设置", icon: Settings },
];

interface LayoutProps {
  activeView: AppView;
  title: string;
  crumb?: string;
  children: ReactNode;
  paths: Record<AppView, string>;
  connections: DatabaseConnection[];
  storages: RemoteStorage[];
  migrations: MigrationJob[];
  backups: BackupJob[];
  username?: string;
  authConfigured?: boolean;
  theme: ThemeMode;
  onToggleTheme: () => void;
  onOpenPalette: () => void;
  onOpenMigration: (job: MigrationJob) => void;
  onNavigate: (view: AppView) => void;
  onLogout: () => void;
}

export function Layout({
  activeView,
  title,
  crumb,
  children,
  paths,
  connections,
  storages,
  migrations,
  backups,
  username = "admin",
  authConfigured = true,
  theme,
  onToggleTheme,
  onOpenPalette,
  onOpenMigration,
  onNavigate,
  onLogout,
}: LayoutProps) {
  const [collapsed, setCollapsed] = useState<boolean>(() => {
    try {
      return window.localStorage.getItem("db-sidebar-collapsed") === "true";
    } catch {
      return false;
    }
  });
  const [mobileOpen, setMobileOpen] = useState(false);

  useEffect(() => {
    if (!mobileOpen) return;
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") setMobileOpen(false);
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [mobileOpen]);

  const effectiveCollapsed = collapsed && !mobileOpen;

  function toggleSidebar() {
    setCollapsed((prev) => {
      const next = !prev;
      try {
        window.localStorage.setItem("db-sidebar-collapsed", String(next));
      } catch {
        // Ignore storage errors.
      }
      return next;
    });
  }

  const runningMigrations = migrations.filter((item) => item.status === "running" || item.status === "pending");
  const failedBackups = backups.filter(
    (item) =>
      item.status === "failed" ||
      ((item.scheduleMode === "scheduled" || item.status === "scheduled") &&
        item.status !== "running" &&
        item.status !== "pending" &&
        item.artifactVersions?.[0]?.status === "failed"),
  ).length;

  const counts: Partial<Record<AppView, number>> = {
    connections: connections.length,
    backups: backups.length,
    migrations: migrations.length,
    storages: storages.length,
  };

  return (
    <>
      <div className="app">
        <div className="workspace">
          {mobileOpen ? (
            <div className="atlas-scrim" aria-hidden="true" onClick={() => setMobileOpen(false)} />
          ) : null}
          <aside
            className={`atlas ${effectiveCollapsed ? "collapsed" : ""} ${mobileOpen ? "mobile-open" : ""}`}
            aria-label="侧边导航"
          >
            <div className="brand" title={effectiveCollapsed ? "DataBridge" : undefined}>
              <span className="glyph">
                <BridgeLogo size={34} />
              </span>
              {!effectiveCollapsed ? (
                <div>
                  <div className="bname">DataBridge</div>
                  <div className="bsub">数据备份与迁移</div>
                </div>
              ) : null}
            </div>

            {!effectiveCollapsed ? <div className="sec-label">工作空间</div> : null}
            <nav aria-label="主导航">
              {mainNav.map(({ id, label, icon: Icon }) => {
                const isActive = activeView === id;
                const count = counts[id];
                const collapsedTip = effectiveCollapsed
                  ? id === "backups" && failedBackups > 0
                    ? `${label}（${failedBackups} 个最近执行失败）`
                    : count != null
                      ? `${label} (${count})`
                      : label
                  : undefined;
                return (
                  <Fragment key={id}>
                    {id === "logs" ? <div className="nav-sep" role="separator" /> : null}
                    <a
                      href={paths[id]}
                      className={`navrow ${isActive ? "active" : ""}`}
                      aria-current={isActive ? "page" : undefined}
                      aria-label={label}
                      data-tooltip-content={collapsedTip}
                      onClick={(event) => {
                        event.preventDefault();
                        setMobileOpen(false);
                        onNavigate(id);
                      }}
                    >
                      <Icon size={16} strokeWidth={1.85} />
                      {!effectiveCollapsed ? <span>{label}</span> : null}
                      {id === "backups" && failedBackups > 0 ? (
                        <span
                          className="dot"
                          data-tooltip-content={effectiveCollapsed ? undefined : `${failedBackups} 个备份任务最近执行失败`}
                        />
                      ) : !effectiveCollapsed && count != null ? (
                        <span className="cnt">{count}</span>
                      ) : null}
                    </a>
                  </Fragment>
                );
              })}
            </nav>

            {!effectiveCollapsed && runningMigrations.length > 0 ? (
              <div className="live-strip">
                <h4>
                  <span className="pulse" />
                  进行中 ({runningMigrations.length})
                </h4>
                {runningMigrations.slice(0, 3).map((job) => (
                  <button
                    key={job.id}
                    type="button"
                    className="live-item"
                    onClick={() => {
                      setMobileOpen(false);
                      onOpenMigration(job);
                    }}
                  >
                    <div className="li-name">
                      <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                        {job.name || `${job.sourceName} → ${job.targetName}`}
                      </span>
                      <span className="pct">{job.progress}%</span>
                    </div>
                    <div className="li-sub">
                      {job.sourceName} → {job.targetName} · {migrationStageLabel(job.stage)}
                    </div>
                    <div className="mini-bar">
                      <i style={{ width: `${job.progress}%` }} />
                    </div>
                  </button>
                ))}
              </div>
            ) : null}

            <div className="atlas-foot">
              <div className="user-chip">
                {!effectiveCollapsed ? (
                  <>
                    <span className="avatar">{(username.trim().slice(0, 2) || "DB").toUpperCase()}</span>
                    <div style={{ flex: 1, minWidth: 0 }}>
                      <div className="un">{username || "admin"}</div>
                    </div>
                  </>
                ) : null}
                <button
                  type="button"
                  className="icon-btn ghost mobile-close-btn"
                  aria-label="关闭菜单"
                  onClick={() => setMobileOpen(false)}
                >
                  <PanelLeftClose size={16} />
                </button>
                <button
                  type="button"
                  className="icon-btn ghost desktop-collapse-btn"
                  aria-label={effectiveCollapsed ? "展开左侧菜单" : "收起左侧菜单"}
                  aria-expanded={!effectiveCollapsed}
                  data-tooltip-content={effectiveCollapsed ? "展开左侧菜单" : "收起左侧菜单"}
                  onClick={toggleSidebar}
                >
                  {effectiveCollapsed ? <PanelLeftOpen size={16} /> : <PanelLeftClose size={16} />}
                </button>
              </div>
            </div>
          </aside>

          <main className="main">
            <header className="cmdbar">
              <button
                type="button"
                className="icon-btn mobile-menu-btn"
                aria-label="打开导航菜单"
                aria-expanded={mobileOpen}
                onClick={() => setMobileOpen(true)}
              >
                <Menu size={17} />
              </button>

              <div className="page-t">
                {crumb ? <span className="crumb">{crumb} / </span> : null}
                {title}
              </div>

              <button
                type="button"
                className="searchbox-trigger"
                aria-label="打开命令面板"
                data-tooltip-content="搜索连接、任务、备份 (⌘K)"
                onClick={onOpenPalette}
              >
                <Search size={15} />
                <span className="ph">搜索连接、任务、备份，或输入命令…</span>
                <span className="kbd">⌘K</span>
              </button>

              <button
                type="button"
                className="icon-btn"
                aria-label={theme === "dark" ? "切换到浅色模式" : "切换到深色模式"}
                data-tooltip-content={theme === "dark" ? "切换到浅色模式" : "切换到深色模式"}
                onClick={onToggleTheme}
              >
                {theme === "dark" ? <Sun size={16} /> : <Moon size={16} />}
              </button>

              <button
                type="button"
                className="icon-btn"
                aria-label="退出登录"
                data-tooltip-content="退出登录"
                onClick={onLogout}
              >
                <LogOut size={16} />
              </button>
            </header>

            <div className="content">{children}</div>
          </main>
        </div>
      </div>
    </>
  );
}
