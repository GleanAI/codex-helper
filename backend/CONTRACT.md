# Codex Helper 后端 API 契约

本文件记录当前 HTTP 兼容边界。路径、方法、鉴权、状态码、响应字段和用户可见错误文案发生变化时，必须同步修改本文件。代码和测试是运行行为的最终事实来源。

## 1. 全局行为

- 默认监听 `${LISTEN_ADDR:-:8080}`，API 前缀为 `/api/v1/`。
- `GET /health/live` 始终返回 `200 {"status":"ok"}`；`GET /health/ready` 在 SQLite 可用时返回 `200 {"status":"ok","appServer":bool}`，数据库不可用时返回 `503 {"error":string}`。`appServer` 表示至少一个账号的 app-server 已完成初始化。
- JSON 请求体最多读取 1 MiB，拒绝未知字段；业务错误统一为 `{"error":string}`。未匹配 API 返回 `404 {"error":"接口不存在"}`。
- 所有响应带 `X-Content-Type-Options: nosniff`、`X-Frame-Options: DENY`、`Referrer-Policy: same-origin` 和同源 CSP。
- `GET /api/v1/system/status`、`POST /api/v1/setup`、`POST /api/v1/auth/login` 匿名可用。status 的其他方法返回 405；其余 API 要求有效 `session` cookie，非 `GET`/`HEAD` 请求还要求 `X-Requested-With: codex-helper`，否则分别返回 401 或 403。当前 dispatcher 只对部分路由显式限制 HTTP method；下文使用“任意方法”或“非 `GET`”的地方是对实际兼容行为的记录。
- session 有效期七天，cookie 为 `HttpOnly`、`SameSite=Strict`、`Path=/`；数据库只保存 token 摘要。登录失败按 `RemoteAddr` 在进程内限制为 15 分钟最多 10 次，超限返回 429。
- 未命中静态文件的非 API GET 路径返回嵌入的 `index.html`，供前端路由回退。

## 2. 公共对象

### Account

```text
{id, displayName, email:string|null, planType:string|null,
 expectedKind:"any"|"personal"|"team",
 actualKind:"unknown"|"personal"|"team",
 validationStatus:"pending"|"matched"|"mismatch"|"unknown",
 possibleDuplicate:bool, connected:bool, createdAt, updatedAt}
```

### Dashboard

```text
{accountId, displayName,
 account:{email:string|null, authMode:string|null, planType:string|null, connected:bool},
 limits:[{limitId, limitName:string|null, windowType,
          usedPercent, windowDurationMinutes, resetsAt, planType:string|null}],
 summary:{lifetimeTokens?, peakDailyTokens?, longestRunningTurnSec?,
          currentStreakDays?, longestStreakDays?, callCount?, inputTokens?, outputTokens?},
 usage:[{date,totalTokens,callCount?,inputTokens?,outputTokens?}],
 fetchedAt, stale, lastError?}
```

时间字段为 Unix 秒。app-server 未提供的摘要字段可以是 `null`；列表应返回数组而非 `null`。

## 3. 系统、初始化与会话

| 方法与路径 | 鉴权 | 行为 |
| --- | --- | --- |
| `GET /api/v1/system/status` | 匿名 | `200 {initialized,version,appServer}`。`version` 为镜像构建时注入的应用版本，未注入时默认为 `0.2.0`。 |
| `POST /api/v1/setup` | 匿名、仅未初始化 | body `{username,password,timezone}`；用户名至少 3 位、密码至少 10 位，否则 400；时区有效时写入，否则使用默认 UTC。事务创建唯一管理员和通用设置，设置 session，返回 `201 {ok:true}`；已初始化返回 409。 |
| `POST /api/v1/auth/login` | 匿名 | body `{username,password}`；未初始化返回 409，错误凭据返回 401，成功设置 session 并返回 `200 {ok:true}`，限流返回 429。 |
| `任意方法 /api/v1/auth/me` | session；非 `GET`/`HEAD` 还需来源头 | `200 {username}`。前端使用 `GET`。 |
| `POST /api/v1/auth/logout` | session + 来源头 | 删除当前 session、清除 cookie，返回 `200 {ok:true}`。 |

兼容文案包括：`系统已初始化`、`用户名至少3位，密码至少10位`、`请先初始化`、`用户名或密码错误`、`尝试次数过多，请稍后再试`、`未登录`、`请求来源校验失败`。

## 4. Codex 账号与用量

