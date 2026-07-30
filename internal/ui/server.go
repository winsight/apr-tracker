// Package ui 负责前端视图展示层，基于 Gin 提供 REST API 和静态文件服务。
// 仅负责接收底层数据并渲染 / 输出 JSON 响应，不含业务逻辑。
package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"apr-tracker/internal/backend"
	"apr-tracker/internal/config"
	"apr-tracker/internal/db"
	"apr-tracker/internal/models"
)

// Server 封装 HTTP 服务、数据库与配置引用
type Server struct {
	cfg    *models.AppConfig
	db     *db.Database
	engine *backend.Engine
	srv    *http.Server
}

// NewServer 创建 UI 服务实例
func NewServer(cfg *models.AppConfig, database *db.Database, engine *backend.Engine) *Server {
	return &Server{
		cfg:    cfg,
		db:     database,
		engine: engine,
	}
}

// Start 启动 HTTP 服务（阻塞）
func (s *Server) Start(addr string) error {
	mux := http.NewServeMux()

	// API 路由
	mux.HandleFunc("/api/history", s.handleHistory)
	mux.HandleFunc("/api/run_parse", s.handleRunParse)
	mux.HandleFunc("/api/get_config", s.handleGetConfig)
	mux.HandleFunc("/api/save_config", s.handleSaveConfig)
	mux.HandleFunc("/api/delete_history", s.handleDeleteHistory)
	mux.HandleFunc("/api/update_meta", s.handleUpdateMeta)
	mux.HandleFunc("/api/image", s.handleImage)

	// 静态文件服务（前端 HTML/JS/CSS）
	// /libs/ → internal/ui/templates/libs/
	libsFS := http.FileServer(http.Dir(resolveTemplateDir("libs")))
	mux.Handle("/libs/", http.StripPrefix("/libs", libsFS))
	// / → index.html
	mux.HandleFunc("/", s.handleStatic)

	s.srv = &http.Server{
		Addr:         addr,
		Handler:      withCORS(mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	fmt.Printf("🚀 APR Tracker 服务启动于 http://localhost%s\n", addr)
	return s.srv.ListenAndServe()
}

// Shutdown 优雅关闭
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

// ---------- API Handlers ----------

// handleHistory 获取某模块的所有版本历史数据
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, 400, "Method Not Allowed", nil)
		return
	}

	module := r.URL.Query().Get("module")
	if module == "" {
		writeJSON(w, 400, "缺少 module 参数", nil)
		return
	}

	records, err := s.db.GetHistory(module)
	if err != nil {
		writeJSON(w, 500, fmt.Sprintf("查询失败: %v", err), nil)
		return
	}

	if records == nil {
		records = []*models.VersionRecord{}
	}

	writeJSON(w, 0, "", records)
}

// handleRunParse 触发解析流程
func (s *Server) handleRunParse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 400, "Method Not Allowed", nil)
		return
	}

	var req models.RunParseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, fmt.Sprintf("请求解析失败: %v", err), nil)
		return
	}

	if req.Module == "" {
		writeJSON(w, 400, "缺少 module 字段", nil)
		return
	}

	// 根据配置决定解析范围
	targetModule := req.Module
	if s.cfg.RunParsersMode == "all" {
		targetModule = "" // 全量模式
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	results, err := s.engine.RunParse(ctx, targetModule)
	if err != nil {
		writeJSON(w, 500, fmt.Sprintf("解析失败: %v", err), nil)
		return
	}

	// 批量写入数据库
	savedCount := 0
	for _, result := range results {
		if result.Error != nil {
			fmt.Printf("[UI] 跳过错误版本 %s/%s: %v\n", result.Module, result.Version, result.Error)
			continue
		}

		record := &models.VersionRecord{
			Module:      result.Module,
			Version:     result.Version,
			StagesFound: result.StagesFound,
			Timing:      result.Timing,
			DRC:         result.DRC,
			Latency:     result.Latency,
			Runtime:     result.Runtime,
			CellUsage:   result.CellUsage,
		}

		if err := s.db.UpsertVersion(record); err != nil {
			fmt.Printf("[UI] 保存记录失败 %s/%s: %v\n", result.Module, result.Version, err)
			continue
		}
		savedCount++
	}

	msg := fmt.Sprintf("解析完成！共处理 %d 个版本，成功保存 %d 条记录", len(results), savedCount)
	writeJSON(w, 0, msg, nil)
}

// handleGetConfig 返回当前配置
func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, 400, "Method Not Allowed", nil)
		return
	}

	// 构造返回数据（包含动态模块列表）
	modules := config.GetAllModules(s.cfg)
	if len(modules) == 0 {
		// 从数据库补充
		dbModules, _ := s.db.GetAllModules()
		if len(dbModules) > 0 {
			modules = dbModules
		}
	}

	data := map[string]interface{}{
		"user_root_dir":         s.cfg.UserRootDir,
		"owner_modules":         s.cfg.OwnerModules,
		"enabled_parsers":       s.cfg.EnabledParsers,
		"care_flag":             s.cfg.CareFlag,
		"latency_target_corner": s.cfg.LatencyTargetCorner,
		"latency_skew_pattern":  s.cfg.LatencySkewPattern,
		"runtime_parse_mode":    s.cfg.RuntimeParseMode,
		"run_parsers_mode":      s.cfg.RunParsersMode,
		"backup_images":         s.cfg.BackupImages,
		"image_backup_path":     s.cfg.ImageBackupPath,
		"modules":               modules,
	}

	writeJSON(w, 0, "", data)
}

