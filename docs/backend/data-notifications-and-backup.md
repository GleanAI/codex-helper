# 数据、通知与备份

## SQLite 与迁移

数据库位于 `${DATA_DIR:-/data}/codex-helper.db`，启用 WAL、5 秒 busy timeout 和 foreign keys。`backend/internal/store/store.go` 是 schema 与启动迁移的事实来源：

- `settings` 保存通用、SMTP、Telegram、绑定码及安装标记；秘密单独以密文 key 保存。
- `admin` 与 `sessions` 保存唯一管理员和登录会话。
- `accounts` 保存 Codex 连接元数据与期望套餐类型。
- `daily_usage` 和 `limit_snapshots` 按 `account_id` 保存历史，删除账号时级联删除。同步返回的官方日桶以账号和日期为键合并；用于连续图表的零值日期只在读取时生成，不写入数据库。`daily_usage` 是可从官方源恢复的缓存，账号槽位退出登录、身份未知或换绑到不同邮箱时只清除该槽位的记录，避免跨身份展示。
- `notifications` 保存账号外键、稳定去重键、调度时间、结构化消息、状态、次数和脱敏错误；删除账号时级联删除对应通知。
- `telegram_updates` 保存 Bot API offset。

启动迁移必须幂等并兼容早期单账号库：创建默认账号 1，把旧用量、限额以及可识别的旧通知去重记录迁入该账号，补 `expected_kind`、通知 body 和账号外键。schema 变化应增加覆盖旧结构且保留已有行的测试。

## 清理与备份

每分钟调度器根据 `retentionDays` 清理旧限额、通知和每日用量；允许范围为 30–365 天。`maintenance/backup` 使用 SQLite `VACUUM INTO` 创建独立一致性快照，包含已提交 WAL 数据且不中断写入。

数据库快照不包含 `/data/secret.key`、`/data/codex` 或 `/data/accounts/*/codex`，因此不能单独恢复通知凭据和 Codex 登录。完整灾难恢复必须停止容器并备份、恢复整个 `/data`，同时保持 UID `10001` 可读。不得把 `docker compose down -v` 写成普通升级步骤。

## 提醒生成与重试

限额快照按 `(account_id, limit_id, window_type)` 比较。重置前提醒的 key 包含旧周期的 `resets_at` 和 `before`。重置后确认不再仅凭本地时钟到点生成：正常重置必须在旧重置时间后的六小时内看到上游 `resets_at` 推进到未来的新周期，再以旧周期时间生成 `after` key；异常提前重置以六小时内旧快照的 ID 生成 `detected_after` key，且百分比回落必须超过 `0.01`。过期的重置前提醒和无法证明已确认的旧版重置后记录不会发送。

确认事件保存新快照中的剩余比例、下一次重置时间以及该账号的完整限额窗口快照。同步先提交 staged 快照和通知，成功保存账号并发布内存 Dashboard 后，按账号恢复六小时窗口内的全部 staged 记录；因此进程在两步之间崩溃时，下次成功同步仍会继续发送且不会重复。过期 staged 记录标记为 expired。管理端、公开页、Telegram 查询、自动 Telegram 提醒和 SMTP 邮件共享同一份已确认数据。

处理器每分钟为当前 Dashboard 生成到期提醒，并只发送 `scheduled_at` 后六小时内的未发送记录。Telegram 与 SMTP 中任何启用渠道失败都会把记录标记为 failed，后续周期在窗口内重试；全部启用渠道成功才标记 sent。稳定 key 和 `INSERT OR IGNORE` 是防重复边界。

通用设置启用 `autoHello` 后，限额快照会为每个账号符合以下条件的 5 小时窗口创建 `auto_hello` staged 任务：窗口为 300 分钟、`usedPercent` 为零，且 `resetsAt` 与抓取时间相差 5 小时 ±5 分钟。独立的 `auto_hello_state` 标记保存阶段开始时间、成功完成时间 `completed_at` 和已确认的活动窗口结束时间 `active_resets_at`；成功完成时间与通知 sent 状态在同一事务提交。

轻量 Hello 即使已启动窗口，用量百分比也可能仍为零。因此成功后的完整快照在窗口剩余时间大于零且小于 4 小时 55 分钟时，记录上游活动窗口的结束时间。该时间到期后，必须再观测到上游 `resetsAt` 推进且新窗口符合未使用条件，才开启下一阶段；历史任务去重查询同时以真实重置时间为边界，旧成功记录不会阻止新周期。明确非零用量仍会解除旧阶段标记。可选元数据短暂缺失、未启动窗口的 `resetsAt` 持续滑动、仅本地时间经过五小时以及普通历史清理都不会开启重复阶段；关闭开关时保留已确认的结束时间，重新启用后由最新快照确认是否可以排队。

升级时在事务中为旧状态表补充可空字段，仅以当前阶段的成功任务及成功后的有效倒计时快照回填；证据不足则保持去重。后续启动不再重复使用历史任务创建旧标记。账号同步成功发布 Dashboard 后任务才进入 pending；发送前再次核对最新 Dashboard，元数据暂不可确认时保留任务，明确非零时才将任务标记为 expired，并把同账号、限额和窗口的旧重复任务一并淘汰。发送通过该账号的 app-server 独立低 effort 只读 ephemeral turn 完成，只有 `turn/completed` 的最终状态为 `completed` 才标记 sent；超时时使用 `turn/interrupt` 并等待终态，仍无法确认结果时停止自动重试以免重复消耗额度。已确认失败的任务从首次计划时间起按 0、5、15、30、60、120、240 分钟退避，六小时内最多尝试七次；每次获得 thread ID 后都会取消订阅，取消订阅失败则回收该账号的 app-server 进程。

## Telegram 与 SMTP

Telegram 保存加密 Token、Chat ID 和 Bot 信息。保存 Token 前调用 `getMe`；long polling timeout 为 25 秒，HTTP client timeout 为 35 秒。Telegram transport 错误在进入 API、通知记录或日志前会去除含 Token 的 URL；启动迁移也会清理旧通知错误中的 Telegram URL。六位绑定码十分钟有效且一次成功后清除。存在绑定 Chat ID 时自动启用额度提醒和查询菜单；启动时如发现旧版本已绑定但关闭了菜单，会主动发送带键盘的启用通知，成功后写回新状态。每条 update 在处理前重新核对当前 Token 和 Chat ID。更换 Token 或删除配置会重置 update offset。“当前用量”“立即刷新”和“重置时间”按与网页端相同的额度名称和稳定顺序显示窗口，以区分普通 7 天窗口与 `gpt-reserve` 7 天窗口。自动重置提醒也按该顺序显示触发账号在事件生成时的全部限额窗口、剩余比例和重置时间；失败重试复用已保存的窗口快照，只重新计算相对时间。旧版未保存完整窗口的待发送记录继续回退为单窗口文案，SMTP 邮件也保持单窗口内容。解除绑定原子删除 Token、Chat ID、Bot 信息、兼容开关值和绑定码；随后清理 Telegram 键盘失败不会恢复本地秘密。

SMTP 支持 `starttls`、隐式 `tls` 和 `none`，TLS 最低 1.2；支持可选 PLAIN AUTH，发送 multipart text/html。TCP 连接和后续 SMTP/TLS 读写共享 35 秒 deadline。修改外部调用时必须保留这一超时边界、TLS server name、HTML 转义和不记录秘密的错误处理。
