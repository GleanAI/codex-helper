# 数据、通知与备份

## SQLite 与迁移

数据库位于 `${DATA_DIR:-/data}/codex-helper.db`，启用 WAL、5 秒 busy timeout 和 foreign keys。`backend/internal/store/store.go` 是 schema 与启动迁移的事实来源：

- `settings` 保存通用、SMTP、Telegram、绑定码及安装标记；秘密单独以密文 key 保存。
- `admin` 与 `sessions` 保存唯一管理员和登录会话。
- `accounts` 保存 Codex 连接元数据与期望套餐类型。
- `daily_usage` 和 `limit_snapshots` 按 `account_id` 保存历史，删除账号时级联删除。
- `notifications` 保存稳定去重键、调度时间、结构化消息、状态、次数和错误。
- `telegram_updates` 保存 Bot API offset。

启动迁移必须幂等并兼容早期单账号库：创建默认账号 1，把旧用量和限额数据迁入该账号，补 `expected_kind` 和通知 body。schema 变化应增加覆盖旧结构且保留已有行的测试。

## 清理与备份

每分钟调度器根据 `retentionDays` 清理旧限额、通知和每日用量；允许范围为 30–365 天。`maintenance/backup` 使用 SQLite `VACUUM INTO` 创建独立一致性快照，包含已提交 WAL 数据且不中断写入。

数据库快照不包含 `/data/secret.key`、`/data/codex` 或 `/data/accounts/*/codex`，因此不能单独恢复通知凭据和 Codex 登录。完整灾难恢复必须停止容器并备份、恢复整个 `/data`，同时保持 UID `10001` 可读。不得把 `docker compose down -v` 写成普通升级步骤。

## 提醒生成与重试

限额快照按 `(account_id, limit_id, window_type)` 比较。计划提醒的 key 还包含 `resets_at` 和 `before|after`；异常提前重置以旧快照 ID 生成 `detected_after` key。百分比回落必须超过 `0.01`，旧快照不得超过六小时。

处理器每分钟为当前 Dashboard 生成到期提醒，并只发送 `scheduled_at` 后六小时内的未发送记录。Telegram 与 SMTP 中任何启用渠道失败都会把记录标记为 failed，后续周期在窗口内重试；全部启用渠道成功才标记 sent。稳定 key 和 `INSERT OR IGNORE` 是防重复边界。

## Telegram 与 SMTP

Telegram 保存加密 Token、Chat ID、启用开关和菜单开关。保存 Token 前调用 `getMe`；long polling timeout 为 25 秒，HTTP client timeout 为 35 秒。六位绑定码十分钟有效且一次成功后清除。只有绑定 Chat ID 且启用菜单时才处理查询命令；每条 update 在处理前重新核对当前 Token 和开关，关闭菜单会通过 `remove_keyboard` 清理客户端键盘。更换 Token 或删除配置会重置 update offset。解除绑定原子删除 Token、Chat ID、Bot 信息、开关和绑定码；随后清理 Telegram 键盘失败不会恢复本地秘密。

SMTP 支持 `starttls`、隐式 `tls` 和 `none`，TLS 最低 1.2；支持可选 PLAIN AUTH，发送 multipart text/html。TCP 连接和后续 SMTP/TLS 读写共享 35 秒 deadline。修改外部调用时必须保留这一超时边界、TLS server name、HTML 转义和不记录秘密的错误处理。
