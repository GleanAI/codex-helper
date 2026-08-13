# Codex Helper 文档中心

代码和测试是当前行为的最终事实来源。本目录解释跨文件机制、工程约束和操作流程，不替代源码、根 `README.md` 或后端 API 契约。

## 按任务导航

| 任务 | 必读文档 |
| --- | --- |
| 修改启动、路由、中间件、健康检查或后台任务 | [`backend/runtime-and-api.md`](backend/runtime-and-api.md) |
| 修改初始化、登录、session、请求来源或秘密 | [`backend/authentication-and-security.md`](backend/authentication-and-security.md) |
| 修改 Codex 登录、账号进程、同步、套餐或用量解析 | [`backend/codex-integration-and-usage.md`](backend/codex-integration-and-usage.md) |
| 修改 SQLite、提醒、Telegram、SMTP、清理或备份 | [`backend/data-notifications-and-backup.md`](backend/data-notifications-and-backup.md) |
| 修改 React 路由、总览、设置、状态或 API 调用 | [`frontend/application.md`](frontend/application.md) |
| 开发、构建、测试或检查格式 | [`guides/development-and-validation.md`](guides/development-and-validation.md) |
| 修改镜像、Compose、运行用户或持久化 | [`guides/deployment.md`](guides/deployment.md) |
| 修改高风险行为或核对工程约束 | [`reference/engineering-invariants.md`](reference/engineering-invariants.md) |
| 代码审查 | [`reference/code-review-rules.md`](reference/code-review-rules.md) |
| 修改 API 路径、状态码、字段或兼容文案 | [`../backend/CONTRACT.md`](../backend/CONTRACT.md) |

## 目录职责

- `backend/`：后端运行、认证、Codex 集成、数据和通知的实现事实。
- `frontend/`：前端路由、状态、渲染和 API 集成。
- `guides/`：可执行的开发、验证和部署流程。
- `reference/`：工程不变量与代码审查等查表型规则。

## 维护规则

同一事实只保留一个权威位置，其他文档通过链接引用。用户功能和部署概览继续维护在根 [`README.md`](../README.md)；端点级 API 契约维护在 [`backend/CONTRACT.md`](../backend/CONTRACT.md)。只有出现需要长期保留取舍背景的真实决策时，才新增 ADR，不创建空目录或占位文件。
