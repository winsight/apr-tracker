# APR Tracker — 离线迁移与编译部署手册

## 目录

1. [快速开始（联网机器）](#1-快速开始联网机器)
2. [制作离线迁移包](#2-制作离线迁移包)
3. [迁移到离线服务器](#3-迁移到离线服务器)
4. [离线服务器上构建](#4-离线服务器上构建)
5. [配置与运行](#5-配置与运行)
6. [常见问题排错](#6-常见问题排错)
7. [开发修改后重新迁移](#7-开发修改后重新迁移)

---

## 1. 快速开始（联网机器）

```bash
# 克隆仓库
git clone git@github.com:winsight/apr-tracker.git
cd apr-tracker

# 下载依赖
go mod tidy
go mod vendor   # 生成 vendor/ 目录

# 构建
CGO_ENABLED=0 go build -mod=vendor -ldflags="-s -w" -o bin/apr-tracker ./cmd/apr-tracker/

# 运行
./bin/apr-tracker -config configs/config.yaml
```

---

## 2. 制作离线迁移包

在**联网开发机**上执行：

```bash
cd /path/to/apr-tracker

# 给脚本执行权限
chmod +x scripts/make_migration_pkg.sh

# 制作迁移包
./scripts/make_migration_pkg.sh

# 输出示例:
#   /path/to/migration_pkg/apr-tracker-offline-20260731_120000.tar.gz

# 传输到离线服务器
scp /path/to/migration_pkg/apr-tracker-offline-*.tar.gz user@target-server:/home/user/
```

迁移包包含：
```
apr-tracker-offline-XXXXXXXX/
└── src/
    ├── cmd/apr-tracker/main.go
    ├── configs/config.yaml
    ├── go.mod / go.sum
    ├── internal/
    │   ├── config/config.go
    │   ├── models/models.go
    │   ├── db/database.go
    │   ├── backend/
    │   │   ├── scanner.go
    │   │   ├── timing_parser.go
    │   │   ├── drc_parser.go
    │   │   ├── latency_parser.go
    │   │   ├── runtime_parser.go
    │   │   ├── cell_usage_parser.go
    │   │   └── engine.go
    │   └── ui/
    │       ├── server.go
    │       ├── tui/tui.go
    │       └── templates/
    │           ├── index.html
    │           └── libs/
    │               ├── vue.global.js
    │               ├── echarts.min.js
    │               ├── sweetalert2.all.min.js
    │               ├── sweetalert2-fix.css
    │               └── tailwind.min.css
    ├── scripts/
    │   └── offline_build.sh
    ├── vendor/          ← 所有 Go 依赖的完整源码
    └── MIGRATION.md
```

---

## 3. 迁移到离线服务器

### 3.1 前提条件

离线服务器需要：

| 依赖 | 版本要求 | 如何获取 |
|---|---|---|
| Go toolchain | ≥ 1.21 | [go.dev/dl](https://go.dev/dl/) 下载 `go1.21.*.linux-amd64.tar.gz` |
| 操作系统 | Linux amd64 / arm64 | — |
| 磁盘空间 | ~500 MB（源码 + vendor + 编译产物） | — |

**重要**：本项目使用 `CGO_ENABLED=0` 纯静态编译，**不需要 gcc / glibc / 任何 C 库**。

### 3.2 在离线服务器上安装 Go

```bash
# 1. 将 go1.21.13.linux-amd64.tar.gz 传输到离线服务器
scp go1.21.13.linux-amd64.tar.gz user@target:/tmp/

# 2. 在离线服务器上安装
ssh user@target
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf /tmp/go1.21.13.linux-amd64.tar.gz
echo 'export PATH=/usr/local/go/bin:$PATH' >> ~/.bashrc
source ~/.bashrc
go version  # 验证安装
```

### 3.3 解压迁移包

```bash
cd /home/user/
tar -xzf apr-tracker-offline-*.tar.gz
cd apr-tracker-offline-*/src/

# 确认 vendor 存在
ls vendor/ | head -5
# 应该看到: github.com/  modernc.org/  golang.org/  gopkg.in/
```

---

## 4. 离线服务器上构建

```bash
cd /path/to/src  # 进入迁移包中的 src/ 目录

# 方法 A: 使用构建脚本（推荐）
chmod +x scripts/offline_build.sh
./scripts/offline_build.sh

# 方法 B: 手动构建
CGO_ENABLED=0 go build -mod=vendor -ldflags="-s -w" -o bin/apr-tracker ./cmd/apr-tracker/

# 验证产物
file bin/apr-tracker
# 期望输出: ELF 64-bit LSB executable ... statically linked ... stripped

ls -lh bin/apr-tracker
# 约 10 MB

# 验证无动态链接
ldd bin/apr-tracker
# 期望输出: not a dynamic executable
```

### 构建参数说明

| 参数 | 作用 |
|---|---|
| `CGO_ENABLED=0` | 禁用 CGO，纯 Go 编译 |
| `-mod=vendor` | 使用本地 vendor/ 目录，不访问网络 |
| `-ldflags="-s -w"` | 去除调试信息，缩减二进制体积 |
| `-o bin/apr-tracker` | 输出路径 |

---

## 5. 配置与运行

### 5.1 修改配置文件

迁移后**必须**修改 `configs/config.yaml` 中的路径：

```yaml
# 修改为离线服务器上的实际路径
user_root_dir: /home/your_user/Documents/Proj/

owner_modules:
  your_username:
    - LEON
    - PCIE_ICL0

# 修改图片备份路径
image_backup_path: "/data/apr_backup/images/"

# 其他配置按需调整...
```

### 5.2 运行

```bash
# TUI 终端模式（默认）
./bin/apr-tracker -config configs/config.yaml

# Web 模式（通过浏览器访问）
./bin/apr-tracker -config configs/config.yaml --web -addr :8080

# 指定数据库路径
./bin/apr-tracker -config configs/config.yaml -db /data/apr_tracker.db
```

### 5.3 TUI 快捷键

| 操作 | 按键 |
|---|---|
| 切换模块 | `←` `→` |
| 导航版本 | `↑` `↓` |
| 切换视图 Tab | `Tab` |
| 直接跳 Tab | `1`-`6` |
| 查看详情 | `Enter` |
| 返回列表 | `Esc` |
| 触发解析 | `r` |
| 刷新数据 | `R` |
| 退出 | `q` |

---

## 6. 常见问题排错

### Q: `go: cannot find main module`

```
❌ go: cannot find main module; see 'go help modules'
```

**原因**：当前目录不在 Go module 根目录。  
**解决**：确保在包含 `go.mod` 的 `src/` 目录下执行构建。

### Q: `vendor/ directory not found`

```
❌ vendor/ directory not found
```

**原因**：迁移包制作时 vendor 未包含，或解压路径不对。  
**解决**：在联网机器上重新执行 `go mod vendor && ./scripts/make_migration_pkg.sh`。

### Q: `Permission denied` 运行脚本

```bash
chmod +x scripts/*.sh
```

### Q: 编译产物太大

```bash
# 使用 upx 进一步压缩（可选）
upx --best --lzma bin/apr-tracker
# 约 10 MB → 3.5 MB
```

### Q: 目标服务器缺少终端支持（TUI 乱码）

```bash
# 确认终端类型
echo $TERM  # 需要 xterm-256color 或类似的

# 如果终端不支持，使用 Web 模式
./bin/apr-tracker -config configs/config.yaml --web
```

### Q: 数据库损坏

```bash
# 删除数据库重建
rm /tmp/apr_tracker.db
./bin/apr-tracker -config configs/config.yaml
```

---

## 7. 开发修改后重新迁移

### 方案 A: git 增量同步（推荐）

```bash
# 开发机器上
git add -A
git commit -m "feat: xxx"
git push

# 离线服务器上（如果连通了 git）
git pull
go mod tidy && go mod vendor
CGO_ENABLED=0 go build -mod=vendor -ldflags="-s -w" -o bin/apr-tracker ./cmd/apr-tracker/
```

### 方案 B: 仅传输差异文件

```bash
# 开发机器上：重新制作迁移包
./scripts/make_migration_pkg.sh

# 或仅 rsync 源码（不含 vendor，vendor 不变时跳过）
rsync -av --exclude='vendor/' --exclude='bin/' --exclude='.git/' \
      ./ user@target:/path/to/src/
```

### 方案 C: 仅更新 vendor（依赖变更时）

```bash
# 开发机器上
go mod tidy && go mod vendor
tar -czf vendor-update.tar.gz vendor/
scp vendor-update.tar.gz user@target:/path/to/src/
# 离线服务器上
tar -xzf vendor-update.tar.gz
```

---

## 附录：项目依赖清单

| 依赖 | 版本 | 用途 |
|---|---|---|
| `modernc.org/sqlite` | v1.32.0 | 纯 Go SQLite 驱动 |
| `gopkg.in/yaml.v3` | v3.0.1 | YAML 配置解析 |
| `golang.org/x/sync` | v0.7.0 | errgroup 并发控制 |
| `github.com/charmbracelet/bubbletea` | v1.3.10 | TUI 框架 |
| `github.com/charmbracelet/bubbles` | v1.0.0 | TUI 组件（table/viewport） |
| `github.com/charmbracelet/lipgloss` | v1.1.0 | TUI 样式系统 |

**全部为纯 Go 实现，零 CGO 依赖，支持 CGO_ENABLED=0 交叉编译。**
