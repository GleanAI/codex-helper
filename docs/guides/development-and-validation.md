# 开发与验证

## 环境政策

宿主机只保证 Docker 和 Docker Compose。所有 Go、Node、npm、格式化、构建、测试和安全扫描都在容器内执行。以下命令从仓库根目录运行；挂载源码时使用只读模式，只有构建输出确实需要写回工作区时才放宽。

## 后端

完整格式、构建、vet 和测试：

```bash
docker run --rm \
  -v "$PWD/backend:/app:ro" \
  -v codex-helper-go-mod:/go/pkg/mod \
  -v codex-helper-go-cache:/root/.cache/go-build \
  -w /app golang:1.26.0-bookworm \
  sh -c 'test -z "$(gofmt -l .)" && go build ./... && go vet ./... && go test -count=1 ./...'
```

针对性测试可把最后一段替换为 `go test -count=1 ./internal/store` 或相应 package。测试通过 `t.TempDir()` 创建专用 SQLite，禁止把 `DATA_DIR` 指向运行实例。

## 前端

在临时副本中安装依赖、构建并运行 Vitest，避免容器把 root 所有权的 `node_modules` 写入工作区：

```bash
docker run --rm -v "$PWD/frontend:/src:ro" node:24.19.0-bookworm-slim \
  sh -c 'cp -a /src /tmp/frontend && cd /tmp/frontend && npm ci --no-audit --no-fund && npm run build && npm test'
```

依赖或安全改动额外运行：

```bash
docker run --rm -v "$PWD/frontend:/src:ro" node:24.19.0-bookworm-slim \
  sh -c 'cp -a /src /tmp/frontend && cd /tmp/frontend && npm ci --no-audit --no-fund && npm audit --omit=dev --audit-level=high'
```

## Playwright E2E

使用与 `frontend/package.json` 中 `@playwright/test` 匹配的官方镜像：

```bash
docker run --rm --ipc=host \
  -v "$PWD/frontend:/src:ro" \
  mcr.microsoft.com/playwright:v1.62.1-noble \
  sh -c 'cp -a /src /tmp/frontend && cd /tmp/frontend && npm ci --no-audit --no-fund && npm run test:e2e'
```

若升级 Playwright，必须同步镜像 tag。E2E 的 API 由 route mock 提供，不要求启动 Go 后端。

## 镜像与 Compose

```bash
docker compose config --quiet
docker build --check .
docker build -t codex-helper:test .
```

涉及容器启动、静态资源或健康检查时，再使用隔离的临时 Compose project 和专用 volume 验证；不得连接或删除用户的运行卷。

## 最低验证矩阵

| 改动 | 最低验证 |
| --- | --- |
| 后端普通逻辑 | 相关 Go test、gofmt 检查、build、vet |
| SQLite schema、迁移、备份 | 新旧 schema 测试、WAL 快照测试、完整后端门禁 |
| app-server 生命周期或同步 | codex/app runtime 单测、并发和失败重试路径 |
| 鉴权、session 或秘密 | security 与 app 测试、成功和拒绝路径 |
| 通知 | reminder 渲染、去重、计划与异常重置测试 |
| 前端 | 相关 Vitest、生产 build |
| 布局、路由、设置或响应式 | Playwright desktop 和 mobile |
| Dockerfile、Compose、持久化 | 配置检查及相应镜像/运行验证 |
| 纯文档 | 链接、术语、事实来源和 `git diff --check` |

先运行针对性检查，再按风险扩大范围。修复缺陷时优先添加修复前会失败的回归测试。
