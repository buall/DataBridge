import {
  ArrowLeftRight,
  Cloud,
  Database,
  LayoutDashboard,
  Moon,
  Plus,
  ScrollText,
  Settings,
  ShieldCheck,
  Sun,
} from "lucide-react";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { migrationStageLabel } from "../labels";
import { migrationStatusLabel } from "../status";
import type { AppView, BackupJob, DatabaseConnection, MigrationJob } from "../types";
import type { ThemeMode } from "./UI";

interface PaletteItem {
  id: string;
  section: string;
  label: string;
  sub?: string;
  icon: ReactNode;
  run: () => void;
}

export function CommandPalette({
  open,
  onClose,
  connections,
  migrations,
  backups,
  theme,
  onToggleTheme,
  onNavigate,
  onCreateMigration,
  onCreateBackup,
  onOpenMigration,
  onOpenBackup,
}: {
  open: boolean;
  onClose: () => void;
  connections: DatabaseConnection[];
  migrations: MigrationJob[];
  backups: BackupJob[];
  theme: ThemeMode;
  onToggleTheme: () => void;
  onNavigate: (view: AppView) => void;
  onCreateMigration: () => void;
  onCreateBackup: () => void;
  onOpenMigration: (job: MigrationJob) => void;
  onOpenBackup: (job: BackupJob) => void;
}) {
  const [query, setQuery] = useState("");
  const [selectedIndex, setSelectedIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    if (open) {
      setQuery("");
      setSelectedIndex(0);
      window.setTimeout(() => inputRef.current?.focus(), 20);
    }
  }, [open]);

  const items = useMemo<PaletteItem[]>(() => {
    const q = query.trim().toLowerCase();
    const out: PaletteItem[] = [];

    const actions: PaletteItem[] = [
      {
        id: "act-mig",
        section: "快捷动作",
        label: "新建迁移任务",
        icon: <ArrowLeftRight size={16} />,
        run: () => onCreateMigration(),
      },
      {
        id: "act-bkp",
        section: "快捷动作",
        label: "新建备份任务",
        icon: <ShieldCheck size={16} />,
        run: () => onCreateBackup(),
      },
      {
        id: "act-conn",
        section: "快捷动作",
        label: "管理数据连接",
        icon: <Plus size={16} />,
        run: () => onNavigate("connections"),
      },
      {
        id: "act-theme",
        section: "快捷动作",
        label: theme === "dark" ? "切换到浅色模式" : "切换到深色模式",
        icon: theme === "dark" ? <Sun size={16} /> : <Moon size={16} />,
        run: () => onToggleTheme(),
      },
    ];
    actions.forEach((item) => {
      if (!q || item.label.toLowerCase().includes(q)) out.push(item);
    });

    connections.forEach((conn) => {
      if (!q || `${conn.name} ${conn.type} ${conn.host} ${conn.note ?? ""}`.toLowerCase().includes(q)) {
        out.push({
          id: `conn-${conn.id}`,
          section: "数据库实例",
          label: conn.name,
          sub: `${conn.type} · ${conn.host}:${conn.port}`,
          icon: <Database size={16} />,
          run: () => onNavigate("connections"),
        });
      }
    });

    migrations.forEach((job) => {
      if (!q || `${job.name} ${job.id} ${job.sourceName} ${job.targetName}`.toLowerCase().includes(q)) {
        out.push({
          id: `mig-${job.id}`,
          section: "迁移任务",
          label: job.name || `${job.sourceName} → ${job.targetName}`,
          sub: `${migrationStatusLabel(job.status)} · ${migrationStageLabel(job.stage)}`,
          icon: <ArrowLeftRight size={16} />,
          run: () => onOpenMigration(job),
        });
      }
    });

    backups.forEach((job) => {
      if (!q || `${job.name} ${job.id} ${job.sourceName} ${job.sourceDatabase}`.toLowerCase().includes(q)) {
        out.push({
          id: `bkp-${job.id}`,
          section: "备份任务",
          label: job.name,
          sub: `${job.sourceName} · ${migrationStatusLabel(job.status)}`,
          icon: <ShieldCheck size={16} />,
          run: () => onOpenBackup(job),
        });
      }
    });

    const pages: Array<[AppView, string, ReactNode]> = [
      ["overview", "工作台总览", <LayoutDashboard size={16} key="ov" />],
      ["connections", "数据连接", <Database size={16} key="cn" />],
      ["backups", "备份任务", <ShieldCheck size={16} key="bk" />],
      ["migrations", "迁移任务", <ArrowLeftRight size={16} key="mg" />],
      ["storages", "远端存储", <Cloud size={16} key="st" />],
      ["logs", "运行日志", <ScrollText size={16} key="lg" />],
      ["settings", "系统设置", <Settings size={16} key="se" />],
    ];
    pages.forEach(([view, label, icon]) => {
      if (!q || label.toLowerCase().includes(q)) {
        out.push({
          id: `nav-${view}`,
          section: "页面导航",
          label,
          icon,
          run: () => onNavigate(view),
        });
      }
    });

    return out.slice(0, 15);
  }, [backups, connections, migrations, onCreateBackup, onCreateMigration, onNavigate, onOpenBackup, onOpenMigration, onToggleTheme, query, theme]);

  useEffect(() => {
    if (selectedIndex >= items.length) {
      setSelectedIndex(Math.max(0, items.length - 1));
    }
  }, [items.length, selectedIndex]);

  useEffect(() => {
    if (!open) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        onClose();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose, open]);

  if (!open) return null;

  let lastSection = "";

  return createPortal(
    <div className="overlay" style={{ placeItems: "start center", paddingTop: "12vh" }} onMouseDown={onClose}>
      <div className="palette" role="dialog" aria-modal="true" aria-label="命令面板" onMouseDown={(event) => event.stopPropagation()}>
        <input
          ref={inputRef}
          value={query}
          onChange={(event) => {
            setQuery(event.target.value);
            setSelectedIndex(0);
          }}
          onKeyDown={(event) => {
            if (event.key === "ArrowDown") {
              event.preventDefault();
              setSelectedIndex((current) => Math.min(current + 1, Math.max(0, items.length - 1)));
            } else if (event.key === "ArrowUp") {
              event.preventDefault();
              setSelectedIndex((current) => Math.max(current - 1, 0));
            } else if (event.key === "Enter" && items[selectedIndex]) {
              event.preventDefault();
              const target = items[selectedIndex];
              onClose();
              target.run();
            }
          }}
          placeholder="搜索连接、任务、备份，或输入命令…"
          autoComplete="off"
        />
        <div className="pal-list">
          {items.length ? (
            items.map((item, idx) => {
              const showSec = item.section !== lastSection;
              lastSection = item.section;
              return (
                <div key={item.id}>
                  {showSec ? <div className="pal-sec">{item.section}</div> : null}
                  <button
                    type="button"
                    className={`pal-item ${idx === selectedIndex ? "sel" : ""}`}
                    onClick={() => {
                      onClose();
                      item.run();
                    }}
                  >
                    {item.icon}
                    <span>{item.label}</span>
                    {item.sub ? <span className="ps" title={item.sub}>{item.sub}</span> : null}
                  </button>
                </div>
              );
            })
          ) : (
            <div style={{ padding: 18, color: "var(--muted)", fontSize: 13.5 }}>没有匹配结果，换个关键词试试。</div>
          )}
        </div>
      </div>
    </div>,
    document.body,
  );
}
