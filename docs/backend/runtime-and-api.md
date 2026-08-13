# 后端运行与 API

## 启动与关闭

入口为 `backend/cmd/server/main.go`。普通启动调用 `app.New`：打开 `/data/codex-helper.db` 并执行兼容迁移、打开或创建 `/data/secret.key`、为数据库中的每个账号创建运行时对象，再组装 HTTP server。`Run` 启动 app-server 保活、每分钟调度器和 Telegram long polling；SIGINT/SIGTERM 触发五秒 HTTP 优雅关闭、停止所有账号进程并关闭数据库。

`codex-helper healthcheck` 将 `${LISTEN_ADDR:-:8080}` 的通配地址转换为 `127.0.0.1` 并请求 `/health/live`，供 Docker `HEALTHCHECK` 使用。

## 请求链路

- `internal/app/app.go`：应用生命周期、账号运行时、路由、静态前端和通用 HTTP 辅助函数。
- `internal/app/api.go`：`/api/v1/` 分派、初始化、会话、账号和用量同步。
- `internal/app/notify.go`：SMTP、Telegram、提醒生成和发送。
- `internal/store/store.go`：SQLite schema、兼容迁移和数据方法。
- `internal/codex/client.go`：与 `codex app-server` 的 JSONL 请求/响应关联。

所有路由由 `http.ServeMux` 承载。API 先处理三个匿名入口，再统一调用 `require`；前端资源从 Go `embed.FS` 提供，未知浏览器路径回退到 `index.html`。完整端点以 [`backend/CONTRACT.md`](../../backend/CONTRACT.md) 为准。

## 后台任务

- `keepCodex` 每秒检查未就绪的账号，串行完成进程启动与协议初始化，成功后立即同步。
- `scheduler` 每分钟按 `syncMinutes` 的 Unix 时间取模触发全账号同步，清理过期历史，并异步处理提醒。
- `telegramLoop` 使用 Bot API long polling；仅已配置 Token 才请求更新。
- app-server 的登录、账号和限额通知会触发带短退避的同步；多次失败将内存 Dashboard 标为 stale 并记录 `lastError`。

这些 goroutine 都以应用 context 为退出边界。修改调度时必须避免无限阻塞、重复启动、停止后重启和同一账号并发同步。
