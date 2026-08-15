import React, { lazy, Suspense, useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  BrowserRouter,
  Navigate,
  Route,
  Routes,
  useLocation,
  useNavigate,
} from "react-router-dom";
import {
  Activity,
  Bell,
  Check,
  Github,
  LogOut,
  Moon,
  MoreHorizontal,
  RefreshCw,
  Settings,
  ShieldCheck,
  Sun,
  Trash2,
  Zap,
} from "lucide-react";
import { api, del, get, post, put, toErrorMessage } from "./api";
import { AuthProvider, useAuth } from "./auth";
import { ThemeProvider, useTheme } from "./theme";
import {
  decodeAccounts,
  decodeAction,
  decodeAuthProfile,
  decodeCode,
  decodeDashboard,
  decodeDeviceLogin,
  decodeGeneral,
  decodeOK,
  decodeSMTP,
  decodeTelegram,
  type Account,
  type Dashboard as Dash,
  type DeviceLogin,
  type GeneralSettings,
  type Limit,
  type SMTPSettingsForm,
  type TelegramSettingsForm,
} from "./types";
import "./styles.css";

const UsageChart = lazy(() => import("./usage-chart"));
const repositoryURL = "https://github.com/GleanAI/codex-helper";

function GitHubLink({ className = "" }: { className?: string }) {
  return (
    <a
      className={`github-link ${className}`.trim()}
      href={repositoryURL}
      target="_blank"
      rel="noopener noreferrer"
      aria-label="在 GitHub 上查看 Codex Helper"
      title="GitHub"
    >
      <Github />
      <span>GitHub</span>
    </a>
  );
}

