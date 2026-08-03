#!/bin/bash
#
# Koko EE 镜像构建脚本
# 用法: ./build-ee.sh [VERSION] [PLATFORM]
#

set -e

VERSION="${1:-4.10.16}"
PLATFORM="${2:-linux/amd64}"
CE_TAG="jumpserver/koko:${VERSION}-ce"
EE_TAG="registry.fit2cloud.com/jumpserver/koko:v${VERSION}-ee"

echo "=========================================="
echo "  Koko Docker EE 镜像构建"
echo "  Version: ${VERSION}"
echo "  PLATFORM: ${PLATFORM}"
echo "=========================================="
echo ""

# Step 1: 构建基础 CE 镜像
echo "[1/2] 构建 CE 基础镜像 (${CE_TAG}) [${PLATFORM}]..."
docker buildx build \
    --platform ${PLATFORM} \
    --build-arg VERSION="${VERSION}" \
    -t "${CE_TAG}" \
    -f Dockerfile \
    . \
    --load
echo "✓ CE 镜像构建完成"
echo ""

# Step 2: 构建 EE 镜像
echo "[2/2] 构建 EE 镜像 (${EE_TAG}) [${PLATFORM}]..."
docker buildx build \
    --platform ${PLATFORM} \
    --build-arg VERSION="${VERSION}" \
    -t "${EE_TAG}" \
    -f Dockerfile-ee \
    . \
    --load
echo "✓ EE 镜像构建完成"
echo ""

# Step 3: 导出镜像为 gz 文件
TAR_FILE="koko-ee-${VERSION}.tar.gz"
echo "[3/3] 导出镜像到 ${TAR_FILE}..."
docker save "${EE_TAG}" | gzip > "${TAR_FILE}"
echo "✓ 导出完成"
echo ""

echo "=========================================="
echo "  构建完成！"
echo "  镜像:  ${EE_TAG}"
echo "  文件:  ${TAR_FILE}"
echo "=========================================="
