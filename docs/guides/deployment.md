# 部署与运行

用户可见的安装和日常操作以根 [`README.md`](../../README.md) 为准；本文记录维护代码时必须理解的构建和数据边界。

## 镜像结构

`Dockerfile` 有四个阶段：Node 24.19.0 构建 React 静态资源；Go 1.26.6 以 `CGO_ENABLED=0` 构建后端；Node 阶段默认解析并安装 npm registry 中 `@openai/codex` 的 `latest`；最终 Debian bookworm 镜像只包含 CA、时区、后端、Node runtime 和 Codex 包。`APP_VERSION` 构建参数通过 Go linker 注入状态 API，未指定时默认为 `0.2.0`，前端在初始化页和管理后台品牌区域显示该版本。

Codex 阶段将请求版本对应的 npm 元数据作为构建缓存输入，并从中读取确切版本后安装。每次构建都会检查该元数据；`latest` 指向新版本时只会使 Codex 阶段及其依赖层失效，前后端未变化的构建层仍可复用。已经生成的镜像不会自行升级。需要回滚或验证兼容性时，可通过 `--build-arg CODEX_VERSION=0.147.0` 指定确切版本；该参数同样接受 npm registry 可解析的其他版本标识。

前端产物复制到 `backend/internal/web/dist` 后嵌入二进制。运行层使用 UID `10001` 的 system 用户 `helper`，默认 `DATA_DIR=/data`、`LISTEN_ADDR=:8080`，并暴露 `/data` volume 和 8080。健康检查调用二进制自身的 `healthcheck` 子命令。

## 发布镜像

仓库根目录的 `push-image.sh` 构建 `linux/amd64`、`linux/arm64` 镜像并推送至 Docker Hub。版本号必须形如 `0.3.0` 或 `0.3.0-beta.1`，同时作为应用版本和镜像标签；脚本还会更新 `latest`：

```bash
docker login -u koalalove
bash push-image.sh 0.3.0
# 完整重新构建：bash push-image.sh 0.3.0 --no-cache
```

发布镜像包含发布构建时解析到的 Codex CLI 版本。镜像 tag 后续不会因 npm `latest` 更新而改变，必须重新构建并发布才会包含新版 Codex CLI。

脚本会创建并选用 `codex-helper-builder` buildx builder，并尝试通过 `tonistiigi/binfmt` 注册跨架构模拟器。发布前应先完成本地镜像验证；脚本执行成功即会推送远端标签。

## Compose 与持久化

`docker-compose.yml` 本地构建 `codex-helper:latest`，并通过 `APP_VERSION=dev` 让初始化页和管理后台的版本徽标显示为 `vdev`；它将宿主机 8180 映射到容器 8080，并把命名卷 `codex-helper-data` 挂载到 `/data`。全部数据库、密钥、Codex 配置和多账号凭据都依赖这个卷。

`docker-compose.release.yml` 不在本地构建，而是固定拉取 `koalalove/codex-helper:latest`；该镜像的界面版本由 `push-image.sh` 发布时传入的真实版本号决定，不受开发版 `APP_VERSION=dev` 影响。它将宿主机 8180 映射到容器 8080，并将完整 `/data` 通过 bind mount 保存到 `./data`；该文件不依赖 `.env`。由于应用以 UID/GID `10001` 运行，首次启动前必须创建该目录并将其所有者设为 `10001:10001`：

```bash
mkdir -p ./data
sudo chown -R 10001:10001 ./data
docker compose -f docker-compose.release.yml up -d
```

Release 部署升级时在同一目录中执行 `pull` 和 `up -d`，并保留整个 `./data` 目录。从现有命名卷切换前，必须先停止容器、将整个 `/data` 复制到目标目录，再确认 UID `10001` 可读写；Compose 不会自动迁移旧卷。

升级应使用：

```bash
git pull --ff-only
docker compose up -d --build
```

不要使用 `docker compose down -v`，也不要在未确认 volume 名和备份前重建、迁移或删除数据卷。需要改变运行 UID、volume 或 `DATA_DIR` 时，必须提供旧数据权限和路径的升级验证。

## 网络与安全

应用自身监听 HTTP。公网部署应放在 HTTPS 反向代理后，初始化前限制访问来源，并保证到 OpenAI 登录/Codex 服务、Telegram Bot API 和所选 SMTP 服务的出站连接。当前只有 `LISTEN_ADDR` 是 Compose 显式环境变量；运行配置由前端保存到数据库。

设备码登录不需要 OpenAI API Key。Codex app-server 为每个 `CODEX_HOME` 管理 ChatGPT token；不要把这些目录暴露为静态文件或外部共享目录。

## 备份与恢复

维护接口下载的 SQLite 快照适合查看或数据库级备份，但不包含解密密钥和 Codex 凭据。完整恢复步骤是：停止容器、备份或恢复整个 `/data`、确认 UID `10001` 可读写、再启动并检查 `/health/live`、`/health/ready`、管理员登录和各账号连接。恢复过程不得只替换数据库而遗失对应 `secret.key`。
