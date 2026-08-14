# 前端应用

前端位于 `frontend/`，使用 React 19、TypeScript、Vite、React Router、Lucide 和 Recharts。应用壳和页面位于 `frontend/src/main.tsx`，API 错误与请求生命周期集中在 `api.ts`，传输类型和运行时 decoder 位于 `types.ts`，认证与主题分别由 context 管理。Recharts 图表通过动态 import 独立打包。

## 状态与路由

应用启动先请求 `system/status`，已初始化时再请求 `auth/me`。未初始化渲染安装页；未登录渲染登录页；登录后由 `BrowserRouter` 提供 `/` 总览和 `/settings` 设置，未知路径回到 `/`。状态响应中的构建版本以 `v<version>` 徽标显示在安装页、登录页和登录后侧栏的品牌区域；登录后侧栏使用放大的品牌图标，图标、名称和版本徽标保持单行排列。这些分支只负责交互，服务端 session 才是安全边界。

主题以服务端通用设置为持久来源，`localStorage` 仅用于首屏缓存；system 模式会跟随系统主题变化。当前账号 ID 保存到 `localStorage`。总览先加载账号列表，为当前账号加载 Dashboard，并在每轮请求完成 30 秒后刷新；切换账号会取消旧请求并校验响应账号，避免迟到响应覆盖当前账号。手动刷新调用账号级 sync 后重新读取 Dashboard。

桌面端使用固定侧栏，`800px` 及以下改为紧凑顶栏和固定底部主导航；GitHub、主题和退出位于顶栏辅助菜单。移动布局最低支持 320px，使用动态视口高度和 CSS safe-area 环境变量避开 iOS 浏览器工具栏与设备安全区。内容必须为底部导航保留空间，表单控件避免 iOS 聚焦缩放，主要交互保持至少 44px 触控区域。

## API 客户端

所有请求使用相对 `/api/v1/`、`credentials: same-origin` 和 `X-Requested-With: codex-helper`，仅 JSON body 设置 content type。请求支持取消和 timeout；非 2xx 响应转换为保留 status 的 `ApiError`，401 会统一回到登录界面。JSON 响应先作为 `unknown`，经端点 decoder 校验后进入组件。新增下载或非 JSON 响应不能直接套用当前 `api` helper。

API 类型精确区分后端 `null` 与 optional，并为秘密设置拆分读写形状；空数组及 unknown 套餐按后端返回值处理。邮箱在 Web 界面的账号选择器、总览和设置中统一经过 `maskEmail`；不得把未掩码邮箱添加到新的 Web 可见位置。Telegram `/account` 是独立的已绑定会话输出，当前显示完整邮箱。

## 总览与设置

总览显示账户连接、套餐、每个限额窗口的剩余百分比和重置时间、四项摘要及每日 Token 图。“历史天数”使用每日 Token 趋势的日桶数量，空历史显示 `0 天`，并与 Telegram 历史概览保持一致。`usedPercent` 展示前限制到 0–100，但原始数据语义不得在 API 类型层改写。缺失摘要显示“暂无”，缺失限额显示明确空状态。

设置页四个 tab 全部保持挂载，以保留未提交表单状态；非活动 panel 使用 `aria-hidden` 和 `inert` 隔离。tab 支持方向键、Home 和 End，程序化切换不得改变页面滚动和布局。账号删除必须保留不可撤销确认，并清理正在显示的设备码和本地账号选择。

## 验证重点

纯逻辑和组件测试使用 Vitest；Oxlint 检查 TypeScript/React 正确性，Prettier 检查格式，构建门禁限制单个 JavaScript chunk 不超过 500 KB。浏览器测试使用 Playwright 的 desktop Chrome、Android Chrome 和 iOS WebKit 项目，覆盖设置布局、移动导航、键盘操作、隐藏表单隔离、账号删除、窄屏溢出、邮箱掩码、session 失效和跨账号竞态。修改 effect、轮询或异步加载时应覆盖卸载清理、错误状态及旧响应覆盖新状态；修改 CSS 时同时跑三个浏览器项目。
