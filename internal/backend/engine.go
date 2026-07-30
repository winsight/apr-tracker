package backend

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	"golang.org/x/sync/errgroup"

	"apr-tracker/internal/config"
	"apr-tracker/internal/models"
)

// Engine 解析引擎编排器，负责协调所有 Parser 的并发执行与数据汇聚
type Engine struct {
	cfg     *models.AppConfig
	parsers []models.Parser
}

// NewEngine 创建引擎实例，根据配置注册已启用的 Parser
func NewEngine(cfg *models.AppConfig) *Engine {
	e := &Engine{cfg: cfg}

	for _, name := range cfg.EnabledParsers {
		switch name {
		case "TimingParser":
			e.parsers = append(e.parsers, NewTimingParser(cfg))
		case "DRCParser":
			e.parsers = append(e.parsers, NewDRCParser(cfg))
		case "LatencyParser":
			e.parsers = append(e.parsers, NewLatencyParser(cfg))
		case "RuntimeParser":
			e.parsers = append(e.parsers, NewRuntimeParser(cfg))
		case "CellUsageParser":
			e.parsers = append(e.parsers, NewCellUsageParser(cfg))
		default:
			fmt.Printf("[Engine] 未知解析器: %s，跳过注册\n", name)
		}
	}

	return e
}

// ParseResult 单个版本解析的聚合结果
type ParseResult struct {
	Module      string
	Version     string
	StagesFound []string
	Timing      map[string]*models.StageTimingData
	DRC         map[string]*models.DRCTotal
	Latency     *models.LatencyReport
	Runtime     models.RuntimeReport
	CellUsage   map[string]models.CellUsageReport
	Error       error
}

// RunParse 执行解析流程
// 如果 module 非空，仅解析指定模块的当前最新版本 ("current" 模式)
// 如果 module 为空，解析所有模块的所有版本 ("all" 模式)
func (e *Engine) RunParse(ctx context.Context, module string) ([]*ParseResult, error) {
	// 确定要解析的模块列表
	var modules []string
	if module != "" {
		modules = []string{module}
	} else {
		modules = config.GetAllModules(e.cfg)
	}

	var allResults []*ParseResult

	for _, mod := range modules {
		select {
		case <-ctx.Done():
			return allResults, ctx.Err()
		default:
		}

		owner := config.GetModuleOwner(e.cfg, mod)
		if owner == "" {
			fmt.Printf("[Engine] 模块 %s 未找到对应 owner，跳过\n", mod)
			continue
		}

		modulePath, err := config.ResolveModulePath(e.cfg, owner, mod)
		if err != nil {
			fmt.Printf("[Engine] 解析模块路径失败 [%s]: %v\n", mod, err)
			continue
		}

		// 扫描版本
		versions, err := ScanVersions(ctx, modulePath)
		if err != nil {
			fmt.Printf("[Engine] 扫描版本失败 [%s]: %v\n", mod, err)
			continue
		}

		// 并发解析每个版本
		g, gCtx := errgroup.WithContext(ctx)
		results := make([]*ParseResult, len(versions))

		for i, ver := range versions {
			i, ver := i, ver // capture
			g.Go(func() error {
				result, err := e.parseVersion(gCtx, modulePath, mod, ver)
				if err != nil {
					// 单版本解析失败不中断其他版本
					fmt.Printf("[Engine] 解析版本失败 [%s/%s]: %v\n", mod, ver, err)
					result = &ParseResult{
						Module:  mod,
						Version: ver,
						Error:   err,
					}
				}
				results[i] = result
				return nil // 不中断 errgroup
			})
		}

		_ = g.Wait()

		for _, r := range results {
			if r != nil {
				allResults = append(allResults, r)
			}
		}
	}

	return allResults, nil
}

// parseVersion 解析单个版本的完整数据
func (e *Engine) parseVersion(ctx context.Context, modulePath, moduleName, version string) (*ParseResult, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	rptBase := filepath.Join(modulePath, "rpt", version)
	moduleShortName := GetModuleNameFromPath(modulePath)

	// 扫描该版本已完成的阶段
	stages, err := ScanStages(ctx, rptBase, moduleShortName)
	if err != nil {
		return nil, fmt.Errorf("扫描阶段失败: %w", err)
	}

	result := &ParseResult{
		Module:      moduleName,
		Version:     version,
		StagesFound: stages,
	}

	// 并发执行所有已注册的 Parser
	type parserResult struct {
		name string
		data interface{}
		err  error
	}

	parserResults := make([]parserResult, len(e.parsers))
	var wg sync.WaitGroup

	for i, parser := range e.parsers {
		i, parser := i, parser
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, err := parser.Parse(ctx, modulePath, version, moduleShortName, stages)
			parserResults[i] = parserResult{
				name: parser.Name(),
				data: data,
				err:  err,
			}
		}()
	}
	wg.Wait()

	// 将解析结果分发到对应字段
	for _, pr := range parserResults {
		if pr.err != nil {
			fmt.Printf("[Engine] %s 解析出错: %v\n", pr.name, pr.err)
			continue
		}
		if pr.data == nil {
			continue
		}

		switch pr.name {
		case "TimingParser":
			if timing, ok := pr.data.(map[string]*models.StageTimingData); ok {
				result.Timing = timing
			}
		case "DRCParser":
			if drc, ok := pr.data.(map[string]*models.DRCTotal); ok {
				result.DRC = drc
			}
		case "LatencyParser":
			if latency, ok := pr.data.(*models.LatencyReport); ok {
				result.Latency = latency
			}
		case "RuntimeParser":
			if runtime, ok := pr.data.(models.RuntimeReport); ok {
				result.Runtime = runtime
			}
		case "CellUsageParser":
			if cellUsage, ok := pr.data.(map[string]models.CellUsageReport); ok {
				result.CellUsage = cellUsage
			}
		}
	}

	return result, nil
}

// GetRegisteredParsers 返回已注册的解析器名称列表
func (e *Engine) GetRegisteredParsers() []string {
	var names []string
	for _, p := range e.parsers {
		names = append(names, p.Name())
	}
	return names
}