function App() {
  const { status, authenticated } = useAuth();
  if (!status.initialized) return <Setup version={status.version} />;
  return (
    <BrowserRouter>
      <Routes>
        {authenticated ? (
          <Route path="*" element={<Shell version={status.version} />} />
        ) : (
          <Route path="*" element={<Login version={status.version} />} />
        )}
      </Routes>
    </BrowserRouter>
  );
}
const Splash = () => (
  <main className="center">
    <div className="logo">
      <Zap /> Codex Helper
    </div>
    <div className="spinner" />
  </main>
);
function Brand({ version }: { version?: string }) {
  return (
    <div className="logo">
      <Zap /> <span className="brand-name">Codex Helper</span>
      {version && <span className="version-badge">v{version}</span>}
    </div>
  );
}
function Setup({ version }: { version: string }) {
  const { setup } = useAuth();
  const [form, set] = useState({
    username: "admin",
    password: "",
    timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
  });
  const [err, setErr] = useState("");
  async function submit(e: React.FormEvent) {
    e.preventDefault();
    try {
      await setup(form.username, form.password, form.timezone);
    } catch (x) {
      setErr(toErrorMessage(x));
    }
  }
  return (
    <main className="setup">
      <section className="hero">
        <Brand version={version} />
        <h1>
          连接你的 Codex
          <br />
          <span>看见每一次增长</span>
        </h1>
        <p>一个容器内完成用量洞察、重置提醒与账户管理。</p>
      </section>
      <form className="panel form" onSubmit={submit}>
        <div className="setup-mobile-brand">
          <Brand version={version} />
        </div>
        <small>首次初始化 · 1 / 1</small>
        <h2>创建管理员</h2>
        <label>
          用户名
          <input
            value={form.username}
            onChange={(e) => set({ ...form, username: e.target.value })}
          />
        </label>
        <label>
          密码
          <input
            type="password"
            minLength={10}
            value={form.password}
            onChange={(e) => set({ ...form, password: e.target.value })}
            placeholder="至少 10 位"
          />
        </label>
        <label>
          时区
          <input
            value={form.timezone}
            onChange={(e) => set({ ...form, timezone: e.target.value })}
          />
        </label>
        {err && <p className="error">{err}</p>}
        <button>完成初始化</button>
        <p className="hint">
          Codex、Telegram 与 SMTP 可在进入系统后通过设置中心完成。
        </p>
      </form>
    </main>
  );
}
function Login({ version }: { version: string }) {
  const { login } = useAuth();
  const [u, setU] = useState("admin"),
    [p, setP] = useState(""),
    [e, setE] = useState("");
  return (
    <main className="center">
      <form
        className="panel form login"
        onSubmit={async (x) => {
          x.preventDefault();
          try {
            await login(u, p);
          } catch (q) {
            setE(toErrorMessage(q));
          }
        }}
      >
        <GitHubLink className="login-github" />
        <Brand version={version} />
        <h2>欢迎回来</h2>
        <label>
          用户名
          <input value={u} onChange={(x) => setU(x.target.value)} />
        </label>
        <label>
          密码
          <input
            type="password"
            value={p}
            onChange={(x) => setP(x.target.value)}
          />
        </label>
        {e && <p className="error">{e}</p>}
        <button>登录</button>
      </form>
    </main>
  );
}
function Shell({ version }: { version: string }) {
  const { logout } = useAuth();
  const nav = useNavigate();
  const location = useLocation();
  const { theme, saveTheme, error: themeError } = useTheme();
  const [shellError, setShellError] = useState("");
  const [mobileMenuOpen, setMobileMenuOpen] = useState(false);
  const mobileMenuRef = useRef<HTMLDivElement>(null);
  const mobileMenuButtonRef = useRef<HTMLButtonElement>(null);
  useEffect(() => setMobileMenuOpen(false), [location.pathname]);
  useEffect(() => {
    if (!mobileMenuOpen) return;
    const close = (event: PointerEvent) => {
      if (!mobileMenuRef.current?.contains(event.target as Node))
        setMobileMenuOpen(false);
    };
    const escape = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      setMobileMenuOpen(false);
      mobileMenuButtonRef.current?.focus();
    };
    document.addEventListener("pointerdown", close);
    document.addEventListener("keydown", escape);
    return () => {
      document.removeEventListener("pointerdown", close);
      document.removeEventListener("keydown", escape);
    };
  }, [mobileMenuOpen]);
  const changeTheme = () =>
    void saveTheme(theme === "dark" ? "light" : "dark").catch(() => undefined);
  const signOut = async () => {
    if (!confirm("确定要退出 Codex Helper 管理后台吗？")) return;
    try {
      setShellError("");
      await logout();
    } catch (error) {
      setShellError(toErrorMessage(error));
    }
  };
  return (
    <div className="app">
      <aside>
        <Brand version={version} />
        <nav>
          <button
            className={location.pathname === "/" ? "active" : ""}
            aria-current={location.pathname === "/" ? "page" : undefined}
            onClick={() => nav("/")}
          >
            <Activity />
            总览
          </button>
          <button
            className={location.pathname === "/settings" ? "active" : ""}
            aria-current={
              location.pathname === "/settings" ? "page" : undefined
            }
            onClick={() => nav("/settings")}
          >
            <Settings />
            设置中心
          </button>
        </nav>
        <div className="aside-bottom">
          <GitHubLink />
          <button onClick={changeTheme}>
            {theme === "dark" ? <Sun /> : <Moon />}切换主题
          </button>
          {themeError && <small className="error">{themeError}</small>}
          <button onClick={signOut}>
            <LogOut />
            退出
          </button>
          {shellError && <small className="error">{shellError}</small>}
        </div>
      </aside>
      <div className="mobile-topbar">
        <Brand version={version} />
        <div className="mobile-menu" ref={mobileMenuRef}>
          <button
            ref={mobileMenuButtonRef}
            className="mobile-menu-trigger secondary"
            aria-label="打开更多操作"
            aria-expanded={mobileMenuOpen}
            aria-controls="mobile-actions"
            onClick={() => setMobileMenuOpen((open) => !open)}
          >
            <MoreHorizontal />
          </button>
          {mobileMenuOpen && (
            <div id="mobile-actions" className="mobile-menu-popover">
              <GitHubLink />
              <button onClick={changeTheme}>
                {theme === "dark" ? <Sun /> : <Moon />}切换主题
              </button>
              <button onClick={signOut}>
                <LogOut />
                退出
              </button>
              {(themeError || shellError) && (
                <small className="error">{themeError || shellError}</small>
              )}
            </div>
          )}
        </div>
      </div>
      <section className="content">
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/settings" element={<SettingsPage />} />
          <Route path="*" element={<Navigate to="/" />} />
        </Routes>
      </section>
      <nav className="mobile-bottom-nav" aria-label="主导航">
        <button
          className={location.pathname === "/" ? "active" : ""}
          aria-current={location.pathname === "/" ? "page" : undefined}
          onClick={() => nav("/")}
        >
          <Activity />
          <span>总览</span>
        </button>
        <button
          className={location.pathname === "/settings" ? "active" : ""}
          aria-current={location.pathname === "/settings" ? "page" : undefined}
          onClick={() => nav("/settings")}
        >
          <Settings />
          <span>设置</span>
        </button>
      </nav>
    </div>
  );
}
function Dashboard() {
  const [accounts, setAccounts] = useState<Account[] | null>(null),
    [id, setId] = useState(+(localStorage.accountId || 0)),
    [d, setD] = useState<Dash | null>(null),
    [e, setE] = useState(""),
    [refresh, setRefresh] = useState<"idle" | "loading" | "done">("idle");
  const requestRef = useRef(0);
  const abortRef = useRef<AbortController | null>(null);
  const loadAccounts = async () => {
    try {
      const xs = await get("accounts", decodeAccounts);
      setAccounts(xs);
      setE("");
      const next = xs.some((x) => x.id === id) ? id : xs[0]?.id || 0;
      if (next !== id) setId(next);
    } catch (error) {
      setE(toErrorMessage(error));
    }
  };
  const load = async (accountId = id, signal?: AbortSignal) => {
    if (!accountId) return;
    const request = ++requestRef.current;
    try {
      const dashboard = await get(
        "dashboard?accountId=" + accountId,
        decodeDashboard,
        signal,
      );
      if (request === requestRef.current && dashboard.accountId === accountId) {
        setD(dashboard);
        setE("");
      }
    } catch (x) {
      if (!signal?.aborted && request === requestRef.current)
        setE(toErrorMessage(x));
    }
  };
  useEffect(() => {
    void loadAccounts();
  }, []);
  useEffect(() => {
    if (!id) return;
    localStorage.setItem("accountId", String(id));
    setD(null);
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    let timer = 0;
    const poll = async () => {
      await load(id, controller.signal);
      if (!controller.signal.aborted) timer = window.setTimeout(poll, 30_000);
    };
    void poll();
    return () => {
      controller.abort();
      window.clearTimeout(timer);
    };
  }, [id]);
  const sync = async () => {
    setRefresh("loading");
    setE("");
    try {
      await post(`accounts/${id}/sync`, decodeOK, {}, undefined, 60_000);
      await load(id);
      setRefresh("done");
      window.setTimeout(() => setRefresh("idle"), 2000);
    } catch (x) {
      setE(toErrorMessage(x));
      setRefresh("idle");
    }
  };
  if (accounts === null)
    return (
      <>
        <Header title="用量总览" />
        {e ? (
          <div className="panel empty">
            <p className="error">{e}</p>
            <button onClick={() => void loadAccounts()}>重试</button>
          </div>
        ) : (
          <Splash />
        )}
      </>
    );
  if (!accounts.length)
    return (
      <>
        <Header title="用量总览" sub="请先在设置中心添加账号" />
        <div className="panel empty">尚未添加 Codex 账号</div>
      </>
    );
  if (!d)
    return (
      <>
        <Header title="用量总览" />
        {e ? (
          <div className="panel empty">
            <p className="error">{e}</p>
            <button onClick={() => void load(id)}>重试</button>
          </div>
        ) : (
          <Splash />
        )}
      </>
    );
  return (
    <>
      <Header title="用量总览">
        <div className="header-actions">
          <select
            className="account-select"
            value={id}
            onChange={(x) => setId(+x.target.value)}
          >
            {accounts.map((x) => (
              <option key={x.id} value={x.id}>
                {x.displayName}
                {x.email ? " · " + maskEmail(x.email) : ""}
              </option>
            ))}
          </select>
          <button
            className={"secondary refresh " + refresh}
            disabled={refresh === "loading"}
            onClick={sync}
          >
            {refresh === "done" ? <Check /> : <RefreshCw />}
            {refresh === "loading"
              ? "刷新中"
              : refresh === "done"
                ? "刷新完成"
                : "立即刷新"}
          </button>
        </div>
      </Header>
      {e && <div className="banner">{e}</div>}
      <BalancePanel
        limits={d.limits}
        planType={d.account.planType}
        fetchedAt={d.fetchedAt}
      />
      <div className="stats">
        <Stat label="累计 Tokens" v={num(d.summary.lifetimeTokens)} />
        <Stat label="单日峰值 Tokens" v={num(d.summary.peakDailyTokens)} />
        <Stat label="历史天数" v={d.usage.length + " 天"} />
        <Stat
          label="最长任务时长"
          v={duration(d.summary.longestRunningTurnSec)}
        />
      </div>
      <Suspense fallback={<SettingsLoading />}>
        <UsageChart data={d.usage} />
      </Suspense>
    </>
  );
}
function BalancePanel({
  limits,
  planType,
  fetchedAt,
}: {
  limits: Limit[];
  planType: string | null;
  fetchedAt: number;
}) {
  return (
    <section className="panel balance">
      <div className="balance-header">
        <small>余额</small>
        <div className="balance-meta">
          <span className="badge">{planLabel(planType)}</span>
          <span className="balance-updated">
            更新于{" "}
            {fetchedAt
              ? new Date(fetchedAt * 1000).toLocaleString()
              : "尚未同步"}
          </span>
        </div>
      </div>
      {limits.length ? (
        <div className="balance-windows">
          {limits.map((x) => (
            <LimitWindow key={`${x.limitId}:${x.windowType}`} x={x} />
          ))}
        </div>
      ) : (
        <div className="balance-empty">
          该连接暂无限额数据，请确认登录后刷新。
        </div>
      )}
    </section>
  );
}
function LimitWindow({ x }: { x: Limit }) {
  const used = Math.min(100, Math.max(0, x.usedPercent)),
    left = 100 - used,
    usedLabel = Math.round(used),
    leftLabel = Math.round(left),
    leftColor = `hsl(${left * 1.2} 70% 45%)`,
    windowLabel = limitWindowLabel(x);
  return (
    <div className="limit-window">
      <small>{windowLabel}</small>
      <div className="remaining">
        <strong style={{ color: leftColor }}>
          {leftLabel}
          <i>%</i>
        </strong>
        <span>剩余</span>
      </div>
      <div className="reset">
        <small>下次重置</small>
        <b>
          {x.resetsAt
            ? new Date(x.resetsAt * 1000).toLocaleString()
            : "暂未提供"}
        </b>
        <span>{x.resetsAt ? relative(x.resetsAt) : ""}</span>
      </div>
      <div
        className="bar"
        aria-label={`${windowLabel}：已使用 ${usedLabel}%，剩余 ${leftLabel}%`}
      >
        <em style={{ width: left + "%", background: leftColor }} />
        <i style={{ width: used + "%" }} />
      </div>
    </div>
  );
}
const Stat = ({
  label,
  v,
  muted = false,
}: {
  label: string;
  v: string;
  muted?: boolean;
}) => (
  <div className="panel stat">
    <small>{label}</small>
    <b className={muted ? "muted" : ""}>{v}</b>
  </div>
);
type SettingsTab = "general" | "security" | "codex" | "telegram" | "smtp";

