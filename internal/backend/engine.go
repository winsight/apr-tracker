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
	Skipped     []string // 跳过的解析器名称（数据已存在）
}

// RunParse 执行解析流程
// existingRecords: 数据库中已有的版本记录（用于增量解析，跳过已有数据的 Parser）
// 如果 module 非空，仅解析指定模块（"current" 模式）
// 如果 module 为空，解析所有模块（"all" 模式）
func (e *Engine) RunParse(ctx context.Context, module string, existingRecords []*models.VersionRecord) ([]*ParseResult, error) {
	// 构建 module:version → record 快速查找表
	existingMap := make(map[string]*models.VersionRecord)
	for _, r := range existingRecords {
		key := r.Module + ":" + r.Version
		existingMap[key] = r
	}

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

		versions, err := ScanVersions(ctx, modulePath)
		if err != nil {
			fmt.Printf("[Engine] 扫描版本失败 [%s]: %v\n", mod, err)
			continue
		}

		g, gCtx := errgroup.WithContext(ctx)
		results := make([]*ParseResult, len(versions))

		for i, ver := range versions {
			i, ver := i, ver
			existing := existingMap[mod+":"+ver]
			g.Go(func() error {
				result, err := e.parseVersion(gCtx, modulePath, mod, ver, existing)
				if err != nil {
					fmt.Printf("[Engine] 解析版本失败 [%s/%s]: %v\n", mod, ver, err)
					result = &ParseResult{
						Module:  mod,
						Version: ver,
						Error:   err,
					}
				}
				results[i] = result
				return nil
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

// parseVersion 解析单个版本的完整数据（增量：已有数据的 Parser 跳过）
func (e *Engine) parseVersion(ctx context.Context, modulePath, moduleName, version string, existing *models.VersionRecord) (*ParseResult, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	rptBase := filepath.Join(modulePath, "rpt", version)
	moduleShortName := GetModuleNameFromPath(modulePath)

	stages, err := ScanStages(ctx, rptBase, moduleShortName)
	if err != nil {
		return nil, fmt.Errorf("扫描阶段失败: %w", err)
	}

	// 以已有数据为基底
	result := &ParseResult{
		Module:      moduleName,
		Version:     version,
		StagesFound: stages,
	}
	if existing != nil {
		result.Timing = existing.Timing
		result.DRC = existing.DRC
		result.Latency = existing.Latency
		result.Runtime = existing.Runtime
		result.CellUsage = existing.CellUsage
	}

	// 筛选出需要执行的 Parser（已有数据则跳过）
	type parserResult struct {
		name   string
		data   interface{}
		err    error
		skipped bool
	}

	var toRun []models.Parser
	var toRunIdx []int

	for i, parser := range e.parsers {
		if existing != nil && parserHasData(parser.Name(), existing) {
			result.Skipped = append(result.Skipped, parser.Name())
			fmt.Printf("[Engine] %s/%s: %s 数据已存在，跳过\n", moduleName, version, parser.Name())
		} else {
			toRun = append(toRun, parser)
			toRunIdx = append(toRunIdx, i)
		}
	}

	// 并发执行需要运行的 Parser
	prs := make([]parserResult, len(e.parsers))
	var wg sync.WaitGroup

	for j, parser := range toRun {
		j, parser := j, parser
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, err := parser.Parse(ctx, modulePath, version, moduleShortName, stages)
			prs[toRunIdx[j]] = parserResult{
				name: parser.Name(),
				data: data,
				err:  err,
			}
		}()
	}
	wg.Wait()

	// 将新解析结果覆盖到对应字段
	for _, pr := range prs {
		if pr.err != nil {
			fmt.Printf("[Engine] %s 解析出错: %v\n", pr.name, pr.err)
			continue
		}
		if pr.data == nil {
			continue // Parser 返回 nil 表示无数据（如阶段不存在）
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

// parserHasData 判断某个 Parser 的数据在已有记录中是否已存在
// 注意: TimingParser 永远不跳过，因为:
//   1. summary 文件很小，解析速度极快
//   2. timeDesign.dir 下可能随时新增 GIF 图片，必须每次扫描
func parserHasData(name string, r *models.VersionRecord) bool {
	switch name {
	case "TimingParser":
		return false // 始终运行，确保 GIF 扫描不遗漏
	case "DRCParser":
		return r.DRC != nil && r.DRC["50"] != nil
	case "LatencyParser":
		if r.Latency == nil {
			return false
		}
		return len(r.Latency.Standard) > 0 || len(r.Latency.Cluster) > 0
	case "RuntimeParser":
		return r.Runtime != nil && len(r.Runtime) > 0
	case "CellUsageParser":
		return r.CellUsage != nil && len(r.CellUsage) > 0
	}
	return false
}

// GetRegisteredParsers 返回已注册的解析器名称列表
func (e *Engine) GetRegisteredParsers() []string {
	var names []string
	for _, p := range e.parsers {
		names = append(names, p.Name())
	}
	return names
}
