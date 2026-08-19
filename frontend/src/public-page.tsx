import { Github, LogIn, Zap } from "lucide-react";
import { useEffect, useMemo, useState, type CSSProperties } from "react";
import { Link } from "react-router-dom";
import { getEventually, toErrorMessage } from "./api";
import {
  decodePublicOverview,
  type PublicConnectionStatus,
  type PublicOverview,
} from "./types";

const repositoryURL = "https://github.com/GleanAI/codex-helper";

const statusDetails: Record<
  PublicConnectionStatus,
  { label: string; description: string }
> = {
  offline: { label: "未连接", description: "等待账号完成授权" },
  failed: { label: "读取失败", description: "暂时无法更新用量" },
  loading: { label: "正在读取", description: "正在获取最新额度" },
  pending: { label: "套餐待识别", description: "用量数据仍可继续查看" },
  stale: { label: "数据可能已过期", description: "显示最近一次可用快照" },
  healthy: { label: "运行正常", description: "用量数据已同步" },
};

export default function PublicPage({ version }: { version: string }) {
  const [overview, setOverview] = useState<PublicOverview | null>(null);
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);

  useEffect(() => {
    let stopped = false;
    let running = false;
    let refreshPending = false;
    let controller: AbortController | null = null;
    let timer = 0;
    const poll = async () => {
      if (stopped || running || !publicPageAvailable()) return;
      running = true;
      refreshPending = false;
      controller = new AbortController();
      const requestController = controller;
      try {
        const next = await getEventually(
          "public/overview",
          decodePublicOverview,
          requestController.signal,
        );
        if (!stopped) {
          setOverview(next);
          setError("");
        }
      } catch (requestError) {
        if (!requestController.signal.aborted)
          setError(toErrorMessage(requestError));
      }
      running = false;
      if (controller === requestController) controller = null;
      if (stopped || !publicPageAvailable()) return;
      if (refreshPending) void poll();
      else timer = window.setTimeout(poll, 30_000);
    };
    const availabilityChanged = () => {
      window.clearTimeout(timer);
      if (!publicPageAvailable()) {
        refreshPending = false;
        controller?.abort();
        return;
      }
      if (running) {
        refreshPending = true;
        controller?.abort();
      } else void poll();
    };
    window.addEventListener("online", availabilityChanged);
    window.addEventListener("offline", availabilityChanged);
    document.addEventListener("visibilitychange", availabilityChanged);
    void poll();
    return () => {
      stopped = true;
      controller?.abort();
      window.clearTimeout(timer);
      window.removeEventListener("online", availabilityChanged);
      window.removeEventListener("offline", availabilityChanged);
      document.removeEventListener("visibilitychange", availabilityChanged);
    };
  }, [retry]);

  const latestUpdate = useMemo(() => {
    if (!overview) return 0;
    return Math.max(
      0,
      ...overview.cards.flatMap((card) =>
        card.connections.map((connection) => connection.fetchedAt),
      ),
    );
  }, [overview]);

  return (
    <div className="public-page">
      <header className="public-header">
        <div className="public-brand" aria-label="Codex Helper">
          <span className="public-brand-mark">
            <Zap />
          </span>
          <span>Codex Helper</span>
          <small>v{version}</small>
        </div>
        <nav className="public-actions" aria-label="公开页面操作">
          <a
            className="public-icon-button"
            href={repositoryURL}
            target="_blank"
            rel="noopener noreferrer"
            aria-label="在 GitHub 上查看 Codex Helper"
            title="GitHub"
          >
            <Github />
          </a>
          <Link className="public-login-button" to="/">
            <LogIn />
            登录
          </Link>
        </nav>
      </header>

      <main className="public-main">
        <section className="public-hero">
          <div>
            <span className="public-eyebrow">
              <i aria-hidden="true" /> PUBLIC USAGE STATUS
            </span>
            <h1>Codex 用量状态</h1>
            <p>无需登录，快速查看各个 Codex 连接的额度与重置时间。</p>
          </div>
          <div className="public-freshness" aria-live="polite">
            <span>最近更新</span>
            <strong>
              {latestUpdate
                ? new Date(latestUpdate * 1000).toLocaleString()
                : "等待首次同步"}
            </strong>
            <small>页面每 30 秒自动刷新</small>
          </div>
        </section>

        {error && overview && <div className="public-banner">{error}</div>}
        {!overview ? (
          error ? (
            <section className="public-state-card">
              <h2>暂时无法读取公开用量</h2>
              <p>{error}</p>
              <button onClick={() => setRetry((value) => value + 1)}>
                重新加载
              </button>
            </section>
          ) : (
            <section className="public-loading" aria-label="正在加载公开用量">
              <div className="spinner" />
              <p>正在读取最新用量…</p>
            </section>
          )
        ) : overview.cards.length === 0 ? (
          <section className="public-state-card">
            <h2>尚无可公开的连接</h2>
            <p>管理员添加并连接 Codex 账号后，用量卡片会显示在这里。</p>
          </section>
        ) : (
          <div className="public-card-grid">
            {overview.cards.map((card, cardIndex) => (
              <section
                className="public-usage-card"
                key={`${card.title}:${cardIndex}`}
              >
                <header className="public-card-header">
                  <div>
                    <span>{card.emailIdentified ? "账号" : "连接"}</span>
                    <h2>{card.title}</h2>
                  </div>
                  <small>{card.connections.length} 个连接</small>
                </header>
                <div className="public-connections">
                  {card.connections.map((connection, connectionIndex) => (
                    <PublicConnection
                      connection={connection}
                      key={`${connection.displayName}:${connectionIndex}`}
                    />
                  ))}
                </div>
              </section>
            ))}
          </div>
        )}
      </main>

      <footer className="public-footer">
        本站点由{" "}
        <a href={repositoryURL} target="_blank" rel="noopener noreferrer">
          Codex Helper
        </a>{" "}
        驱动
      </footer>
    </div>
  );
}