const settingsTabs: Array<{
  id: SettingsTab;
  label: string;
  icon: React.ReactNode;
  content: React.ReactNode;
}> = [
  { id: "general", label: "通用", icon: <Settings />, content: <General /> },
  {
    id: "security",
    label: "安全",
    icon: <ShieldCheck />,
    content: <SecuritySettings />,
  },
  { id: "codex", label: "Codex", icon: <Zap />, content: <CodexSettings /> },
  {
    id: "telegram",
    label: "Telegram",
    icon: <Bell />,
    content: <Telegram />,
  },
  { id: "smtp", label: "SMTP 邮件", icon: <Bell />, content: <SMTP /> },
];

function SettingsPage() {
  const [tab, setTab] = useState<SettingsTab>("general");
  const selectAdjacentTab = (
    event: React.KeyboardEvent<HTMLButtonElement>,
    currentIndex: number,
  ) => {
    let nextIndex: number | undefined;
    if (event.key === "ArrowRight" || event.key === "ArrowDown")
      nextIndex = (currentIndex + 1) % settingsTabs.length;
    if (event.key === "ArrowLeft" || event.key === "ArrowUp")
      nextIndex =
        (currentIndex - 1 + settingsTabs.length) % settingsTabs.length;
    if (event.key === "Home") nextIndex = 0;
    if (event.key === "End") nextIndex = settingsTabs.length - 1;
    if (nextIndex === undefined) return;

    event.preventDefault();
    const nextTab = settingsTabs[nextIndex].id;
    setTab(nextTab);
    document.getElementById(`settings-tab-${nextTab}`)?.focus();
  };
  return (
    <>
      <Header title="设置中心" />
      <div className="settings">
        <div className="tabs" role="tablist" aria-label="设置分类">
          {settingsTabs.map((item, index) => (
            <button
              key={item.id}
              id={`settings-tab-${item.id}`}
              type="button"
              role="tab"
              aria-selected={tab === item.id}
              aria-controls={`settings-panel-${item.id}`}
              tabIndex={tab === item.id ? 0 : -1}
              className={tab === item.id ? "active" : ""}
              onClick={() => setTab(item.id)}
              onKeyDown={(event) => selectAdjacentTab(event, index)}
            >
              {item.icon}
              {item.label}
            </button>
          ))}
        </div>
        <div className="settings-content">
          {settingsTabs.map((item) => {
            const active = tab === item.id;
            return (
              <section
                key={item.id}
                id={`settings-panel-${item.id}`}
                className={`settings-panel${active ? " active" : ""}`}
                role="tabpanel"
                aria-labelledby={`settings-tab-${item.id}`}
                aria-hidden={!active}
                inert={!active}
              >
                {item.content}
              </section>
            );
          })}
        </div>
      </div>
    </>
  );
}
const SettingsLoading = () => (
  <div
    className="panel wide settings-loading"
    role="status"
    aria-label="正在加载设置"
  >
    <div className="spinner" />
  </div>
);
function SecuritySettings() {
  const [username, setUsername] = useState("");
  const [savedUsername, setSavedUsername] = useState("");
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [message, setMessage] = useState("");
  const [isError, setIsError] = useState(false);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);

  const loadProfile = async () => {
    try {
      setLoading(true);
      setMessage("");
      const profile = await get("auth/me", decodeAuthProfile);
      setUsername(profile.username);
      setSavedUsername(profile.username);
      setIsError(false);
    } catch (error) {
      setMessage(toErrorMessage(error));
      setIsError(true);
    } finally {
      setLoading(false);
    }
  };
  useEffect(() => {
    void loadProfile();
  }, []);

  if (loading) return <SettingsLoading />;
  if (!savedUsername && isError)
    return (
      <div className="panel wide">
        <p className="error">{message}</p>
        <button onClick={() => void loadProfile()}>重试</button>
      </div>
    );

  const changed = username !== savedUsername || newPassword !== "";
  return (
    <form
      className="panel form wide"
      onSubmit={async (event) => {
        event.preventDefault();
        if (newPassword !== confirmPassword) {
          setMessage("两次输入的新密码不一致");
          setIsError(true);
          return;
        }
        try {
          setSaving(true);
          setMessage("");
          const profile = await put("auth/credentials", decodeAuthProfile, {
            username,
            currentPassword,
            newPassword,
          });
          setUsername(profile.username);
          setSavedUsername(profile.username);
          setCurrentPassword("");
          setNewPassword("");
          setConfirmPassword("");
          setMessage("登录凭据已更新，其他设备需要重新登录");
          setIsError(false);
        } catch (error) {
          setMessage(toErrorMessage(error));
          setIsError(true);
        } finally {
          setSaving(false);
        }
      }}
    >
      <h2>登录安全</h2>
      <p className="hint">修改用户名或密码前，需要验证当前密码。</p>
      <label>
        用户名
        <input
          value={username}
          minLength={3}
          required
          autoComplete="username"
          onChange={(event) => setUsername(event.target.value)}
        />
      </label>
      <label>
        当前密码
        <input
          type="password"
          value={currentPassword}
          required={changed}
          autoComplete="current-password"
          onChange={(event) => setCurrentPassword(event.target.value)}
        />
      </label>
      <label>
        新密码
        <input
          type="password"
          value={newPassword}
          minLength={10}
          autoComplete="new-password"
          placeholder="留空表示不修改"
          onChange={(event) => setNewPassword(event.target.value)}
        />
      </label>
      <label>
        确认新密码
        <input
          type="password"
          value={confirmPassword}
          required={newPassword !== ""}
          autoComplete="new-password"
          onChange={(event) => setConfirmPassword(event.target.value)}
        />
      </label>
      <button disabled={!changed || saving}>
        {saving ? "正在保存…" : "更新登录凭据"}
      </button>
      {message && <p className={isError ? "error" : "hint"}>{message}</p>}
    </form>
  );
}
function General() {
  const [v, setV] = useState<GeneralSettings | null>(null),
    [msg, setMsg] = useState(""),
    [loadError, setLoadError] = useState("");
  const { applyTheme } = useTheme();
  const loadGeneral = async () => {
    try {
      setLoadError("");
      const value = await get("settings/general", decodeGeneral);
      setV(value);
      applyTheme(value.theme);
    } catch (error) {
      setLoadError(toErrorMessage(error));
    }
  };
  useEffect(() => {
    void loadGeneral();
  }, []);
  if (!v)
    return loadError ? (
      <div className="panel wide">
        <p className="error">{loadError}</p>
        <button onClick={() => void loadGeneral()}>重试</button>
      </div>
    ) : (
      <SettingsLoading />
    );
  return (
    <form
      className="panel form wide"
      onSubmit={async (e) => {
        e.preventDefault();
        try {
          const saved = await api("settings/general", decodeGeneral, {
            method: "PUT",
            body: JSON.stringify(v),
          });
          setV(saved);
          applyTheme(saved.theme);
          setMsg("设置已保存");
        } catch (x) {
          setMsg(toErrorMessage(x));
        }
      }}
    >
      <h2>通用设置</h2>
      <div className="grid2">
        <label>
          时区
          <input
            value={v.timezone}
            onChange={(e) => setV({ ...v, timezone: e.target.value })}
          />
        </label>
        <label>
          主题
          <select
            value={v.theme}
            onChange={(e) => {
              const theme = e.target.value;
              if (theme === "system" || theme === "dark" || theme === "light")
                setV({ ...v, theme });
            }}
          >
            <option value="system">跟随系统</option>
            <option value="dark">深色</option>
            <option value="light">浅色</option>
          </select>
        </label>
        <label>
          同步间隔（分钟）
          <input
            type="number"
            min="1"
            max="60"
            value={v.syncMinutes}
            onChange={(e) => setV({ ...v, syncMinutes: +e.target.value })}
          />
        </label>
        <label>
          历史保留（天）
          <select
            value={v.retentionDays}
            onChange={(e) => setV({ ...v, retentionDays: +e.target.value })}
          >
            {[30, 60, 90, 180, 365].map((x) => (
              <option key={x}>{x}</option>
            ))}
          </select>
        </label>
        <label>
          提前提醒（分钟）
          <input
            type="number"
            min="1"
            max="1440"
            value={v.beforeMinutes}
            onChange={(e) => setV({ ...v, beforeMinutes: +e.target.value })}
          />
        </label>
        <label className="checks">
          <input
            type="checkbox"
            checked={v.notifyBefore}
            onChange={(e) => setV({ ...v, notifyBefore: e.target.checked })}
          />
          重置前提醒{" "}
          <input
            type="checkbox"
            checked={v.notifyAfter}
            onChange={(e) => setV({ ...v, notifyAfter: e.target.checked })}
          />
          重置后确认
        </label>
      </div>
      <button>保存设置</button>
      {msg && <p className="hint">{msg}</p>}
    </form>
  );
}
function CodexSettings() {
  const [xs, setXs] = useState<Account[]>([]),
    [deviceLogin, setDeviceLogin] = useState<DeviceLogin | null>(null),
    [active, setActive] = useState(0),
    [newKind, setNewKind] = useState<"personal" | "team">("team"),
    [busy, setBusy] = useState(false),
    [err, setErr] = useState("");
  const load = async (signal?: AbortSignal) => {
    const accounts = await get("accounts", decodeAccounts, signal);
    setXs(accounts);
    return accounts;
  };
  useEffect(() => {
    void load().catch((error) => setErr(toErrorMessage(error)));
  }, []);
  useEffect(() => {
    if (!deviceLogin) return;
    const started = Date.now();
    const controller = new AbortController();
    let timer = 0;
    const poll = async () => {
      try {
        const accounts = await load(controller.signal);
        const account = accounts.find((x) => x.id === deviceLogin.accountId);
        if (!account) {
          setDeviceLogin(null);
          return;
        }
        if (["matched", "mismatch"].includes(account.validationStatus)) return;
        if (Date.now() - started >= 120_000) {
          setErr("设备码登录检测已超时，请重新生成设备码");
          return;
        }
        timer = window.setTimeout(poll, 2000);
      } catch (error) {
        if (!controller.signal.aborted) {
          setErr(toErrorMessage(error));
          timer = window.setTimeout(poll, 2000);
        }
      }
    };
    timer = window.setTimeout(poll, 2000);
    return () => {
      controller.abort();
      window.clearTimeout(timer);
    };
  }, [deviceLogin]);
  const login = async (id: number) => {
    try {
      setBusy(true);
      setActive(id);
      setErr("");
      setDeviceLogin(null);
      const result = await post(
        `accounts/${id}/login/device`,
        decodeDeviceLogin,
        {},
        undefined,
        60_000,
      );
      setDeviceLogin({ accountId: id, ...result });
    } catch (q) {
      setErr(toErrorMessage(q));
    } finally {
      setBusy(false);
    }
  };
  const add = async () => {
    try {
      setBusy(true);
      setActive(0);
      setErr("");
      setDeviceLogin(null);
      const x = await post("accounts", (value) => decodeAccounts([value])[0], {
        displayName: `账号 ${xs.length + 1}`,
        expectedKind: newKind,
      });
      await load();
      setActive(x.id);
      const result = await post(
        `accounts/${x.id}/login/device`,
        decodeDeviceLogin,
        {},
        undefined,
        60_000,
      );
      setDeviceLogin({ accountId: x.id, ...result });
    } catch (q) {
      setErr(toErrorMessage(q));
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="codex-settings wide">
      <div className="codex-heading">
        <h2>Codex 账户与工作区</h2>
        <p>
          个人订阅和 Team 工作区请分别添加为独立连接；同一邮箱可以添加多次。
        </p>
      </div>
      <section className="panel add-account-card">
        <div>
          <h3>添加新连接</h3>
          <p>选择要连接的订阅类型，然后使用设备码完成授权。</p>
        </div>
        <div className="add-account-controls">
          <label>
            连接类型
            <select
              value={newKind}
              onChange={(e) =>
                setNewKind(e.target.value as "personal" | "team")
              }
            >
              <option value="team">Team / Business 工作区</option>
              <option value="personal">个人订阅</option>
            </select>
          </label>
          <button disabled={busy} onClick={add}>
            {busy && active === 0 ? "服务启动中…" : "添加账号"}
          </button>
        </div>
      </section>
      <div className="account-list">
        {xs.length === 0 && (
          <div className="panel empty account-empty">
            尚未添加 Codex 账号，请使用上方的“添加账号”创建连接。
          </div>
        )}
        {xs.map((x) => (
          <section className="panel account-card" key={x.id}>
            <div className="account-card-main">
              <span className={"dot " + (x.connected ? "ok" : "")} />
              <div className="account-details">
                <input
                  aria-label="连接名称"
                  defaultValue={x.displayName}
                  onBlur={async (e) => {
                    const name = e.target.value.trim();
                    if (name && name !== x.displayName) {
                      await put(`accounts/${x.id}`, decodeOK, {
                        displayName: name,
                        expectedKind: x.expectedKind,
                      });
                      void load().catch((error) =>
                        setErr(toErrorMessage(error)),
                      );
                    }
                  }}
                />
                <div className="account-meta">
                  <span>{x.email ? maskEmail(x.email) : "尚未登录"}</span>
                  <span>{planLabel(x.planType)}</span>
                  <span
                    className={x.validationStatus === "mismatch" ? "error" : ""}
                  >
                    {validationLabel(x)}
                  </span>
                </div>
                {x.possibleDuplicate && (
                  <small className="hint">
                    同一邮箱已有相同类型连接，请确认没有重复授权同一工作区
                  </small>
                )}
              </div>
            </div>
            <div className="account-card-actions">
              <label>
                预期连接类型
                <select
                  aria-label="预期连接类型"
                  value={x.expectedKind}
                  onChange={async (e) => {
                    await put(`accounts/${x.id}`, decodeOK, {
                      displayName: x.displayName,
                      expectedKind: e.target.value,
                    });
                    void load().catch((error) => setErr(toErrorMessage(error)));
                  }}
                >
                  <option value="any">不校验</option>
                  <option value="personal">个人订阅</option>
                  <option value="team">Team / Business</option>
                </select>
              </label>
              <div className="account-buttons">
                <button
                  className="secondary"
                  disabled={busy}
                  onClick={() => login(x.id)}
                >
                  {busy && active === x.id ? "服务启动中…" : "设备码登录"}
                </button>
                <button
                  className="secondary"
                  disabled={busy}
                  onClick={async () => {
                    if (
                      !confirm(`确定要退出“${x.displayName}”的 Codex 账号吗？`)
                    )
                      return;
                    try {
                      setErr("");
                      await post(`accounts/${x.id}/logout`, decodeOK);
                      if (deviceLogin?.accountId === x.id) setDeviceLogin(null);
                      await load();
                    } catch (q) {
                      setErr(toErrorMessage(q));
                    }
                  }}
                >
                  退出
                </button>
                <button
                  className="icon danger"
                  disabled={busy}
                  title="删除连接"
                  aria-label={`删除“${x.displayName}”`}
                  onClick={async () => {
                    if (
                      !confirm(
                        `删除“${x.displayName}”及其全部凭据和历史数据？此操作无法撤销。`,
                      )
                    )
                      return;
                    try {
                      setErr("");
                      await del(`accounts/${x.id}`, decodeOK);
                      if (deviceLogin?.accountId === x.id) setDeviceLogin(null);
                      if (active === x.id) setActive(0);
                      if (+localStorage.accountId === x.id)
                        localStorage.removeItem("accountId");
                      await load();
                    } catch (q) {
                      setErr(toErrorMessage(q));
                    }
                  }}
                >
                  <Trash2 />
                </button>
              </div>
            </div>
            {deviceLogin?.accountId === x.id && (
              <div className="codebox">
                <span>
                  在浏览器中访问{" "}
                  <a
                    href={deviceLogin.verificationUrl}
                    target="_blank"
                    rel="noreferrer"
                  >
                    {deviceLogin.verificationUrl}
                  </a>
                  ，然后输入设备码
                </span>
                <strong>{deviceLogin.userCode}</strong>
              </div>
            )}
          </section>
        ))}
      </div>
      {err && <p className="error">{err}</p>}
    </div>
  );
}
function Telegram() {
  const [v, setV] = useState<TelegramSettingsForm | null>(null),
    [msg, setMsg] = useState(""),
    [code, setCode] = useState(""),
    [loadError, setLoadError] = useState("");
  const loadTelegram = async () => {
    try {
      setLoadError("");
      const value = await get("settings/telegram", decodeTelegram);
      setV({ ...value, token: "" });
    } catch (error) {
      setLoadError(toErrorMessage(error));
    }
  };
  useEffect(() => {
    void loadTelegram();
  }, []);
  if (!v)
    return loadError ? (
      <div className="panel wide">
        <p className="error">{loadError}</p>
        <button onClick={() => void loadTelegram()}>重试</button>
      </div>
    ) : (
      <SettingsLoading />
    );
  return (
    <form
      className="panel form wide"
      onSubmit={async (e) => {
        e.preventDefault();
        try {
          const x = await api("settings/telegram", decodeTelegram, {
            method: "PUT",
            body: JSON.stringify(v),
          });
          setV({ ...x, token: "" });
          setMsg(x.warning || "Bot 已验证并保存");
        } catch (x) {
          setMsg(toErrorMessage(x));
        }
      }}
    >
      <h2>Telegram Bot</h2>
      <label>
        Bot Token
        <input
          type="password"
          value={v.token || ""}
          onChange={(e) => setV({ ...v, token: e.target.value })}
          placeholder={
            v.configured ? "已配置，留空表示不修改" : "从 BotFather 获取"
          }
        />
      </label>
      <p className="hint">Bot 绑定后会自动启用额度提醒和查询菜单。</p>
      <button>验证并保存</button>
      <div className="actions">
        <button
          type="button"
          className="secondary"
          onClick={async () => {
            try {
              const x = await post("settings/telegram/bind", decodeCode);
              setCode(x.code);
            } catch (error) {
              setMsg(toErrorMessage(error));
            }
          }}
        >
          生成绑定码
        </button>
        <button
          type="button"
          className="secondary"
          onClick={async () => {
            try {
              await post("settings/telegram/test", decodeOK);
              setMsg("测试消息已发送");
            } catch (x) {
              setMsg(toErrorMessage(x));
            }
          }}
        >
          发送测试
        </button>
        <button
          type="button"
          className="danger"
          disabled={!v.configured}
          onClick={async () => {
            if (
              !confirm(
                "确定要解除 Telegram Bot 绑定吗？这会删除 Bot Token、Chat ID 和未使用的绑定码。",
              )
            )
              return;
            try {
              const x = await del("settings/telegram", decodeAction);
              setV({
                chatId: 0,
                enabled: false,
                menuEnabled: false,
                configured: false,
                token: "",
              });
              setCode("");
              setMsg(x.warning || "Telegram Bot 配置已删除");
            } catch (error) {
              setMsg(toErrorMessage(error));
            }
          }}
        >
          <Trash2 />
          解除绑定
        </button>
      </div>
      {code && (
        <div className="codebox">
          <span>向 Bot 发送</span>
          <strong>/bind {code}</strong>
        </div>
      )}
      {msg && <p className="hint">{msg}</p>}
    </form>
  );
}
function SMTP() {
  const [v, setV] = useState<SMTPSettingsForm | null>(null),
    [msg, setMsg] = useState(""),
    [loadError, setLoadError] = useState("");
  const loadSMTP = async () => {
    try {
      setLoadError("");
      const value = await get("settings/smtp", decodeSMTP);
      setV({ ...value, password: "" });
    } catch (error) {
      setLoadError(toErrorMessage(error));
    }
  };
  useEffect(() => {
    void loadSMTP();
  }, []);
  if (!v)
    return loadError ? (
      <div className="panel wide">
        <p className="error">{loadError}</p>
        <button onClick={() => void loadSMTP()}>重试</button>
      </div>
    ) : (
      <SettingsLoading />
    );
  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      const saved = await api("settings/smtp", decodeSMTP, {
        method: "PUT",
        body: JSON.stringify(v),
      });
      setV({ ...saved, password: "" });
      setMsg("SMTP 设置已保存");
    } catch (x) {
      setMsg(toErrorMessage(x));
    }
  };
  return (
    <form className="panel form wide" onSubmit={save}>
      <h2>SMTP 邮件</h2>
      <div className="grid2">
        <label>
          服务器
          <input
            value={v.host}
            onChange={(e) => setV({ ...v, host: e.target.value })}
            placeholder="smtp.example.com"
          />
        </label>
        <label>
          端口
          <input
            type="number"
            value={v.port}
            onChange={(e) => setV({ ...v, port: +e.target.value })}
          />
        </label>
        <label>
          用户名
          <input
            value={v.username}
            onChange={(e) => setV({ ...v, username: e.target.value })}
          />
        </label>
        <label>
          密码
          <input
            type="password"
            value={v.password || ""}
            onChange={(e) => setV({ ...v, password: e.target.value })}
            placeholder={v.configured ? "已配置，留空不修改" : ""}
          />
        </label>
        <label>
          加密
          <select
            value={v.security}
            onChange={(e) => {
              const security = e.target.value;
              if (
                security === "starttls" ||
                security === "tls" ||
                security === "none"
              )
                setV({ ...v, security });
            }}
          >
            <option value="starttls">STARTTLS</option>
            <option value="tls">隐式 TLS</option>
            <option value="none">无加密</option>
          </select>
        </label>
        <label>
          发件名称
          <input
            value={v.fromName}
            onChange={(e) => setV({ ...v, fromName: e.target.value })}
          />
        </label>
        <label>
          发件地址
          <input
            type="email"
            value={v.from}
            onChange={(e) => setV({ ...v, from: e.target.value })}
          />
        </label>
        <label>
          收件地址
          <input
            type="email"
            value={v.to}
            onChange={(e) => setV({ ...v, to: e.target.value })}
          />
        </label>
      </div>
      <label className="checks">
        <input
          type="checkbox"
          checked={v.enabled}
          onChange={(e) => setV({ ...v, enabled: e.target.checked })}
        />
        启用邮件提醒
      </label>
      <div className="actions">
        <button>保存</button>
        <button
          type="button"
          className="secondary"
          onClick={async () => {
            try {
              await post("settings/smtp/test", decodeOK);
              setMsg("测试邮件已发送");
            } catch (x) {
              setMsg(toErrorMessage(x));
            }
          }}
        >
          发送测试邮件
        </button>
      </div>
      {msg && <p className="hint">{msg}</p>}
    </form>
  );
}
function Header({
  title,
  sub,
  children,
}: {
  title: string;
  sub?: string;
  children?: React.ReactNode;
}) {
  return (
    <header>
      <div>
        <h1>{title}</h1>
        {sub && <p>{sub}</p>}
      </div>
      {children}
    </header>
  );
}
const num = (v?: number | null) =>
  v == null
    ? "暂无"
    : new Intl.NumberFormat("zh-CN", {
        notation: "compact",
        maximumFractionDigits: 1,
      }).format(v);
