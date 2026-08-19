import { Github, LogIn, Zap } from "lucide-react";
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { getEventually, toErrorMessage } from "./api";
import { decodePublicOverview, type PublicOverview } from "./types";
import {
  kindLabel,
  limitLabel,
  planLabel,
  UsageCard,
  UsageConnection,
} from "./usage-card";

const repositoryURL = "https://github.com/GleanAI/codex-helper";

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
          <Link className="public-login-button" to="/login">
            <LogIn />
            登录
          </Link>
        </nav>
      </header>

      <main className="public-main">
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
              <UsageCard
                className="public-usage-card"
                title={card.title}
                emailIdentified={card.emailIdentified}
                connectionCount={card.connections.length}
                key={`${card.title}:${cardIndex}`}
              >
                {card.connections.map((connection, connectionIndex) => (
                  <PublicConnection
                    connection={connection}
                    key={`${connection.displayName}:${connectionIndex}`}
                  />
                ))}
              </UsageCard>
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
  const limits = connection.limits.map((limit) => ({
    label: limitLabel(limit.limitName, limit.windowDurationMinutes),
    usedPercent: limit.usedPercent,
    resetsAt: limit.resetsAt,
  }));
  if (connection.monthlyCreditLimit)
    limits.push({
      label: "月度额度",
      usedPercent: 100 - connection.monthlyCreditLimit.remainingPercent,
      resetsAt: connection.monthlyCreditLimit.resetsAt,
    });
  return (
    <UsageConnection
      className="public-connection"
      displayName={connection.displayName}
      kind={kindLabel(connection.kind)}
      plan={planLabel(connection.planType)}
      status={connection.status}
      fetchedAt={connection.fetchedAt}
      limits={limits}
      emptyMessage={
        connection.status === "offline" ? "登录后显示限额窗口" : "暂无限额数据"
      }
    />
  );
}

function publicPageAvailable() {
  return document.visibilityState !== "hidden" && navigator.onLine;
}
