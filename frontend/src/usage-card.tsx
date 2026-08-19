import type { CSSProperties, ReactNode } from "react";
import type { PublicConnectionStatus } from "./types";

const statusDetails: Record<
  PublicConnectionStatus,
  { label: string; description: string }
> = {
  offline: { label: "未连接", description: "等待账号完成授权" },
  failed: { label: "读取失败", description: "暂时无法更新用量" },
  loading: { label: "正在读取", description: "正在获取最新额度" },
  pending: { label: "套餐待识别", description: "用量数据仍可继续查看" },
  stale: { label: "数据可能已过期", description: "显示最近一次可用快照" },
  healthy: { label: "运行正常", description: "数据已同步" },
};

export function UsageCard({
  title,
  emailIdentified,
  connectionCount,
  children,
  className = "",
}: {
  title: string;
  emailIdentified: boolean;
  connectionCount: number;
  children: ReactNode;
  className?: string;
}) {
  return (
    <section className={`usage-card ${className}`.trim()}>
      <header className="usage-card-header">
        <div>
          <span>{emailIdentified ? "账号" : "连接"}</span>
          <h2>{title}</h2>
        </div>
        <small>{connectionCount} 个连接</small>
      </header>
      <div className="usage-connections">{children}</div>
    </section>
  );
}

export function UsageConnection({
  displayName,
  kind,
  plan,
  status,
  fetchedAt,
  limits,
  emptyMessage,
  error,
  action,
  className = "",
  stackMonthlyUnderSevenDay = false,
}: {
  displayName: string;
  kind: string;
  plan: string;
  status: PublicConnectionStatus;
  fetchedAt: number;
  limits: Array<{
    label: string;
    usedPercent: number;
    resetsAt: number;
    layout?: "seven-day" | "monthly";
  }>;
  emptyMessage: string;
  error?: string;
  action?: ReactNode;
  className?: string;
  stackMonthlyUnderSevenDay?: boolean;
}) {
  const details = statusDetails[status];
  const sevenDayIndex = limits.findIndex(
    (limit) => limit.layout === "seven-day",
  );
  const monthlyIndex = limits.findIndex((limit) => limit.layout === "monthly");
  const stackLimits =
    stackMonthlyUnderSevenDay && sevenDayIndex >= 0 && monthlyIndex >= 0;
  const stackWithRegularLimit =
    stackLimits && limits.some((limit) => limit.layout === undefined);
  const limitsClassName = [
    "usage-limits",
    stackLimits ? "usage-limits-seven-day-monthly" : "",
    stackWithRegularLimit ? "usage-limits-seven-day-monthly-paired" : "",
  ]
    .filter(Boolean)
    .join(" ");
  return (
    <article className={`usage-connection ${className}`.trim()}>
      <div className="usage-connection-heading">
        <div>
          <h3>{displayName}</h3>
          <span>{kind}</span>
        </div>
        <div className="usage-connection-actions">
          <span className="usage-plan">{plan}</span>
          {action}
        </div>
      </div>
      <div className={`usage-status usage-status-${status}`}>
        <i aria-hidden="true" />
        <strong>{details.label}</strong>
        <span>{details.description}</span>
        <time>
          {fetchedAt ? new Date(fetchedAt * 1000).toLocaleString() : "尚未同步"}
        </time>
      </div>
      {error && <div className="usage-warning">{error}</div>}
      {limits.length > 0 ? (
        <div className={limitsClassName}>
          {limits.map((limit, index) => (
            <UsageLimit
              key={`${limit.label}:${limit.resetsAt}:${index}`}
              {...limit}
            />
          ))}
        </div>
      ) : (
        <div className="usage-no-limits">{emptyMessage}</div>
      )}
    </article>
  );
}

function UsageLimit({
  label,
  usedPercent,
  resetsAt,
  layout,
}: {
  label: string;
  usedPercent: number;
  resetsAt: number;
  layout?: "seven-day" | "monthly";
}) {
  const used = Math.min(100, Math.max(0, usedPercent));
  const left = 100 - used;
  const leftLabel = Math.round(left);
  const gaugeStyle = {
    "--gauge-value": `${left}%`,
    "--gauge-color": `hsl(${left * 1.2} 70% 45%)`,
  } as CSSProperties;
  return (
    <div className={`usage-limit${layout ? ` usage-limit-${layout}` : ""}`}>
      <div
        className="usage-limit-gauge"
        style={gaugeStyle}
        role="img"
        aria-label={`${label}：剩余 ${leftLabel}%`}
      >
        <div>
          <strong>{leftLabel}</strong>
          <span>%</span>
        </div>
      </div>
      <div className="usage-limit-details">
        <h4>{label}</h4>
        <span>剩余额度</span>
        <div className="usage-limit-reset">
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

export function kindLabel(kind: "personal" | "team" | "unknown") {
  if (kind === "personal") return "个人订阅";
  if (kind === "team") return "Team / Business 工作区";
  return "待识别连接";
}

export function planLabel(plan: string | null | undefined) {
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

export function limitLabel(name: string | null, minutes: number) {
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