function PublicConnection({
  connection,
}: {
  connection: PublicOverview["cards"][number]["connections"][number];
}) {
  const status = statusDetails[connection.status];
  const hasLimits =
    connection.limits.length > 0 || connection.monthlyCreditLimit !== null;
  return (
    <article className="public-connection">
      <div className="public-connection-heading">
        <div>
          <h3>{connection.displayName}</h3>
          <span>{kindLabel(connection.kind)}</span>
        </div>
        <span className="public-plan">{planLabel(connection.planType)}</span>
      </div>
      <div className={`public-status public-status-${connection.status}`}>
        <i aria-hidden="true" />
        <strong>{status.label}</strong>
        <span>{status.description}</span>
        <time>
          {connection.fetchedAt
            ? new Date(connection.fetchedAt * 1000).toLocaleString()
            : "尚未同步"}
        </time>
      </div>
      {hasLimits ? (
        <div className="public-limits">
          {connection.limits.map((limit, index) => (
            <PublicLimit
              key={`${limit.limitName ?? "limit"}:${limit.windowDurationMinutes}:${index}`}
              label={limitLabel(limit.limitName, limit.windowDurationMinutes)}
              usedPercent={limit.usedPercent}
              resetsAt={limit.resetsAt}
            />
          ))}
          {connection.monthlyCreditLimit && (
            <PublicLimit
              label="月度额度"
              usedPercent={100 - connection.monthlyCreditLimit.remainingPercent}
              resetsAt={connection.monthlyCreditLimit.resetsAt}
            />
          )}
        </div>
      ) : (
        <div className="public-no-limits">
          {connection.status === "offline"
            ? "登录后显示限额窗口"
            : "暂无限额数据"}
        </div>
      )}
    </article>
  );
}

function PublicLimit({
  label,
  usedPercent,
  resetsAt,
}: {
  label: string;
  usedPercent: number;
  resetsAt: number;
}) {
  const used = Math.min(100, Math.max(0, usedPercent));
  const left = 100 - used;
  const leftLabel = Math.round(left);
  const gaugeColor = `hsl(${left * 1.2} 70% 45%)`;
  const gaugeStyle = {
    "--gauge-value": `${left}%`,
    "--gauge-color": gaugeColor,
  } as CSSProperties;
  return (
    <div className="public-limit">
      <div
        className="public-limit-gauge"
        style={gaugeStyle}
        role="img"
        aria-label={`${label}：剩余 ${leftLabel}%`}
      >
        <div>
          <strong>{leftLabel}</strong>
          <span>%</span>
        </div>
      </div>
      <div className="public-limit-details">
        <h4>{label}</h4>
        <span>剩余额度</span>
        <div className="public-limit-reset">
          <small>下次重置</small>
          <strong>
            {resetsAt ? new Date(resetsAt * 1000).toLocaleString() : "暂未提供"}
          </strong>
          {resetsAt > 0 && <span>{relativeTime(resetsAt)}</span>}
        </div>
      </div>
    </div>
  );
}

function publicPageAvailable() {
  return document.visibilityState !== "hidden" && navigator.onLine;
}

function kindLabel(kind: "personal" | "team" | "unknown") {
  if (kind === "personal") return "个人订阅";
  if (kind === "team") return "Team / Business 工作区";
  return "待识别连接";
}

function planLabel(plan: string | null) {
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
}

function limitLabel(name: string | null, minutes: number) {
  const window =
    minutes > 0 && minutes % 1440 === 0
      ? `${minutes / 1440} 天窗口`
      : minutes > 0 && minutes % 60 === 0
        ? `${minutes / 60} 小时窗口`
        : minutes > 0
          ? `${minutes} 分钟窗口`
          : "限额窗口";
  return name ? `${name} · ${window}` : window;
}

function relativeTime(timestamp: number) {
  const seconds = timestamp - Math.floor(Date.now() / 1000);
  if (seconds <= 0) return "即将更新";
  if (seconds < 60) return `${seconds} 秒后`;
  if (seconds < 3600) return `${Math.ceil(seconds / 60)} 分钟后`;
  if (seconds < 86_400) return `${Math.ceil(seconds / 3600)} 小时后`;
  return `${Math.ceil(seconds / 86_400)} 天后`;
}