const duration = (v?: number | null) =>
  v == null
    ? "暂无"
    : v < 60
      ? `${v} 秒`
      : v < 3600
        ? `${Math.floor(v / 60)} 分 ${v % 60} 秒`
        : `${(v / 3600).toFixed(1)} 小时`;
const limitWindowLabel = (limit: Limit) => {
  const minutes = limit.windowDurationMinutes,
    window =
      minutes > 0 && minutes % 1440 === 0
        ? `${minutes / 1440} 天窗口`
        : minutes > 0 && minutes % 60 === 0
          ? `${minutes / 60} 小时窗口`
          : minutes > 0
            ? `${minutes} 分钟窗口`
            : "限额窗口";
  return limit.limitName ? `${limit.limitName} · ${window}` : window;
};
const maskEmailPart = (part: string) => {
  if (part.length <= 1) return "*";
  if (part.length === 2) return part[0] + "*";
  return part[0] + "*".repeat(part.length - 2) + part.at(-1);
};
const maskEmail = (email: string) => {
  const at = email.lastIndexOf("@");
  if (at < 1 || at === email.length - 1) return maskEmailPart(email);
  const local = email.slice(0, at);
  const domain = email.slice(at + 1).split(".");
  return `${maskEmailPart(local)}@${domain
    .map((part, index) =>
      index === domain.length - 1 ? part : maskEmailPart(part),
    )
    .join(".")}`;
};
const planLabel = (plan?: string | null) => {
  if (!plan) return "未识别套餐";
  const labels: Record<string, string> = {
    free: "Free",
    go: "Go",
    plus: "Plus",
    pro: "Pro",
    prolite: "Pro Lite",
    team: "Team",
    business: "Business",
    self_serve_business_prolite: "Business Pro Lite",
    self_serve_business_usage_based: "Business（按量）",
    enterprise: "Enterprise",
    edu: "Edu",
  };
  return labels[plan.toLowerCase()] || plan;
};
const validationLabel = (account: Account) => {
  if (account.expectedKind === "any") return "未启用套餐类型校验";
  if (account.validationStatus === "pending")
    return `等待验证${account.expectedKind === "team" ? " Team / Business" : "个人订阅"}`;
  if (account.validationStatus === "matched")
    return account.actualKind === "team"
      ? "已连接 Team / Business 工作区"
      : "已连接个人订阅";
  if (account.validationStatus === "mismatch")
    return `类型不匹配：实际为 ${planLabel(account.planType)}，请退出后重新授权`;
  return `无法验证工作区类型：${planLabel(account.planType)}`;
};
const relative = (ts: number) => {
  const s = Math.max(0, ts - Math.floor(Date.now() / 1000));
  if (!s) return "即将重置";
  const d = Math.floor(s / 86400),
    h = Math.floor((s % 86400) / 3600),
    m = Math.floor((s % 3600) / 60);
  return d ? `${d} 天 ${h} 小时后` : h ? `${h} 小时 ${m} 分后` : `${m} 分钟后`;
};
const root = document.getElementById("root");
if (!root) throw new Error("缺少 #root 挂载节点");
createRoot(root).render(
  <React.StrictMode>
    <AuthProvider>
      <ThemeProvider>
        <App />
      </ThemeProvider>
    </AuthProvider>
  </React.StrictMode>,
);
