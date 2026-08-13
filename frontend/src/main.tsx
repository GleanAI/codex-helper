import React, { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  BrowserRouter,
  Navigate,
  Route,
  Routes,
  useNavigate,
} from "react-router-dom";
import {
  Activity,
  Bell,
  Check,
  LogOut,
  Moon,
  RefreshCw,
  Settings,
  Sun,
  Trash2,
  Zap,
} from "lucide-react";
import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { api, del, post, put } from "./api";
import "./styles.css";

type Status = { initialized: boolean; appServer: boolean; version: string };
type Limit = {
  limitId: string;
  limitName?: string;
  windowType: string;
  usedPercent: number;
  windowDurationMinutes: number;
  resetsAt: number;
};
type Point = { date: string; totalTokens: number };
type Account = {
  id: number;
  displayName: string;
  email?: string;
  planType?: string;
  connected: boolean;
};
type Dash = {
  accountId: number;
  displayName: string;
  account: { email?: string; planType?: string; connected: boolean };
  limits: Limit[];
  summary: {
    lifetimeTokens?: number;
    peakDailyTokens?: number;
    currentStreakDays?: number;
    longestRunningTurnSec?: number;
  };
  usage: Point[];
  fetchedAt: number;
  stale: boolean;
  lastError?: string;
};
function App() {
  const [s, setS] = useState<Status | null>(null);
  const [auth, setAuth] = useState<boolean | null>(null);
  useEffect(() => {
    api<Status>("system/status").then((x) => {
      setS(x);
      if (x.initialized)
        api("auth/me")
          .then(() => setAuth(true))
          .catch(() => setAuth(false));
      else setAuth(false);
    });
  }, []);
  if (!s || auth === null) return <Splash />;
  if (!s.initialized)
    return (
      <Setup
        onDone={() => {
          setS({ ...s, initialized: true });
          setAuth(true);
        }}
      />
    );
  return (
    <BrowserRouter>
      <Routes>
        {auth ? (
          <Route path="*" element={<Shell logout={() => setAuth(false)} />} />
        ) : (
          <Route path="*" element={<Login done={() => setAuth(true)} />} />
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
function Setup({ onDone }: { onDone: () => void }) {
  const [form, set] = useState({
    username: "admin",
    password: "",
    timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
  });
  const [err, setErr] = useState("");
  async function submit(e: React.FormEvent) {
    e.preventDefault();
    try {
      await post("setup", form);
      onDone();
    } catch (x) {
      setErr((x as Error).message);
    }
  }
  return (
    <main className="setup">
      <section className="hero">
        <div className="logo">
          <Zap /> Codex Helper
        </div>
        <h1>
          连接你的 Codex
          <br />
          <span>看见每一次增长</span>
        </h1>
        <p>一个容器内完成用量洞察、重置提醒与账户管理。</p>
      </section>
      <form className="panel form" onSubmit={submit}>
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
function Login({ done }: { done: () => void }) {
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
            await post("auth/login", { username: u, password: p });
            done();
          } catch (q) {
            setE((q as Error).message);
          }
        }}
      >
        <div className="logo">
          <Zap /> Codex Helper
        </div>
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
function Shell({ logout }: { logout: () => void }) {
  const nav = useNavigate();
  const [theme, setTheme] = useState(localStorage.theme || "system");
  useEffect(() => {
    document.documentElement.dataset.theme =
      theme === "system"
        ? matchMedia("(prefers-color-scheme:dark)").matches
          ? "dark"
          : "light"
        : theme;
    localStorage.theme = theme;
  }, [theme]);
  return (
    <div className="app">
      <aside>
        <div className="logo">
          <Zap /> Codex Helper
        </div>
        <nav>
          <button onClick={() => nav("/")}>
            <Activity />
            总览
          </button>
          <button onClick={() => nav("/settings")}>
            <Settings />
            设置中心
          </button>
        </nav>
        <div className="aside-bottom">
          <button onClick={() => setTheme(theme === "dark" ? "light" : "dark")}>
            {theme === "dark" ? <Sun /> : <Moon />}切换主题
          </button>
          <button
            onClick={async () => {
              if (!confirm("确定要退出 Codex Helper 管理后台吗？")) return;
              await post("auth/logout");
              logout();
            }}
          >
            <LogOut />
            退出
          </button>
        </div>
      </aside>
      <section className="content">
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/settings" element={<SettingsPage />} />
          <Route path="*" element={<Navigate to="/" />} />
        </Routes>
      </section>
    </div>
  );
}
function Dashboard() {
  const [accounts, setAccounts] = useState<Account[]>([]),
    [id, setId] = useState(+(localStorage.accountId || 0)),
    [d, setD] = useState<Dash | null>(null),
    [e, setE] = useState(""),
    [refresh, setRefresh] = useState<"idle" | "loading" | "done">("idle");
  const loadAccounts = () =>
    api<Account[]>("accounts").then((xs) => {
      setAccounts(xs);
      const next = xs.some((x) => x.id === id) ? id : xs[0]?.id || 0;
      if (next !== id) setId(next);
    });
  const load = async (accountId = id) => {
    if (!accountId) return;
    try {
      setD(await api<Dash>("dashboard?accountId=" + accountId));
      setE("");
    } catch (x) {
      setE((x as Error).message);
    }
  };
  useEffect(() => {
    loadAccounts();
  }, []);
  useEffect(() => {
    if (!id) return;
    localStorage.accountId = String(id);
    setD(null);
    load(id);
    const t = setInterval(() => load(id), 30000);
    return () => clearInterval(t);
  }, [id]);
  const sync = async () => {
    setRefresh("loading");
    setE("");
    try {
      await post(`accounts/${id}/sync`);
      await load(id);
      setRefresh("done");
      setTimeout(() => setRefresh("idle"), 2000);
    } catch (x) {
      setE((x as Error).message);
      setRefresh("idle");
    }
  };
  if (!accounts.length)
    return (
      <>
        <Header title="用量总览" sub="请先在设置中心添加账号" />
        <div className="panel empty">尚未添加 Codex 账号</div>
      </>
    );
  if (!d) return <Splash />;
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
                {x.email ? " · " + x.email : ""}
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
      <div className="account panel">
        <span className={"dot " + (d.account.connected ? "ok" : "")} />
        <div>
          <small>Codex Account</small>
          <b>
            {d.displayName} · {d.account.email || "尚未连接"}
          </b>
        </div>
        <span className="badge">{d.account.planType || "未识别套餐"}</span>
        <span className="fresh">
          更新于{" "}
          {d.fetchedAt
            ? new Date(d.fetchedAt * 1000).toLocaleString()
            : "尚未同步"}
        </span>
      </div>
      <div className="limits">
        {d.limits.map((x, i) => (
          <LimitCard key={i} x={x} />
        ))}
      </div>
      {!d.limits.length && (
        <div className="panel empty">
          该连接暂无限额数据，请确认登录后刷新。
        </div>
      )}
      <div className="stats">
        <Stat label="累计 Tokens" v={num(d.summary.lifetimeTokens)} />
        <Stat label="单日峰值" v={num(d.summary.peakDailyTokens)} />
        <Stat
          label="连续使用"
          v={
            d.summary.currentStreakDays == null
              ? "暂无"
              : d.summary.currentStreakDays + " 天"
          }
        />
        <Stat
          label="最长任务时长"
          v={duration(d.summary.longestRunningTurnSec)}
        />
      </div>
      <Chart data={d.usage} />
    </>
  );
}
function LimitCard({ x }: { x: Limit }) {
  const used = Math.min(100, Math.max(0, x.usedPercent)),
    left = 100 - used,
    usedLabel = Math.round(used),
    leftLabel = Math.round(left),
    leftColor = `hsl(${left * 1.2} 70% 45%)`;
  return (
    <div className="panel limit">
      <small>余额</small>
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
        aria-label={`${x.windowType}：已使用 ${usedLabel}%，剩余 ${leftLabel}%`}
      >
        <em style={{ width: left + "%", background: leftColor }} />
        <i style={{ width: used + "%" }} />
      </div>
      <footer>
        <span>已使用 {usedLabel}%</span>
        <span style={{ color: leftColor }}>剩余 {leftLabel}%</span>
      </footer>
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
const Chart = ({ data }: { data: Point[] }) => (
  <div className="panel chart">
    <div>
      <h3>每日 Token 趋势</h3>
    </div>
    <ResponsiveContainer width="100%" height={280}>
      <AreaChart data={data}>
        <defs>
          <linearGradient id="a" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0" stopColor="#5ee7f7" stopOpacity={0.35} />
            <stop offset="1" stopColor="#5ee7f7" stopOpacity={0} />
          </linearGradient>
        </defs>
        <CartesianGrid stroke="var(--grid)" vertical={false} />
        <XAxis dataKey="date" stroke="var(--muted)" />
        <YAxis
          width={55}
          tickCount={5}
          tickFormatter={compactAxis}
          stroke="var(--muted)"
        />
        <Tooltip
          formatter={(v) => [
            new Intl.NumberFormat("zh-CN").format(Number(v)),
            "Tokens",
          ]}
          contentStyle={{
            background: "var(--panel)",
            border: "1px solid var(--border)",
            borderRadius: 12,
          }}
        />
        <Area
          type="monotone"
          dataKey="totalTokens"
          stroke="#4dd8ed"
          fill="url(#a)"
          strokeWidth={2}
        />
      </AreaChart>
    </ResponsiveContainer>
  </div>
);
type SettingsTab = "general" | "codex" | "telegram" | "smtp";

const settingsTabs: Array<{
  id: SettingsTab;
  label: string;
  icon: React.ReactNode;
  content: React.ReactNode;
}> = [
  { id: "general", label: "通用", icon: <Settings />, content: <General /> },
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
      nextIndex = (currentIndex - 1 + settingsTabs.length) % settingsTabs.length;
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
function General() {
  const [v, setV] = useState<any>(null),
    [msg, setMsg] = useState("");
  useEffect(() => {
    api("settings/general").then(setV);
  }, []);
  if (!v) return <SettingsLoading />;
  return (
    <form
      className="panel form wide"
      onSubmit={async (e) => {
        e.preventDefault();
        try {
          await api("settings/general", {
            method: "PUT",
            body: JSON.stringify(v),
          });
          setMsg("设置已保存");
        } catch (x) {
          setMsg((x as Error).message);
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
            onChange={(e) => setV({ ...v, theme: e.target.value })}
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
    [result, setResult] = useState<any>(null),
    [active, setActive] = useState(0),
    [busy, setBusy] = useState(false),
    [err, setErr] = useState("");
  const load = () => api<Account[]>("accounts").then(setXs);
  useEffect(() => {
    load();
  }, []);
  const login = async (id: number) => {
    try {
      setBusy(true);
      setActive(id);
      setErr("");
      setResult(await post(`accounts/${id}/login/device`));
    } catch (q) {
      setErr((q as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const add = async () => {
    try {
      setBusy(true);
      setActive(0);
      setErr("");
      const x = await post<Account>("accounts", {
        displayName: `账号 ${xs.length + 1}`,
      });
      await load();
      setActive(x.id);
      setResult(await post(`accounts/${x.id}/login/device`));
    } catch (q) {
      setErr((q as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="panel form wide">
      <div className="settings-title">
        <div>
          <h2>Codex 账户与工作区</h2>
          <p>
            个人订阅和 Team 工作区请分别添加为独立连接；同一邮箱可以添加多次。
          </p>
        </div>
        <button disabled={busy} onClick={add}>
          {busy && active === 0 ? "服务启动中…" : "添加账号"}
        </button>
      </div>
      <div className="account-list">
        {xs.map((x) => (
          <div className="account-row" key={x.id}>
            <span className={"dot " + (x.connected ? "ok" : "")} />
            <div>
              <input
                aria-label="连接名称"
                defaultValue={x.displayName}
                onBlur={async (e) => {
                  const name = e.target.value.trim();
                  if (name && name !== x.displayName) {
                    await put(`accounts/${x.id}`, { displayName: name });
                    load();
                  }
                }}
              />
              <small>
                {x.email || "尚未登录"} · {x.planType || "未识别套餐"}
              </small>
            </div>
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
                if (!confirm(`确定要退出“${x.displayName}”的 Codex 账号吗？`))
                  return;
                await post(`accounts/${x.id}/logout`);
                load();
              }}
            >
              退出
            </button>
            <button
              className="icon danger"
              disabled={busy}
              title="删除连接"
              onClick={async () => {
                if (
                  confirm(
                    `删除“${x.displayName}”及其全部凭据和历史数据？此操作无法撤销。`,
                  )
                ) {
                  await del(`accounts/${x.id}`);
                  if (+localStorage.accountId === x.id)
                    localStorage.removeItem("accountId");
                  load();
                }
              }}
            >
              <Trash2 />
            </button>
          </div>
        ))}
      </div>
      {result && (
        <div className="codebox">
          <span>
            为“{xs.find((x) => x.id === active)?.displayName}”访问{" "}
            <a href={result.verificationUrl} target="_blank" rel="noreferrer">
              {result.verificationUrl}
            </a>
          </span>
          <strong>{result.userCode}</strong>
        </div>
      )}
      {err && <p className="error">{err}</p>}
    </div>
  );
}
function Telegram() {
  const [v, setV] = useState<any>(null),
    [msg, setMsg] = useState(""),
    [code, setCode] = useState("");
  useEffect(() => {
    api("settings/telegram").then(setV);
  }, []);
  if (!v) return <SettingsLoading />;
  return (
    <form
      className="panel form wide"
      onSubmit={async (e) => {
        e.preventDefault();
        try {
          const x: any = await api("settings/telegram", {
            method: "PUT",
            body: JSON.stringify(v),
          });
          setV(x);
          setMsg("Bot 已验证并保存");
        } catch (x) {
          setMsg((x as Error).message);
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
      <div className="checks">
        <label>
          <input
            type="checkbox"
            checked={v.enabled}
            onChange={(e) => setV({ ...v, enabled: e.target.checked })}
          />
          启用提醒
        </label>
        <label>
          <input
            type="checkbox"
            checked={v.menuEnabled}
            onChange={(e) => setV({ ...v, menuEnabled: e.target.checked })}
          />
          启用查询菜单
        </label>
      </div>
      <button>验证并保存</button>
      <div className="actions">
        <button
          type="button"
          className="secondary"
          onClick={async () => {
            const x: any = await post("settings/telegram/bind");
            setCode(x.code);
          }}
        >
          生成绑定码
        </button>
        <button
          type="button"
          className="secondary"
          onClick={async () => {
            try {
              await post("settings/telegram/test");
              setMsg("测试消息已发送");
            } catch (x) {
              setMsg((x as Error).message);
            }
          }}
        >
          发送测试
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
  const [v, setV] = useState<any>(null),
    [msg, setMsg] = useState("");
  useEffect(() => {
    api("settings/smtp").then(setV);
  }, []);
  if (!v) return <SettingsLoading />;
  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      setV(
        await api("settings/smtp", { method: "PUT", body: JSON.stringify(v) }),
      );
      setMsg("SMTP 设置已保存");
    } catch (x) {
      setMsg((x as Error).message);
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
            onChange={(e) => setV({ ...v, security: e.target.value })}
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
              await post("settings/smtp/test");
              setMsg("测试邮件已发送");
            } catch (x) {
              setMsg((x as Error).message);
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
const num = (v?: number) =>
  v == null
    ? "暂无"
    : new Intl.NumberFormat("zh-CN", {
        notation: "compact",
        maximumFractionDigits: 1,
      }).format(v);
const compactAxis = (v: number) =>
  new Intl.NumberFormat("en", {
    notation: "compact",
    maximumFractionDigits: 1,
  }).format(v);
const duration = (v?: number) =>
  v == null
    ? "暂无"
    : v < 60
      ? `${v} 秒`
      : v < 3600
        ? `${Math.floor(v / 60)} 分 ${v % 60} 秒`
        : `${(v / 3600).toFixed(1)} 小时`;
const relative = (ts: number) => {
  const s = Math.max(0, ts - Math.floor(Date.now() / 1000));
  if (!s) return "即将重置";
  const d = Math.floor(s / 86400),
    h = Math.floor((s % 86400) / 3600),
    m = Math.floor((s % 3600) / 60);
  return d ? `${d} 天 ${h} 小时后` : h ? `${h} 小时 ${m} 分后` : `${m} 分钟后`;
};
createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