// handleSaveConfig 保存配置（热更新内存中的配置 + 写回 YAML 文件）
func (s *Server) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 400, "Method Not Allowed", nil)
		return
	}

	var newCfg models.AppConfig
	if err := json.NewDecoder(r.Body).Decode(&newCfg); err != nil {
		writeJSON(w, 400, fmt.Sprintf("配置解析失败: %v", err), nil)
		return
	}

	// 更新内存配置
	s.cfg.UserRootDir = newCfg.UserRootDir
	s.cfg.OwnerModules = newCfg.OwnerModules
	s.cfg.EnabledParsers = newCfg.EnabledParsers
	s.cfg.CareFlag = newCfg.CareFlag
	s.cfg.LatencyTargetCorner = newCfg.LatencyTargetCorner
	s.cfg.LatencySkewPattern = newCfg.LatencySkewPattern
	s.cfg.RuntimeParseMode = newCfg.RuntimeParseMode
	s.cfg.RunParsersMode = newCfg.RunParsersMode
	s.cfg.BackupImages = newCfg.BackupImages
	s.cfg.ImageBackupPath = newCfg.ImageBackupPath

	writeJSON(w, 0, "配置已保存", nil)
}

// handleDeleteHistory 删除一条版本记录
func (s *Server) handleDeleteHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 400, "Method Not Allowed", nil)
		return
	}

	var req models.DeleteHistoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, fmt.Sprintf("请求解析失败: %v", err), nil)
		return
	}

	if err := s.db.DeleteVersion(req.Module, req.Version); err != nil {
		writeJSON(w, 500, fmt.Sprintf("删除失败: %v", err), nil)
		return
	}

	writeJSON(w, 0, "删除成功", nil)
}

// handleUpdateMeta 更新备注和父版本
func (s *Server) handleUpdateMeta(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 400, "Method Not Allowed", nil)
		return
	}

	var req models.UpdateMetaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, fmt.Sprintf("请求解析失败: %v", err), nil)
		return
	}

	if err := s.db.UpdateMeta(req.Module, req.Version, req.Note, req.ParentVersion); err != nil {
		writeJSON(w, 500, fmt.Sprintf("更新失败: %v", err), nil)
		return
	}

	writeJSON(w, 0, "保存成功", nil)
}

// handleImage 提供图片文件服务（支持备份路径和原始路径）
func (s *Server) handleImage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, 400, "Method Not Allowed", nil)
		return
	}

	filePath := r.URL.Query().Get("file")
	if filePath == "" {
		http.Error(w, "缺少 file 参数", 400)
		return
	}

	// 安全检查：防止路径穿越
	absPath, err := filepath.Abs(filePath)
	if err != nil {
	http.Error(w, "路径解析失败", 400)
		return
	}

	if !fileExistsSafe(absPath) {
		http.Error(w, "文件不存在", 404)
		return
	}

	http.ServeFile(w, r, absPath)
}

// handleStatic 提供前端静态页面（仅响应 / 路径）
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	indexPath := resolveTemplateDir("index.html")
	if _, err := os.Stat(indexPath); err == nil {
		http.ServeFile(w, r, indexPath)
		return
	}

	// 兜底：返回内嵌的简单 HTML
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(defaultHTML))
}

// resolveTemplateDir 查找模板文件所在目录，按优先级搜索
func resolveTemplateDir(subpath string) string {
	candidates := []string{
		"internal/ui/templates/" + subpath,
		"ui/templates/" + subpath,
		"templates/" + subpath,
		subpath,
	}
	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil {
			if info.IsDir() || subpath != "" {
				return p
			}
		}
	}
	// 兜底返回第一个候选路径
	return candidates[0]
}

// ---------- 辅助函数 ----------

func writeJSON(w http.ResponseWriter, code int, msg string, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	resp := models.APIResponse{
		Code: code,
		Msg:  msg,
		Data: data,
	}
	json.NewEncoder(w).Encode(resp)
}

func fileExistsSafe(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// withCORS 添加跨域和日志中间件
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}

		start := time.Now()
		next.ServeHTTP(w, r)
		fmt.Printf("[%s] %s %s (%v)\n", r.Method, r.URL.Path, r.RemoteAddr, time.Since(start))
	})
}

// defaultHTML 当 index.html 找不到时的兜底页面
const defaultHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <title>APR Tracker</title>
    <style>
        body { font-family: -apple-system, sans-serif; max-width: 800px; margin: 60px auto; padding: 0 20px; }
        h1 { color: #0052cc; }
        .status { background: #e3fcef; padding: 20px; border-radius: 8px; }
    </style>
</head>
<body>
    <h1>📦 APR Tracker Dashboard</h1>
    <div class="status">
        <p>✅ 后端服务运行中</p>
        <p>⚠️ 前端页面 (index.html) 未找到，请将 HTML 文件放置到正确路径。</p>
    </div>
</body>
</html>`
