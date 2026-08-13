# Codex Helper

一个单容器运行的 Codex 账户用量仪表盘。通过 Codex app-server 读取当前 ChatGPT/Codex 账户、套餐、限额窗口、重置时间和每日 Token 历史，并支持 Telegram 与 SMTP 重置提醒。

## 功能

- 支持添加并切换多个 Codex 账号或工作区（包括同一邮箱的个人订阅与 Team 工作区）
- 展示 Codex 账户、套餐、剩余额度和下次重置时间
- 展示累计 Token、单日峰值、连续使用天数及每日 Token 趋势
- 在本地 SQLite 中保留历史用量和限额快照
- 在限额重置前、重置后发送 Telegram 或邮件提醒
- 通过 Telegram 菜单查询当前用量、重置时间、历史概览和账户信息
- 所有运行期配置均可通过前端完成
- React 前端、Go 后端、Codex CLI 和 SQLite 运行在同一个容器中

## 运行要求

- Docker Engine
- Docker Compose v2（使用 `docker compose` 命令）
- 一台能够访问 GitHub、OpenAI 登录页面及 Codex 服务的主机
- 一个可使用 Codex 的 ChatGPT 账户

项目不需要单独准备 OpenAI API Key。当前界面使用 ChatGPT 设备码登录，并由容器内的 Codex app-server 保存和刷新登录凭据。OpenAI 官方的设备码流程说明见 [Codex App Server 文档](https://learn.chatgpt.com/docs/app-server#3b-log-in-with-chatgpt-device-code-flow)。

## 安装

```bash
git clone git@github.com:zhoujun0601/codex-helper.git
cd codex-helper
docker compose up -d --build
```

查看运行状态：

```bash
docker compose ps
docker compose logs -f codex-helper
```

容器健康后访问：

```text
http://服务器地址:8080
```

首次打开页面时创建管理员账号，并设置所在时区。用户名至少 3 位，密码至少 10 位。

> 首次初始化没有额外安装码。创建管理员之前，不要将端口直接暴露到不可信网络。公网部署应使用 HTTPS 反向代理，并限制初始化阶段的访问来源。

## 连接 Codex 账户

1. 使用管理员账号登录 Codex Helper。
2. 打开“设置中心” → “Codex”。
3. 点击“生成设备码”。
4. 点击页面显示的 OpenAI 验证地址，或在另一台设备的浏览器中打开该地址。
5. 登录需要监控的 ChatGPT/Codex 账户。
6. 输入页面显示的一次性设备码并确认授权。
7. 返回 Codex Helper，等待数秒后进入“用量总览”，点击“立即刷新”。
8. 页面显示账户邮箱、套餐和限额窗口后，即表示连接成功。

设备码授权在浏览器中完成，适用于 Docker、NAS 和远程服务器。默认连接的登录凭据保存在 `/data/codex`，新增连接保存在 `/data/accounts/<账号 ID>/codex`，不会写入浏览器或项目源码。

如需添加其他账号，或同一邮箱下的个人订阅与 Team 工作区，请在“设置中心” → “Codex”中分别创建连接并完成设备码登录。每个连接使用隔离的登录凭据，可自定义名称并在总览顶部切换。删除连接会同时删除对应凭据和历史数据。

创建连接时可选择预期的“个人订阅”或“Team / Business 工作区”。授权完成后，Codex Helper 会使用 app-server 返回的真实套餐进行校验；`team` 和当前 Business 系列套餐均识别为团队工作区。设备码接口本身不能指定工作区 ID，因此同一邮箱包含多个空间时，需要在授权页面进入目标空间；如果页面提示类型不匹配，请退出该连接后重新授权。

## 通用设置与提醒时间

进入“设置中心” → “通用”，可设置：

- 时区：用于初始化配置；界面时间按浏览器本地时区显示
- 同步间隔：1–60 分钟
- 历史保留时间：30、60、90、180 或 365 天
- 提前提醒时间：重置前 1–1440 分钟
- 是否发送重置前提醒和重置后确认；重置后确认也会通过额度百分比回落识别并提醒官方活动、临时补发等提前重置

提醒只会针对 Codex app-server 返回的限额窗口发送。发送失败的提醒会在计划时间后的六小时内自动重试。

## 配置 Telegram

1. 在 Telegram 中联系 [@BotFather](https://t.me/BotFather)，使用 `/newbot` 创建 Bot，并取得 Bot Token。
2. 打开“设置中心” → “Telegram”。
3. 填写 Bot Token，按需勾选“启用提醒”和“启用查询菜单”。
4. 点击“验证并保存”。
5. 点击“生成绑定码”。
6. 在 Telegram 中打开刚创建的 Bot，发送页面显示的命令，例如：

   ```text
   /bind 123456
   ```

7. 收到“绑定成功”后，返回页面点击“发送测试”。

绑定码十分钟内有效，一个实例只绑定一个 Telegram 会话。即使关闭查询菜单，仍可完成绑定并接收提醒；开启菜单后可使用以下按钮或命令：

- 当前用量（包含所有已添加连接）
- 重置时间 / `/reset`
- 历史概览 / `/usage`
- 账户信息 / `/account`
- 立即刷新 / `/refresh`

服务器必须能够访问 `https://api.telegram.org`。

## 配置 SMTP 邮件

进入“设置中心” → “SMTP 邮件”，填写：

- SMTP 服务器和端口
- 用户名和密码或应用专用密码
- 加密方式：STARTTLS、隐式 TLS 或无加密
- 发件名称、发件地址和收件地址
- “启用邮件提醒”开关

保存后点击“发送测试邮件”。常见组合为 STARTTLS/587 或隐式 TLS/465，具体值以邮件服务商文档为准。密码使用 `/data/secret.key` 加密后存入 SQLite，不会由设置接口返回明文。

## 日常操作

更新项目：

```bash
git pull --ff-only
docker compose up -d --build
```

停止和重新启动：

```bash
docker compose stop
docker compose start
```

查看日志：

```bash
docker compose logs --tail=200 codex-helper
```

健康检查：

```bash
curl http://localhost:8080/health/live
curl http://localhost:8080/health/ready
```

## 数据、备份与恢复

所有持久数据位于 Docker 卷的 `/data`：

- `codex-helper.db`：管理员、设置、历史用量和通知记录
- `secret.key`：用于解密 SMTP 密码和 Telegram Token
- `codex/`：Codex 登录凭据及配置

登录管理页面后，可直接在浏览器访问以下地址下载一致性 SQLite 快照：

```text
http://服务器地址:8080/api/v1/maintenance/backup
```

SQLite 快照不包含 `secret.key` 和 `codex/`。完整灾难恢复必须同时备份整个 `/data` 数据卷；恢复时先停止容器，再恢复全部内容，并确保文件所有者仍可被容器中的 UID `10001` 读取。

不要使用 `docker compose down -v`，该命令会删除持久数据卷。

## 数据边界

Codex Helper 展示的是 Codex app-server 实际返回的数据。当前接口提供：

- ChatGPT/Codex 账户和套餐类型
- 限额窗口使用百分比、窗口长度和重置时间
- 累计 Token、单日峰值、连续使用天数、最长任务时长等摘要
- 每日总 Token 桶

当前接口不提供调用次数、输入 Token、输出 Token、订阅价格或账单续期日，因此总览使用“最长任务时长”作为替代摘要指标。部分摘要或每日数据也可能因账户或服务端暂未返回而显示为“暂无”。根据 OpenAI 官方文档，`account/usage/read` 需要 Codex 服务支持的身份认证；仅 API Key 或 Bedrock 登录不能读取这些 ChatGPT 用量数据。

## 常见问题

### 页面显示“app-server 未连接”

先查看日志并重启服务：

```bash
docker compose logs --tail=200 codex-helper
docker compose restart codex-helper
```

确认主机时间准确、DNS 和 HTTPS 出站访问正常。应用会自动重启并重新初始化异常的 app-server 进程。

### 完成设备码授权后仍显示“尚未连接”

- 等待几秒后点击“立即刷新”。
- 确认授权的是需要监控的 ChatGPT 账户。
- 重新进入 Codex 设置，退出账户后再次生成设备码。
- 检查 `docker compose logs -f codex-helper` 中是否存在网络或认证错误。

### Telegram 无法绑定

- 必须先“验证并保存”Bot Token，再生成绑定码。
- 将完整的 `/bind 数字` 命令发送给对应 Bot，而不是 BotFather。
- 绑定码只有十分钟有效，过期后重新生成。
- 确认服务器能够访问 Telegram Bot API。

### SMTP 测试失败

- 核对端口和加密方式是否匹配。
- 部分服务商要求使用应用专用密码，而不是网页登录密码。
- 检查云服务商是否封禁 SMTP 出站端口。
- 确认发件地址符合 SMTP 账号或服务商的代发规则。

## 本地开发

前端与后端分别位于 `frontend/` 和 `backend/`。

维护项目或使用编码代理前，请先阅读 [AGENTS.md](AGENTS.md) 和[维护者文档中心](docs/README.md)；修改 HTTP 接口时同时核对[后端 API 契约](backend/CONTRACT.md)。维护者的可复现验证基线统一使用 Docker，下面的宿主机命令仅用于人工本地开发。

启动前端开发服务器：

```bash
cd frontend
npm install
npm run dev
```

启动后端：

```bash
cd backend
export DATA_DIR=/tmp/codex-helper-data
go run ./cmd/server
```

本地开发需要 Go、Node.js、npm，并确保 `codex` 命令在 `PATH` 中。Vite 默认将 `/api` 和 `/health` 代理到 `http://localhost:8080`。
