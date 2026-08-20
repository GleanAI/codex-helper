# Codex 集成与用量同步

## 账号隔离与进程生命周期

每个 `accounts` 记录对应一个 `accountRuntime` 和独立 `codex app-server` 子进程。账号 1 为升级兼容固定使用 `/data/codex`；其他账号使用 `/data/accounts/<id>/codex`。该目录通过子进程 `CODEX_HOME` 注入，保存并刷新 ChatGPT 登录凭据。

`ensureReady` 用 lifecycle mutex 串行冷启动：创建目录、启动子进程、在 20 秒内调用 `initialize`，成功后才标记 ready。仅子进程存活不代表协议已初始化；失败进程必须关闭后重试。`syncing` mutex 串行同账号同步并保护 Dashboard，`stateMu` 保护 ready/stopped。删除账号先从应用 map 移除并停止进程，再删除数据库和精确凭据目录。

## JSONL 协议与登录

客户端以 stdio 启动 `codex app-server`，每行一个 JSON 消息。递增 ID 将响应关联到 buffered channel；请求受调用 context 或 20 秒 timeout 控制。旧进程晚退出时不能把替代进程标记为断开。

设备码登录调用：

```text
account/login/start {type:"chatgptDeviceCode"}
```

前端展示 `verificationUrl` 与 `userCode`。收到 `account/login/completed`、`account/updated` 或 `account/rateLimits/updated` 后异步重拉数据；登录完成最多以 0、1、3 秒退避等待套餐分类可用。官方流程和字段见 [Codex App Server 文档](https://learn.chatgpt.com/docs/app-server)。Dockerfile 固定的 CLI 版本及项目测试仍是实际兼容基线。

## 同步数据流

一次同步依次读取 `account/read`、`account/rateLimits/read` 和 `account/usage/read`：

- `account/read` 决定连接、邮箱、认证模式和套餐；读取失败会使整次同步失败。
- 限额读取兼容 `rateLimitsByLimitId` 多 bucket 和 `rateLimits` 单 bucket，再把 `primary`/`secondary` 位置展平。两者不对应固定周期；上游 nullable 的 `windowDurationMins` 标准化为 Dashboard 的 `windowDurationMinutes`，缺失时使用 `0`。可选的 `individualLimit` 作为独立 `monthlyCreditLimit` 返回，优先读取单 bucket 快照，缺失时回退到 multi-bucket 中的 canonical `codex` bucket；不把自然月额度伪装成固定时长窗口。读取失败时保留空限额和 `null` 月度额度而不令整次同步失败。
- 套餐未知时，可从所有可分类且一致的限额 bucket 回填；冲突或未知时必须保持 unknown。
- 用量读取更新 summary，并把合法的每日 Token bucket 按账号合并到 `daily_usage`；同日后续值覆盖旧值。Dashboard 从保留期内最早保存的官方日桶连续生成到配置时区的今天，缺失日期补为零。接口失败且登录邮箱未变化时保留上一份内存 summary，并从 SQLite 恢复历史；退出登录、身份未知或邮箱变化时清除该精确槽位可从官方源恢复的日用量缓存，不沿用旧 summary，且用量失败不令整次同步失败。
- 每次限额同步写入快照，结合已到期窗口的 `resets_at` 推进或提前发生的用量百分比显著回落确认重置，并更新账号元数据及内存 Dashboard。只有新快照确认后才生成重置后提醒；通知使用新周期的剩余比例和下一次重置时间。

`account/rateLimits/read` 的 `individualLimit`、`account/usage/read` 的 optional 指标和 daily buckets 都可能暂未提供。能力按响应字段检测，不根据 Codex 版本或套餐猜测。仅 API Key 或 Bedrock 登录不能保证读取 ChatGPT 用量；不得在缺失数据时合成调用次数、输入/输出 Token、价格或账单日期。

后台按 `syncMinutes`（默认 5 分钟）重复执行同一条用量读取链路，详情页每轮请求完成 30 秒后重读 Dashboard。同步间隔只影响发现上游变化的时间；`account/usage/read` 没有强制上游重新统计的参数，因此不得把补齐的零值解释为独立测得的实时用量。

## 套餐类型

用户期望类型仅为连接后的校验提示：`any`、`personal`、`team`。当前 personal 包括 free/go/plus/pro/prolite；team 包括 team/business 及两种 self-serve business 标识。未知新套餐必须显示 unknown，不能静默当作某一类型。相同邮箱和相同已知实际类型的多个账号标记 `possibleDuplicate`，但不会自动合并或删除。
