# 认证与安全

## 初始化与管理员

新数据库始终创建账号表中的默认 Codex 账号，但只有 `settings.initialized` 存在才视为完成安装。`POST /api/v1/setup` 在事务中创建唯一 `admin(id=1)`、通用设置和安装标记；用户名至少 3 位、密码至少 10 位。首次初始化没有额外安装令牌，因此初始化完成前不得把实例直接暴露到不可信网络。

管理员密码使用 argon2id（3 次、64 MiB、2 lanes、32 字节结果和随机 salt）保存。当前没有改密或找回接口；不要通过新增旁路直接写入明文或弱摘要。

## Session 与请求来源

登录成功生成 32 字节随机 token，客户端得到七天 `HttpOnly`、`SameSite=Strict` cookie，SQLite 只保存 SHA-256 摘要。每次受保护请求回查未过期 session。登出删除当前摘要并清 cookie。

所有非 `GET`/`HEAD` 受保护请求还必须携带 `X-Requested-With: codex-helper`。这是当前同源部署下的额外 CSRF 门禁，不替代 session 校验，也不意味着可以放宽 CSP 或 cookie 策略。`Secure` 当前为 false，以支持 README 中直接 HTTP 部署；公网必须由 HTTPS 反向代理保护，调整此兼容行为时同步评估代理终止 TLS 的方式。

登录失败限流仅存在于单进程内，以 `RemoteAddr` 为 key，每 15 分钟最多 10 次。修改反向代理或客户端 IP 处理时，不能未经可信代理白名单就相信任意转发头。

## 密钥与外部凭据

`security.OpenVault` 首次启动创建权限 `0600` 的 `/data/secret.key`。SMTP 密码和 Telegram Bot Token 使用该 32 字节密钥经 AES-GCM 加密，密文写入 SQLite；GET 和保存响应不得返回秘密明文，空密码/Token 表示保留旧值。

Codex OAuth 凭据由 app-server 写入各账号隔离的 `CODEX_HOME`，不经过浏览器或 SQLite。`secret.key`、数据库、Codex 目录和日志都可能包含敏感运行信息，不得提交 Git、加入镜像层或复制到前端。

## HTTP 边界

JSON 解码限制为 1 MiB 并拒绝未知字段。统一安全头包括限制性 CSP、`nosniff`、禁止 iframe 和 same-origin referrer。前端路由、按钮禁用和邮箱掩码均不是服务端授权边界；所有新敏感端点必须在后端经过 `require`，改变 API 方法时还要核对来源头逻辑。
