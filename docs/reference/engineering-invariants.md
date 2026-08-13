# 工程不变量

修改鉴权、SQLite、账号删除、Codex 进程、秘密、通知、备份或部署边界前核对本页。

## API 与认证

- [`backend/CONTRACT.md`](../../backend/CONTRACT.md) 是路径、状态码、响应字段和兼容文案的契约。匿名入口只能是当前 status、setup 和 login。
- 所有受保护端点必须回查 session；非只读请求还必须验证 `X-Requested-With`。前端路由与按钮不能代替后端门禁。
- session 原 token 只进入 cookie，SQLite 只保存摘要；密码保持 argon2id。错误和日志不得包含密码、cookie、Bot Token、SMTP 密码或 Codex token。
- 初始化是事务性单管理员创建。新增自动初始化能力前必须保留并发与首次公网暴露的安全边界。

## 数据与账号

- `internal/store/store.go` 是 SQLite schema 和兼容迁移的事实来源。启动迁移必须幂等、保留旧数据并启用 foreign keys。
- 旧单账号数据迁到账号 1；账号 1 的凭据路径永久为 `/data/codex`，不能统一搬到 `accounts/1`。
- 删除账号必须先停止并移除精确 runtime，再删除该账号数据库行和精确凭据目录；不得使用未校验路径、glob 或宽泛递归删除。
- `daily_usage` 与 `limit_snapshots` 以 `account_id` 隔离。任何查询、更新、提醒 key 或清理不得串账号。
- `expectedKind` 只是校验期望；未知套餐保持 unknown。相同邮箱提示重复不能成为自动合并依据。

## Codex 运行时

- 每账号一套 app-server 和 `CODEX_HOME`。进程存活与协议 ready 是不同状态，初始化失败必须关闭旧进程。
- lifecycle mutex 串行启动/停止，syncing mutex 串行同步和 Dashboard 访问，state mutex 保护 ready/stopped；不得以无锁读写替换。
- 旧子进程晚退出不能断开新子进程或失败新请求。请求取消和 20 秒 timeout 必须清除 pending channel。
- `account/read` 是同步的硬依赖；rate limits 与 usage 是可缺失数据。optional、`null`、多 bucket 和未知字段必须安全降级。

## 通知、外部输入与秘密

- SMTP 密码和 Telegram Token 使用 `/data/secret.key` 的 AES-GCM 密文保存；设置响应继续掩去秘密。密钥丢失不能通过返回密文或明文“修复”。
- 提醒 dedupe key 必须包含账号、limit、window、reset 或旧 snapshot 身份及 kind；重试窗口保持计划时间后六小时。
- Telegram/SMTP 用户文本进入 HTML 前必须转义。Telegram HTTP client 和 SMTP 连接/读写必须保留 timeout；SMTP TLS 最低 1.2 并验证 server name。
- Telegram 绑定码十分钟且成功即消费；其他 chat 在绑定后不得查询实例数据。

## 前端与部署

- Web 界面中的所有可见账号邮箱继续掩码；Telegram `/account` 当前会向已绑定会话显示完整邮箱。账号切换清空旧 Dashboard；删除账号清除设备码和 localStorage 选择。
- 非活动设置 panel 保持 `inert`，tab 键盘和布局稳定性是现有可访问性契约。
- 前端生产资源嵌入 Go 二进制；Docker build stage 的复制顺序变化必须验证实际嵌入的是新产物。
- `/data` 是唯一完整恢复单元。数据库快照不含 `secret.key` 和 Codex 凭据；任何文档不得暗示其可完整灾难恢复。
- 运行数据、数据库、密钥、凭据、`.env`、`node_modules` 和构建缓存不得进入 Git 或 Docker 构建上下文。
