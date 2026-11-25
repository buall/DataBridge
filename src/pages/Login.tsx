import { AlertTriangle, LockKeyhole, LogIn, Moon, Sun } from "lucide-react";
import { type FormEvent, useState } from "react";
import { BridgeLogo, Button, useTheme } from "../components/UI";

interface LoginProps {
  configured: boolean;
  onLogin: (username: string, password: string) => Promise<void>;
}

export function Login({ configured, onLogin }: LoginProps) {
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const { theme, toggleTheme } = useTheme();

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSubmitting(true);
    setError("");
    try {
      await onLogin(username, password);
    } catch (loginError) {
      setError(loginError instanceof Error ? loginError.message : "登录失败");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <main className="login-shell">
      <button
        type="button"
        className="icon-btn"
        style={{ position: "fixed", top: 18, right: 18 }}
        aria-label={theme === "dark" ? "切换到浅色模式" : "切换到深色模式"}
        onClick={toggleTheme}
      >
        {theme === "dark" ? <Sun size={16} /> : <Moon size={16} />}
      </button>

      <section className="login-card" aria-labelledby="login-title">
        <div className="login-head">
          <span className="glyph">
            <BridgeLogo size={38} />
          </span>
          <div>
            <strong style={{ display: "block", fontSize: 17, fontWeight: 700, letterSpacing: "-.02em" }}>
              DataBridge
            </strong>
            <span style={{ display: "block", fontSize: 11.5, color: "var(--muted)", letterSpacing: ".04em" }}>
              数据备份与迁移
            </span>
          </div>
        </div>

        <div style={{ display: "flex", alignItems: "center", gap: 11, marginBottom: 16 }}>
          <span
            style={{
              width: 34,
              height: 34,
              borderRadius: 10,
              background: "var(--indigo-soft)",
              color: "var(--indigo-ink)",
              display: "grid",
              placeItems: "center",
              flex: "none",
            }}
          >
            <LockKeyhole size={17} />
          </span>
          <div>
            <h1 id="login-title" style={{ margin: 0, fontSize: 18, fontWeight: 700 }}>
              登录控制台
            </h1>
            <p style={{ margin: 0, fontSize: 12.5, color: "var(--muted)" }}>公网访问前请先完成身份验证</p>
          </div>
        </div>

        {!configured ? (
          <div className="login-banner">
            <AlertTriangle size={15} style={{ flex: "none", marginTop: 2 }} />
            <span>
              当前未设置登录密码。公网部署请配置 <code className="mono">DATABRIDGE_AUTH_PASSWORD</code> 后重启服务。
            </span>
          </div>
        ) : null}

        <form onSubmit={submit}>
          <label className="field">
            <span>用户名</span>
            <input autoComplete="username" value={username} onChange={(event) => setUsername(event.target.value)} />
          </label>
          <label className="field">
            <span>密码</span>
            <input
              autoComplete="current-password"
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
          </label>
          {error ? <div className="error-banner" style={{ marginBottom: 12 }}>{error}</div> : null}
          <Button
            variant="primary"
            disabled={submitting || !username || !password}
            type="submit"
            style={{ width: "100%", height: 40 }}
          >
            <LogIn size={16} />
            {submitting ? "正在登录..." : "登录"}
          </Button>
        </form>
      </section>
    </main>
  );
}
