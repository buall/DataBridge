import { LogIn, Moon, RefreshCw, Sun } from "lucide-react";
import { type FormEvent, useCallback, useEffect, useState } from "react";
import { api } from "../api";
import { LoginBackground } from "../components/LoginBackground";
import { BridgeLogo, Button, requiredLabel, useTheme } from "../components/UI";

interface LoginProps {
  configured?: boolean;
  initialCaptchaRequired?: boolean;
  onLogin: (username: string, password: string, captchaId?: string, captchaCode?: string) => Promise<void>;
}

export function Login({ initialCaptchaRequired = false, onLogin }: LoginProps) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [captchaRequired, setCaptchaRequired] = useState(initialCaptchaRequired);
  const [captchaId, setCaptchaId] = useState("");
  const [captchaImage, setCaptchaImage] = useState("");
  const [captchaCode, setCaptchaCode] = useState("");
  const [captchaLoading, setCaptchaLoading] = useState(false);
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const { theme, toggleTheme } = useTheme();

  const loadCaptcha = useCallback(async () => {
    setCaptchaLoading(true);
    try {
      const data = await api.captcha();
      setCaptchaId(data.id);
      setCaptchaImage(data.image);
      setCaptchaCode("");
    } catch (captchaError) {
      setError(captchaError instanceof Error ? captchaError.message : "加载图形验证码失败");
    } finally {
      setCaptchaLoading(false);
    }
  }, []);

  useEffect(() => {
    if (initialCaptchaRequired) {
      setCaptchaRequired(true);
    }
  }, [initialCaptchaRequired]);

  useEffect(() => {
    if (captchaRequired && !captchaImage) {
      void loadCaptcha();
    }
  }, [captchaRequired, captchaImage, loadCaptcha]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSubmitting(true);
    setError("");
    try {
      await onLogin(
        username,
        password,
        captchaRequired ? captchaId : undefined,
        captchaRequired ? captchaCode : undefined,
      );
    } catch (loginError) {
      setError(loginError instanceof Error ? loginError.message : "登录失败");
      const errObj = loginError as { captchaRequired?: boolean; code?: string } | null;
      const needsCaptcha =
        Boolean(errObj?.captchaRequired) ||
        errObj?.code === "captcha_required" ||
        errObj?.code === "invalid_captcha" ||
        captchaRequired;
      if (needsCaptcha) {
        setCaptchaRequired(true);
        void loadCaptcha();
      }
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <main className="login-shell">
      <LoginBackground />
      <button
        type="button"
        className="icon-btn"
        style={{ position: "fixed", top: 18, right: 18, zIndex: 2 }}
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
            <strong id="login-title" style={{ display: "block", fontSize: 17, fontWeight: 700, letterSpacing: "-.02em" }}>
              DataBridge
            </strong>
            <span style={{ display: "block", fontSize: 11.5, color: "var(--muted)", letterSpacing: ".04em" }}>
              数据备份与迁移
            </span>
          </div>
        </div>

        <form onSubmit={submit}>
          <label className="field">
            <span>{requiredLabel("用户名")}</span>
            <input required autoComplete="username" value={username} onChange={(event) => setUsername(event.target.value)} />
          </label>
          <label className="field">
            <span>{requiredLabel("密码")}</span>
            <input
              required
              autoComplete="current-password"
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
          </label>
          {captchaRequired ? (
            <label className="field">
              <span>{requiredLabel("图形验证码")}</span>
              <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                <input
                  required
                  autoComplete="off"
                  placeholder="请输入右侧字符"
                  maxLength={6}
                  value={captchaCode}
                  onChange={(event) => setCaptchaCode(event.target.value)}
                  style={{ flex: 1, minWidth: 0, textTransform: "uppercase" }}
                />
                <button
                  type="button"
                  onClick={() => void loadCaptcha()}
                  disabled={captchaLoading}
                  title="点击刷新验证码"
                  style={{
                    padding: 0,
                    border: "1px solid var(--border)",
                    borderRadius: 8,
                    background: "var(--surface)",
                    cursor: "pointer",
                    height: 38,
                    width: 120,
                    overflow: "hidden",
                    flex: "none",
                    display: "grid",
                    placeItems: "center",
                  }}
                >
                  {captchaImage ? (
                    <img src={captchaImage} alt="图形验证码" style={{ width: "100%", height: "100%", objectFit: "cover", display: "block" }} />
                  ) : (
                    <span style={{ fontSize: 12, color: "var(--muted)" }}>加载中...</span>
                  )}
                </button>
                <button
                  type="button"
                  className="icon-btn"
                  aria-label="刷新图形验证码"
                  disabled={captchaLoading}
                  onClick={() => void loadCaptcha()}
                  style={{ flex: "none", height: 38, width: 38 }}
                >
                  <RefreshCw size={15} />
                </button>
              </div>
            </label>
          ) : null}
          {error ? <div className="error-banner" style={{ marginBottom: 12 }}>{error}</div> : null}
          <Button
            variant="primary"
            disabled={submitting || !username || !password || (captchaRequired && !captchaCode.trim())}
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
