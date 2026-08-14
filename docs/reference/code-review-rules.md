# Codex Helper 代码审查规则

目标是发现可复现的正确性、安全性、兼容性和可靠性问题，而不是增加评论数量。审查必须结合完整 diff、调用链、测试和部署边界，不能孤立阅读修改行。

## 输出格式

findings 优先，按 `P0` 至 `P3` 排序。每条必须包含严重级别和简短标题、精确文件与起始行号、触发输入或执行顺序、实际影响和最小修复方向。同一根因只报告一次。

- `P0`：可利用的安全突破、不可恢复数据丢失或全系统中断。
- `P1`：高概率生产故障、认证绕过、账号凭据删除或核心同步失效。
- `P2`：条件性但真实的错误、兼容性破坏、状态漂移、重复通知或资源风险。
- `P3`：影响有限且不阻塞合并的真实维护问题。

不要报告纯格式偏好、没有现实失败路径的猜测、仓库已有且未被当前改动暴露的问题，或仅以“缺少测试”为问题本身。没有 finding 时明确说明，并列出未执行验证或剩余风险。

## 审查流程

1. 阅读完整 diff，识别 API、认证、SQLite、Codex runtime、通知、前端、部署和持久数据影响。
2. 搜索修改符号的调用方、被调用方、相似路径和测试，走完实际成功与失败路径。
3. 对照 [`engineering-invariants.md`](engineering-invariants.md) 和 [`backend/CONTRACT.md`](../../backend/CONTRACT.md)。
4. 在容器内运行与风险成比例的验证；复核 finding，删除重复根因和不影响用户的推测。

## 后端专项

- 匿名端点是否意外扩大；session、过期检查和非只读来源头是否在所有路径生效；cookie 改动是否适配 HTTPS 反代。
- setup 是否事务化且不能覆盖管理员；登录限流是否存在竞态、无限内存或错误信任代理头。
- SQLite migration 是否可从旧 schema 启动且保留数据；查询是否带正确 `account_id`；rows、transaction 和临时文件是否关闭。
- 删除账号是否精确停止 runtime、级联历史并删除正确目录，尤其是账号 1 的旧路径。
- app-server 生命周期是否可能双启动、停止后重启、死锁或由旧进程回调污染新进程；pending 请求在所有结束路径是否释放。
- app-server optional/null、多 bucket、未知套餐和部分接口失败是否降级，而非生成错误数据或清除有效凭据。
- 提醒是否稳定去重、限定六小时重试、正确区分 before/after/detected reset；多渠道部分失败是否按既定语义重试。
- SMTP、Telegram 错误是否有 timeout、TLS 和 HTML 转义；设置接口、日志和错误是否泄露秘密。
- 备份是否包含 committed WAL 且不阻塞写入；文案是否误称数据库快照为完整恢复包。

## 前端专项

- 初始化、未登录、登录态路由是否无闪烁或循环；401 后是否进入可恢复状态。
- 切换或删除账号时，旧 Dashboard、设备码和 localStorage 是否清理；异步旧响应是否覆盖新账号。
- API 类型是否准确表达 `null`、optional、unknown 和空列表；错误响应是否可能被当作成功。
- 邮箱是否在所有可见位置掩码；服务端文本和外部 URL 是否以安全方式渲染。
- effect、30 秒 polling、设备码 polling、timer 和 event handler 是否在卸载时清理。
- 设置 tab 是否保持键盘操作、`inert` 隔离、表单状态、滚动位置和窄屏无溢出。

## 部署与验证专项

- 固定的 Go、Node 和 Playwright 版本是否同步 Dockerfile、lockfile 与文档；Codex CLI 是否按 npm `latest` 策略解析、记录实际安装版本并保留显式版本覆盖能力；架构和静态构建是否匹配运行层。
- 静态前端是否在 Go build 前正确复制；`.dockerignore` 是否会丢失必须资源或带入运行数据。
- 最终镜像是否继续非 root，`/data` 权限是否兼容 UID `10001`；Compose 升级是否复用原卷。
- 新环境变量是否同步代码、Compose、README 和部署文档；秘密是否可能进入 build arg、镜像层或日志。
- 最低验证遵循 [`development-and-validation.md`](../guides/development-and-validation.md) 的矩阵。纯文档至少检查链接、术语、事实来源和 `git diff --check`。
