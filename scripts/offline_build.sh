#!/bin/bash
# =============================================================================
# APR Tracker — 离线服务器构建与部署脚本
# 用法: ./scripts/offline_build.sh [--clean] [--run] [--web]
# =============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# ---- 默认参数 ----
CLEAN=false
RUN=false
WEB_MODE=false

for arg in "$@"; do
    case $arg in
        --clean) CLEAN=true ;;
        --run)   RUN=true ;;
        --web)   WEB_MODE=true ;;
    esac
done

cd "$PROJECT_ROOT"

echo "============================================"
echo "  APR Tracker 离线构建"
echo "============================================"
echo "项目根目录: $PROJECT_ROOT"
echo "Go 版本:    $(go version)"
echo ""

# ---- 1. 环境检查 ----
echo "[1/5] 检查构建环境..."

# 检查 Go
if ! command -v go &>/dev/null; then
    echo "❌ 未找到 Go，请先安装 Go 1.21+"
    echo "   离线安装: 将 go1.21.*.linux-amd64.tar.gz 解压到 /usr/local/go"
    echo "   然后: export PATH=/usr/local/go/bin:\$PATH"
    exit 1
fi

GO_VERSION=$(go version | grep -oP 'go\d+\.\d+' | head -1)
echo "  Go 版本: $GO_VERSION"

# 检查 gcc (modernc.org/sqlite 用纯 Go 实现，不需要 CGO)
echo "  CGO_ENABLED=0 (纯静态编译，不需要 gcc)"

# ---- 2. vendor 检查 ----
echo "[2/5] 检查 vendor 依赖..."
if [ ! -d "vendor" ]; then
    echo "❌ vendor/ 目录不存在！"
    echo "   这是离线迁移包，vendor/ 应该已在迁移时包含。"
    echo "   如果丢失，请在联网机器上运行: go mod vendor"
    exit 1
fi

VENDOR_COUNT=$(find vendor -name '*.go' | wc -l)
echo "  vendor 包含 $VENDOR_COUNT 个 Go 源文件"

# ---- 3. 清理旧产物 ----
if $CLEAN; then
    echo "[3/5] 清理旧编译产物..."
    rm -rf bin/
    echo "  已清理"
else
    echo "[3/5] 跳过清理 (--clean 可强制清理)"
fi

# ---- 4. 编译 ----
echo "[4/5] 开始编译..."

BUILD_FLAGS="-mod=vendor"
BUILD_FLAGS="$BUILD_FLAGS -ldflags=\"-s -w\""
BUILD_FLAGS="$BUILD_FLAGS -o bin/apr-tracker"
BUILD_FLAGS="$BUILD_FLAGS ./cmd/apr-tracker/"

mkdir -p bin

echo "  执行: CGO_ENABLED=0 go build $BUILD_FLAGS"

if CGO_ENABLED=0 go build -mod=vendor -ldflags="-s -w" -o bin/apr-tracker ./cmd/apr-tracker/ 2>&1; then
    FILE_SIZE=$(ls -lh bin/apr-tracker | awk '{print $5}')
    echo "  ✅ 编译成功"
    echo "  产物: bin/apr-tracker ($FILE_SIZE)"
    file bin/apr-tracker
else
    echo "  ❌ 编译失败"
    exit 1
fi

# ---- 5. 运行（可选） ----
if $RUN; then
    echo ""
    echo "============================================"
    echo "  启动 APR Tracker"
    echo "============================================"
    echo ""
    if $WEB_MODE; then
        echo "模式: Web (--web)"
        exec ./bin/apr-tracker -config configs/config.yaml --web
    else
        echo "模式: TUI"
        exec ./bin/apr-tracker -config configs/config.yaml
    fi
fi

echo ""
echo "============================================"
echo "  ✅ 离线构建完成"
echo "============================================"
echo ""
echo "=== 运行命令 ==="
echo "  # TUI 终端模式"
echo "  ./bin/apr-tracker -config configs/config.yaml"
echo ""
echo "  # Web 模式"
echo "  ./bin/apr-tracker -config configs/config.yaml --web -addr :8080"
echo ""
echo "=== 纯静态验证 ==="
echo "  file bin/apr-tracker"
echo "  # 应输出: statically linked, stripped"
echo ""
