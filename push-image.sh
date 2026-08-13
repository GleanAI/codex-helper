#!/bin/bash
# Codex Helper 单容器多架构镜像构建并推送到 Docker Hub。
# 用法: bash push-image.sh <version> [--no-cache]
set -e

cd "$(dirname "$0")"

OWNER="koalalove"
IMAGE="codex-helper"
BUILDER="codex-helper-builder"
VERSION="${1:-}"
NO_CACHE=0

if [ -z "$VERSION" ] || [ "${VERSION:0:2}" = "--" ]; then
  echo "✗ 缺少版本号。用法: bash push-image.sh <version> [--no-cache]" >&2
  echo "  例: bash push-image.sh 0.3.0 --no-cache" >&2
  exit 1
fi
if ! echo "$VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$'; then
  echo "✗ 版本号格式非法: '$VERSION'（应形如 0.3.0 或 0.3.0-beta.1）" >&2
  exit 1
fi

shift
for arg in "$@"; do
  case "$arg" in
    --no-cache) NO_CACHE=1 ;;
    *)
      echo "✗ 未知参数: $arg" >&2
      exit 1
      ;;
  esac
done

echo "=================================================="
echo " Codex Helper 单容器多架构镜像构建"
echo " 版本号:      v${VERSION}（镜像 tag + 界面显示）"
echo " 目标仓库:    ${OWNER}/${IMAGE}"
echo " 目标平台:    linux/amd64, linux/arm64"
if [ "$NO_CACHE" = "1" ]; then
  echo " 缓存策略:    --no-cache（完整重新构建）"
else
  echo " 缓存策略:    使用缓存（加 --no-cache 参数可禁用）"
fi
echo "=================================================="
echo ""
echo "请确保已执行: docker login -u ${OWNER}"
echo ""

echo "[准备] 注册 QEMU/binfmt 多架构模拟..."
docker run --rm --privileged tonistiigi/binfmt --install all >/dev/null 2>&1 \
  || echo "[警告] binfmt 注册失败（若宿主已内置模拟可忽略），继续尝试构建。"

if ! docker buildx inspect "$BUILDER" >/dev/null 2>&1; then
  echo "[准备] 创建多架构 buildx builder: ${BUILDER}..."
  docker buildx create --name "$BUILDER" --driver docker-container --bootstrap
fi
docker buildx use "$BUILDER"

BUILD_ARGS=(
  --platform linux/amd64,linux/arm64
  --file Dockerfile
  --build-arg "APP_VERSION=${VERSION}"
  --tag "${OWNER}/${IMAGE}:${VERSION}"
  --tag "${OWNER}/${IMAGE}:latest"
  --push
)
[ "$NO_CACHE" = "1" ] && BUILD_ARGS+=(--no-cache)

echo "[1/1] 构建并推送 Codex Helper 镜像 v${VERSION}..."
docker buildx build "${BUILD_ARGS[@]}" .

echo ""
echo "=================================================="
echo " ✓ 构建推送完成！"
echo " 镜像: ${OWNER}/${IMAGE}:${VERSION}"
echo "       ${OWNER}/${IMAGE}:latest"
echo ""
echo " 快速启动:"
echo "   docker run -d \\"
echo "     -p 8180:8080 \\"
echo "     -v codex-helper-data:/data \\"
echo "     --name codex-helper \\"
echo "     ${OWNER}/${IMAGE}:${VERSION}"
echo "=================================================="
