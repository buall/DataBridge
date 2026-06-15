import { LockKeyhole, LogIn } from "lucide-react";
import { FormEvent, useState } from "react";

interface LoginProps {
  configured: boolean;
  onLogin: (username: string, password: string) => Promise<void>;
}

export function Login({ configured, onLogin }: LoginProps) {
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);

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
    <main className="login-page">
      <section className="login-panel" aria-labelledby="login-title">
        <div className="login-brand">
          <div className="brand-mark">DB</div>
          <div>
            <strong>DataBridge</strong>
            <span>DATABASE MIGRATION</span>
          </div>
        </div>
        <div className="login-heading">
          <span><LockKeyhole size={18} /></span>
          <div>
            <h1 id="login-title">登录控制台</h1>
            <p>公网访问前请先完成身份验证</p>
          </div>
        </div>
        {!configured && (
          <div className="error-banner">
            当前未设置登录密码。公网部署请配置 DATABRIDGE_AUTH_PASSWORD 后重启服务。
          </div>
        )}
        <form className="login-form" onSubmit={submit}>
          <label className="field">
            用户名
            <input autoComplete="username" value={username} onChange={(event) => setUsername(event.target.value)} />
          </label>
          <label className="field">
            密码
            <input
              autoComplete="current-password"
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
          </label>
          {error && <div className="form-error left">{error}</div>}
          <button className="button primary login-submit" disabled={submitting || !username || !password} type="submit">
            <LogIn size={16} />
            {submitting ? "登录中" : "登录"}
          </button>
        </form>
      </section>
    </main>
  );
}
