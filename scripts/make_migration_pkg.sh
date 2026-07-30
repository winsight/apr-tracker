#!/bin/bash
# =============================================================================
# APR Tracker — 离线迁移包制作脚本（在联网开发机上运行）
# 用法: ./scripts/make_migration_pkg.sh [输出目录]
# =============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
OUTPUT_DIR="${1:-$PROJECT_ROOT/../migration_pkg}"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
PKG_NAME="apr-tracker-offline-${TIMESTAMP}"
PKG_DIR="$OUTPUT_DIR/$PKG_NAME"

echo "============================================"
echo "  APR Tracker 离线迁移包制作"
echo "============================================"
echo "项目根目录: $PROJECT_ROOT"
echo "输出目录:   $PKG_DIR"
echo ""

# ---- 1. 确保 vendor 最新 ----
echo "[1/6] 更新 Go module 依赖缓存 (vendor)..."
cd "$PROJECT_ROOT"
go mod tidy
go mod vendor
echo "  vendor 大小: $(du -sh vendor/ | cut -f1)"

# ---- 2. 创建输出目录结构 ----
echo "[2/6] 创建迁移包目录结构..."
rm -rf "$PKG_DIR"
mkdir -p "$PKG_DIR"/{src,go-cache,vendor-copy}

# ---- 3. 复制项目源码（排除不需要的文件） ----
echo "[3/6] 复制项目源码..."
cd "$PROJECT_ROOT"
rsync -av --exclude='vendor/' --exclude='bin/' --exclude='.git/' \
      --exclude='.claude/' --exclude='parser/' --exclude='go_prompt.md' \
      --exclude='*.swp' --exclude='*.swo' --exclude='*~' \
      --exclude='migration_pkg/' \
      ./ "$PKG_DIR/src/" 2>&1 | tail -1

# ---- 4. 复制 vendor 目录 ----
echo "[4/6] 复制 vendor 依赖..."
cp -a vendor "$PKG_DIR/src/" 2>&1 || rsync -a vendor "$PKG_DIR/src/"
echo "  vendor 已复制"

# ---- 5. 收集 Go 工具链信息 ----
echo "[5/6] 收集 Go 工具链信息..."
GO_VERSION=$(go version | awk '{print $3}')
GO_ROOT=$(go env GOROOT)
echo "  当前 Go 版本: $GO_VERSION"
echo "  GOROOT:       $GO_ROOT"
echo "$GO_VERSION" > "$PKG_DIR/GO_VERSION.txt"
echo "$GO_ROOT" > "$PKG_DIR/GOROOT.txt"

# 复制 Go toolchain（如果可能，太大了就不复制）
echo "  提示: Go toolchain 通常很大 (~200MB)，不自动复制。"
echo "  请确保目标服务器已安装 Go $GO_VERSION 或更高版本。"
echo "  下载地址: https://go.dev/dl/"

# ---- 6. 创建校验清单 ----
echo "[6/6] 创建完整性校验清单..."
cd "$PKG_DIR"
find src -type f | sort > "$PKG_DIR/manifest.txt"

# ---- 打包 ----
echo ""
echo "打包为 tar.gz..."
cd "$OUTPUT_DIR"
tar -czf "${PKG_NAME}.tar.gz" "$PKG_NAME"
PACKAGE_SIZE=$(du -sh "${PKG_NAME}.tar.gz" | cut -f1)

# ---- 完成 ----
echo ""
echo "============================================"
echo "  ✅ 离线迁移包制作完成"
echo "============================================"
echo " 包路径:   $OUTPUT_DIR/${PKG_NAME}.tar.gz"
echo " 包大小:   $PACKAGE_SIZE"
echo " 目录结构:"
echo "   $PKG_NAME/"
echo "   ├── src/          # 项目源码 + vendor/"
echo "   ├── GO_VERSION.txt"
echo "   └── manifest.txt"
echo ""
echo "=== 迁移到离线服务器后 ==="
echo "  1. scp ${PKG_NAME}.tar.gz user@target:/path/"
echo "  2. tar -xzf ${PKG_NAME}.tar.gz"
echo "  3. cd src/"
echo "  4. 编辑 configs/config.yaml（修改路径）"
echo "  5. CGO_ENABLED=0 go build -mod=vendor -ldflags=\"-s -w\" \\"
echo "       -o bin/apr-tracker ./cmd/apr-tracker/"
echo "  6. ./bin/apr-tracker -config configs/config.yaml"
echo ""