| 方法与路径 | 请求与响应 |
| --- | --- |
| `GET /api/v1/accounts` | 返回 `200 Account[]`，按 ID 升序。 |
| `POST /api/v1/accounts` | body `{displayName,expectedKind}`；空名称默认为 `新账号`，空类型默认为 `any`；成功返回 `201 Account`。 |
| `PUT /api/v1/accounts/{id}` | body `{displayName,expectedKind?}`；名称不能为空，省略类型时保留旧值；成功返回 `200 {ok:true}`。 |
| `DELETE /api/v1/accounts/{id}` | 停止该账号进程，删除账号及级联历史，再删除对应凭据目录；成功返回 `200 {ok:true}`。 |
| `POST /api/v1/accounts/{id}/login/device` | 启动并初始化 app-server，调用 `account/login/start` 的 `chatgptDeviceCode` 流程；返回含 `verificationUrl`、`userCode` 和 `loginId` 的结果。 |
| `POST /api/v1/accounts/{id}/logout` | 调用 `account/logout` 并将连接状态置为 false；返回 `200 {ok:true}`。 |
| `POST /api/v1/accounts/{id}/sync` | 同步指定账号；成功 `200 {ok:true}`，上游失败 502。 |
| `任意方法 /api/v1/dashboard?accountId={id}` | 返回内存中的 `Dashboard`；非 `GET`/`HEAD` 还需来源头。省略或无效的零值 ID 使用账号 1，前端使用 `GET`。 |
| `POST /api/v1/sync?accountId={id}` | 旧兼容入口，同步指定账号；省略或零值 ID 使用账号 1。 |

账号不存在返回 404 `账号不存在`；非法路径 ID 返回 400 `账号 ID 无效`；无效 `expectedKind` 返回 400 `连接类型无效`。未知账号套餐不得猜测为个人或团队。

## 5. 设置与通知

### 通用设置

`GET /api/v1/settings/general` 返回：

```text
{timezone,theme,syncMinutes,retentionDays,beforeMinutes,notifyBefore,notifyAfter}
```

任意非 `GET` 方法都按更新处理，前端使用 `PUT`；请求接受完整对象。`syncMinutes` 为 1–60，`retentionDays` 为 30–365，`beforeMinutes` 为 1–1440，时区必须能由 Go 加载；非法值返回 400。成功返回保存后的对象。

### SMTP

- `GET /api/v1/settings/smtp` 返回 `{host,port,username,from,fromName,to,security,enabled,configured}`；`password` 为空或省略，默认端口 587、默认 `security=starttls`。
- 任意非 `GET` 方法都按更新处理，前端使用 `PUT`；接受完整设置，`password` 留空时保留旧秘密。host、合法端口、from、to 必填；成功响应不返回密码。
- `POST /api/v1/settings/smtp/test` 使用已保存配置发送测试邮件；成功 `200 {ok:true}`，发送失败 502。
- 前端为 `security` 提供 `starttls`、`tls` 和 `none`；后端当前不对该字段做白名单校验，其他值会落入无 TLS 分支。

### Telegram

- `GET /api/v1/settings/telegram` 返回 `{chatId,enabled,menuEnabled,configured,botName?}`，不返回 Token 明文。
- 任意非 `GET` 方法都按更新处理，前端使用 `PUT`；接受 `{token,chatId,enabled,menuEnabled}`，Token 留空时保留旧值，保存前调用 Bot API `getMe` 验证。失败返回 400 或 502。
- `POST /api/v1/settings/telegram/bind` 返回六位 `{code}`，绑定码有效十分钟。
- `POST /api/v1/settings/telegram/test` 向已绑定会话发送测试消息；未绑定或发送失败返回 502。

## 6. 维护接口

| 方法与路径 | 行为 |
| --- | --- |
| `POST /api/v1/maintenance/cleanup` | 按当前保留天数删除旧限额快照、通知和每日用量，返回 `200 {deleted}`。 |
| `任意方法 /api/v1/maintenance/backup` | 使用 SQLite `VACUUM INTO` 生成包含已提交 WAL 数据的一致性快照，并以 `codex-helper.db` 下载；非 `GET`/`HEAD` 还需来源头，前端使用 `GET`。快照不含 `/data/secret.key` 或 Codex 凭据目录。 |

## 7. 外部协议边界

每个账号通过 JSONL stdio 与 `codex app-server` 通信。当前使用的方法为 `initialize`、`account/read`、`account/login/start`、`account/logout`、`account/rateLimits/read` 和 `account/usage/read`，并响应 `account/login/completed`、`account/updated`、`account/rateLimits/updated` 通知。官方协议说明见 [Codex App Server](https://learn.chatgpt.com/docs/app-server)；本项目以 Dockerfile 固定的 Codex CLI 版本、当前解析代码和测试作为兼容基线。
