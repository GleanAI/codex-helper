# Codex Helper

一个单容器运行的 Codex 账户用量仪表盘，支持每日 Token 历史、限额窗口、重置提醒、Telegram 查询菜单与 SMTP 邮件。

## 快速启动

```bash
docker compose up -d --build
```

打开 `http://localhost:8080` 创建管理员。初始化后在“设置中心”完成 Codex 设备码登录、Telegram 和 SMTP 配置。除监听端口与数据卷外，运行期设置均在前端管理。

> 首次初始化未设置安装码。不要在创建管理员前将端口暴露到不可信网络。

## 数据与备份

所有持久数据位于 Docker 卷的 `/data`。可在设置中心下载 SQLite 备份。完整恢复时停止容器，替换数据库及 `secret.key`、`codex/` 后再启动；三者应一起备份。

## 数据边界

Codex app server 当前提供套餐类型、限额窗口、重置时间、累计及每日总 Token。它不提供调用次数、输入 Token、输出 Token、订阅价格或账单续期日，因此界面会将这些字段标记为不可用。

## 开发

前端与后端分别位于 `frontend/` 和 `backend/`。启动两个开发进程：

```bash
cd frontend
npm install
npm run dev
```

```bash
cd backend
go run ./cmd/server
```

开发时将 `DATA_DIR` 指向可写目录，并确保 `codex` 在 `PATH` 中。生产环境建议使用 HTTPS 反向代理。
