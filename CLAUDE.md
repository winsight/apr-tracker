# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test

```bash
# 纯静态编译（零 CGO，离线服务器兼容）
CGO_ENABLED=0 go build -mod=vendor -ldflags="-s -w" -o bin/apr-tracker ./cmd/apr-tracker/

# 快速编译（联网开发机，自动下载依赖）
CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/apr-tracker ./cmd/apr-tracker/

# 运行（TUI 终端模式默认，--web 切换 HTTP）
./bin/apr-tracker -config configs/config.yaml
./bin/apr-tracker -config configs/config.yaml --web -addr :8080

# 运行单个包的全部测试
go test -mod=vendor ./internal/models/...

# 运行单个测试
go test -mod=vendor -run TestStageTimingDataRoundtrip ./internal/models/ -v

# 生成 vendor（依赖变更后必须执行，保证离线编译可用）
go mod tidy && go mod vendor

# 制作离线迁移包
./scripts/make_migration_pkg.sh
```

## Architecture

本项目遵循 Clean Architecture 分层，依赖方向严格向内：

```
cmd/apr-tracker/main.go          ← 仅依赖注入 + 启动（无业务逻辑）
  ├── internal/config/           ← YAML 加载 + 模块路径模糊匹配
  ├── internal/models/           ← 所有层共用的 DTO + Parser 接口
  ├── internal/db/               ← SQLite DAO（modernc.org/sqlite 纯 Go 驱动）
  ├── internal/backend/          ← 解析引擎（5 个 Parser + 编排器）
  └── internal/ui/
      ├── tui/tui.go             ← Bubble Tea 终端看板（默认主界面）
      └── server.go              ← HTTP API + 静态文件（--web 备选）
```

**关键设计决策：**

- **CGO_ENABLED=0 是硬性要求**。所有依赖必须是纯 Go：`modernc.org/sqlite`（非 `mattn/go-sqlite3`）、`gopkg.in/yaml.v3`、Bubble Tea 系列。**绝对不要引入任何需要 CGO 的库。**
- **Fyne 不可用**：它依赖 CGO/OpenGL，与 `CGO_ENABLED=0` 冲突。GUI 方案用 Bubble Tea TUI。
- `vendor/` 目录存在但不纳入 git（太大），通过 `go mod vendor` 生成，离线迁移包脚本自动包含。
- `go_prompt.md` 和 `parser/` 目录是 Python 参考代码，不纳入版本管理。

## Parser 接口与注册机制

所有 Parser 实现 `models.Parser` 接口：

```go
type Parser interface {
    Name() string
    Parse(ctx context.Context, modulePath, version, moduleName string, stagesFound []string) (interface{}, error)
}
```

- `ctx` 为第一参数（支持超时/取消），返回 `interface{}` 由 engine 做类型断言分发。
- `engine.go` 的 `NewEngine()` 按 `config.yaml` 中 `enabled_parsers` 列表动态注册。
- 新增 Parser 步骤：(1) 实现接口 → (2) 在 `NewEngine()` 的 switch 中加 case → (3) 在 `parseVersion()` 的 switch 中加类型断言分发 → (4) 在 `parserHasData()` 中加跳过判断。

## 增量解析机制

`engine.go` 的 `RunParse()` 接收 `existingRecords` 参数。`parserHasData()` 判断某个 Parser 的数据是否已在 DB 中存在：

- **TimingParser 永不跳过**：summary 文件小、解析快，且 `timeDesign.dir` 下可能随时新增 GIF。
- **其他 Parser**：数据不变则跳过，避免重复扫描大日志文件。

## StageTimingData 序列化陷阱

`StageTimingData` 有**自定义 `MarshalJSON` 和 `UnmarshalJSON`**——Groups 字段（reg2reg/in2reg/reg2out 等）在 JSON 中被扁平化到顶层。修改结构体字段时**必须同步更新两个方法**，否则 DB 存取会丢失数据。

## 模块路径模糊匹配

`config.ResolveModulePath()` 实现：YAML 中的模块名（如 `PCIE_ICL0`）通过后缀匹配定位到实际目录（如 `21.APR.PCIE_ICL0`），自动选择最新修改的匹配目录。

## 前端静态资源

`internal/ui/templates/libs/` 下的 Vue.js / ECharts / SweetAlert2 / Tailwind 文件是手动下载的，不在任何包管理器中。如需更新版本，直接替换文件即可。
