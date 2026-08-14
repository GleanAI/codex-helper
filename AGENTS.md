# AGENTS.md

本文件是代理在本仓库工作的常驻入口。这里只保留每次任务都应知道的规则；具体机制按任务阅读 [`docs/`](docs/README.md) 中的专题文档。代码和测试是当前行为的最终事实来源。

## 沟通语言

所有对话回复必须使用简体中文。代码、命令、路径、标识符、API 路径、配置项、日志原文、英文专有名词与缩写保持原样。

## 项目概览

Codex Helper 是单容器部署的 Codex 账户用量仪表盘，通过 Codex app-server 读取 ChatGPT/Codex 账户、限额和 Token 历史，并通过 Telegram 与 SMTP 发送重置提醒。

- `backend/`：Go 1.26、`net/http` 与 SQLite 后端；[`backend/CONTRACT.md`](backend/CONTRACT.md) 是 API 路径、状态码、响应结构和兼容文案的契约。
- `frontend/`：React 19、TypeScript、Vite、Recharts 前端；生产构建嵌入 Go 二进制。
- `Dockerfile`：依次构建前端、Go 后端和构建时解析的最新 Codex CLI，最终以 UID `10001` 非 root 用户运行；可通过 build arg 固定 Codex 版本。
- `docker-compose.yml`：对外映射端口并将全部运行数据保存到 `codex-helper-data` 卷的 `/data`。

## 开始工作前

1. 先阅读改动相关的代码、调用方和测试，不要仅凭文档推断行为。
2. 从 [`docs/README.md`](docs/README.md) 选择对应专题；修改 API 时同时核对 [`backend/CONTRACT.md`](backend/CONTRACT.md)。
3. 修改鉴权、SQLite、账号删除、Codex 进程、凭据、提醒、备份或部署时，先读 [`docs/reference/engineering-invariants.md`](docs/reference/engineering-invariants.md)。
4. 执行代码审查时，必须遵循 [`docs/reference/code-review-rules.md`](docs/reference/code-review-rules.md)。

## 环境与执行政策

宿主机只保证 Docker 和 Docker Compose。依赖安装、Go/Node 命令、构建、测试、格式化、类型检查和安全扫描均在容器内执行；不得在宿主机安装或直接运行 `go`、`node`、`npm` 等工具，也不得修改宿主机工具链或 shell 配置。

后端基线为 `golang:1.26.6-bookworm`，前端为 `node:24.19.0-bookworm-slim`，E2E 使用与 `@playwright/test` 版本匹配的官方 Playwright 镜像。完整命令见 [`docs/guides/development-and-validation.md`](docs/guides/development-and-validation.md)。纯文档改动无需运行应用构建。

## 验证与审查

- 行为修改必须验证成功路径、关键边界和失败路径；修复缺陷时优先添加能复现原问题的回归测试。
- 后端至少运行相关 Go 测试以及 `gofmt` 检查、`go build ./...` 和 `go vet ./...`；SQLite 迁移、并发或备份改动必须运行相关集成测试。
- 前端至少运行相关 Vitest 和 `npm run build`；布局、路由、账号设置或响应式交互改动运行相关 Playwright 测试。
- Dockerfile、Compose、持久化或运行用户改动必须验证镜像或 Compose 配置，并核对升级数据路径。
- 代码审查只报告可复现、由当前改动引入或暴露的问题，按 P0–P3 排序；完整流程见 [`docs/reference/code-review-rules.md`](docs/reference/code-review-rules.md)。

## 必须保持的约束

- HTTP 业务接口保持在 `/api/v1/`；未初始化状态只开放 status、setup 和 login，其他接口必须经过 session 与非只读请求来源校验。
- `backend/internal/store/store.go` 中的 schema 和兼容迁移是 SQLite 结构的事实来源；启动迁移必须保留旧库数据并保持幂等。
- 账号 ID `1` 的 Codex 凭据固定保留在 `/data/codex`；其他账号使用 `/data/accounts/<id>/codex`，升级时不得搬迁旧路径。
- 每个账号拥有独立 app-server 进程和 `CODEX_HOME`；启动、初始化、同步和停止必须维持现有串行化与并发保护。
- 删除账号会删除数据库历史和对应凭据目录，是不可恢复操作；前后端必须保持明确确认与精确目标。
- session cookie 只保存随机 token，数据库只存 SHA-256 摘要；密码继续使用 argon2id，SMTP 密码和 Telegram Token 继续由 `/data/secret.key` 加密。
- 设置接口不得返回 SMTP 密码或 Telegram Token 明文；秘密不得进入 Git、日志、前端状态快照或 Docker 构建上下文。
- 限额百分比、窗口和 Token 摘要以 app-server 返回值为准；optional、`null`、多 bucket 和未知套餐必须安全降级。
- 提醒以稳定 dedupe key 去重，失败只在计划时间后六小时窗口内重试；提前、计划后和异常提前重置语义不得混淆。
- SQLite 下载仅是包含已提交 WAL 数据的一致性数据库快照，不包含 `secret.key` 或 Codex 凭据；完整恢复必须备份整个 `/data`。
- 前端路由和隐藏控件只负责交互，安全边界必须由后端强制执行；账号邮箱在界面中继续掩码显示。

## 文档维护

- 后端运行、认证、Codex 集成、数据和通知机制写入 `docs/backend/`；前端实现写入 `docs/frontend/`。
- 可执行的开发与部署步骤写入 `docs/guides/`；工程不变量和代码审查规则写入 `docs/reference/`。
- 用户功能和部署概览继续维护在根 `README.md`；端点级 API 契约维护在 `backend/CONTRACT.md`，专题文档通过链接引用。
- 行为变更必须同步更新相关文档；文档与实现冲突时先以代码和测试为准，再修正文档。
