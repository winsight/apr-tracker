// Package main 项目入口，仅负责读取配置、依赖注入和启动前后端服务。
// 严禁包含具体业务逻辑。
//
// 默认启动 Bubble Tea TUI 终端看板，支持 --web 标志切换到 Web 模式。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"apr-tracker/internal/backend"
	"apr-tracker/internal/config"
	"apr-tracker/internal/db"
	"apr-tracker/internal/models"
	"apr-tracker/internal/ui"
	tui "apr-tracker/internal/ui/tui"
)

func main() {
	configPath := flag.String("config", "configs/config.yaml", "配置文件路径")
	dbPath := flag.String("db", "", "SQLite 数据库路径（默认使用系统临时目录）")
	addr := flag.String("addr", ":8080", "HTTP 监听地址（仅 --web 模式）")
	webMode := flag.Bool("web", false, "使用 Web 模式（默认使用 TUI 终端模式）")
	flag.Parse()

	// 1. 加载配置
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("❌ 加载配置失败: %v", err)
	}
	fmt.Printf("✅ 配置加载成功 [%s]\n", *configPath)

	// 2. 初始化数据库
	if *dbPath == "" {
		*dbPath = filepath.Join(os.TempDir(), "apr_tracker.db")
	}
	database, err := db.New(*dbPath)
	if err != nil {
		log.Fatalf("❌ 数据库初始化失败: %v", err)
	}
	defer database.Close()
	fmt.Printf("✅ 数据库初始化成功 [%s]\n", *dbPath)

	// 3. 创建解析引擎
	engine := backend.NewEngine(cfg)
	fmt.Printf("✅ 解析引擎就绪，已注册 %d 个解析器: %v\n",
		len(engine.GetRegisteredParsers()), engine.GetRegisteredParsers())

	if *webMode {
		runWeb(cfg, database, engine, *addr)
	} else {
		runTUI(cfg, database, engine)
	}
}

// runTUI 启动 Bubble Tea 终端界面
func runTUI(cfg *models.AppConfig, database *db.Database, engine *backend.Engine) {
	model := tui.NewModel(database, engine, cfg)

	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	// 优雅关闭：监听系统信号，确保 tea.Program 正确退出
	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer stop()

	go func() {
		<-ctx.Done()
		p.Quit()
	}()

	if _, err := p.Run(); err != nil {
		log.Fatalf("❌ TUI 运行失败: %v", err)
	}

	fmt.Println("👋 APR Tracker 已退出")
}

// runWeb 启动 HTTP Web 服务（备选模式）
func runWeb(cfg *models.AppConfig, database *db.Database, engine *backend.Engine, addr string) {
	server := ui.NewServer(cfg, database, engine)

	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer stop()

	go func() {
		<-ctx.Done()
		fmt.Println("\n🛑 正在关闭服务...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			fmt.Printf("关闭服务出错: %v\n", err)
		}
	}()

	if err := server.Start(addr); err != nil {
		log.Fatalf("❌ 服务启动失败: %v", err)
	}
}
