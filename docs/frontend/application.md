# 前端应用

前端位于 `frontend/`，使用 React 19、TypeScript、Vite、React Router、Lucide 和 Recharts。`frontend/src/main.tsx` 当前集中承载应用组件和 API 数据类型，`frontend/src/api.ts` 是 fetch 包装层。

## 状态与路由

应用启动先请求 `system/status`，已初始化时再请求 `auth/me`。未初始化渲染安装页；未登录渲染登录页；登录后由 `BrowserRouter` 提供 `/` 总览和 `/settings` 设置，未知路径回到 `/`。这些分支只负责交互，服务端 session 才是安全边界。

主题和当前账号 ID 保存到 `localStorage`。总览先加载账号列表，为当前账号加载 Dashboard，并每 30 秒刷新内存数据；切换账号必须清空旧 Dashboard，避免短暂展示另一账号信息。手动刷新调用账号级 sync 后重新读取 Dashboard。

## API 客户端

所有请求使用相对 `/api/v1/`、`credentials: same-origin`、JSON content type 和 `X-Requested-With: codex-helper`。非 2xx 响应优先显示 `{error}`，否则退化为 HTTP 状态。新增下载或非 JSON 响应不能直接套用当前 `api` helper。

后端的 optional、`null`、空数组及 unknown 套餐必须在 TypeScript 中准确表达。邮箱在账号选择器、总览和设置中统一经过 `maskEmail`；不得把未掩码邮箱添加到新的可见位置。

## 总览与设置

总览显示账户连接、套餐、每个限额窗口的剩余百分比和重置时间、四项摘要及每日 Token 图。`usedPercent` 展示前限制到 0–100，但原始数据语义不得在 API 类型层改写。缺失摘要显示“暂无”，缺失限额显示明确空状态。

设置页四个 tab 全部保持挂载，以保留未提交表单状态；非活动 panel 使用 `aria-hidden` 和 `inert` 隔离。tab 支持方向键、Home 和 End，程序化切换不得改变页面滚动和布局。账号删除必须保留不可撤销确认，并清理正在显示的设备码和本地账号选择。

## 验证重点

纯逻辑和组件测试使用 Vitest；现有浏览器测试使用 Playwright 覆盖设置布局、键盘操作、隐藏表单隔离、账号删除、窄屏溢出和邮箱掩码。修改 effect、轮询或异步加载时应覆盖卸载清理、错误状态及旧响应覆盖新状态；修改 CSS 时同时跑 desktop 和 mobile projects。
