package backend

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"apr-tracker/internal/models"
)

// LatencyParser 解析 Clock Skew/Latency 报告
type LatencyParser struct {
	cfg *models.AppConfig
}

// NewLatencyParser 构造函数
func NewLatencyParser(cfg *models.AppConfig) *LatencyParser {
	return &LatencyParser{cfg: cfg}
}

// Name 返回解析器名称
func (p *LatencyParser) Name() string { return "LatencyParser" }

// Parse 执行延迟解析
func (p *LatencyParser) Parse(ctx context.Context, modulePath, version, moduleName string, stagesFound []string) (interface{}, error) {
	// 只在 40.cts 及之后才有时钟报告
	if !containsStage(stagesFound, "40") && !containsStage(stagesFound, "50") {
		return nil, nil
	}

	// 动态获取当前模块的 corner 和 pattern 配置
	targetCorner := p.getModuleConfig("latency_target_corner", moduleName, "delay_cworst_CCworst_T:wcz:0:both.late")
	patterns := p.getSkewPatterns(moduleName)

	standardFile := filepath.Join(modulePath, "rpt", version, fmt.Sprintf("%s.ClockSkewGroup.rpt", moduleName))
	clusterFile := filepath.Join(modulePath, "rpt", version, fmt.Sprintf("%s.reportClockSkewGroup_cluster.rpt", moduleName))

	result := &models.LatencyReport{}

	if fileExists(standardFile) {
		entries, err := p.extractLatency(ctx, standardFile, targetCorner, patterns)
		if err != nil {
			fmt.Printf("[LatencyParser] 解析 standard 失败: %v\n", err)
		} else {
			result.Standard = entries
		}
	}

	if fileExists(clusterFile) {
		entries, err := p.extractLatency(ctx, clusterFile, targetCorner, patterns)
		if err != nil {
			fmt.Printf("[LatencyParser] 解析 cluster 失败: %v\n", err)
		} else {
			result.Cluster = entries
		}
	}

	if len(result.Standard) == 0 && len(result.Cluster) == 0 {
		return nil, nil
	}

	return result, nil
}

// extractLatency 从单文件中提取指定 corner 的时钟延迟数据
func (p *LatencyParser) extractLatency(ctx context.Context, filepath_ string, targetCorner string, patterns []string) ([]models.LatencyEntry, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	if len(patterns) == 0 {
		return nil, nil
	}

	var results []models.LatencyEntry
	inTargetCorner := false

	f, err := os.Open(filepath_)
	if err != nil {
		return nil, fmt.Errorf("打开文件失败: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		stripped := strings.TrimSpace(line)

		if stripped == "" || strings.HasPrefix(stripped, "---") || strings.HasPrefix(stripped, "===") {
			continue
		}

		// 非缩进行 → corner 标题行
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			inTargetCorner = strings.Contains(line, targetCorner)
			continue
		}

		if inTargetCorner {
			parts := strings.Fields(line)
			if len(parts) >= 6 {
				skewGroup := parts[0]

				// 检查是否匹配配置的时钟域前缀
				matchedPattern := ""
				for _, pat := range patterns {
					if strings.HasPrefix(skewGroup, pat) {
						matchedPattern = pat
						break
					}
				}

				if matchedPattern != "" {
					skewW := "N/A"
					if len(parts) > 9 {
						skewW = parts[9]
					} else if len(parts) > 5 {
						skewW = parts[5]
					}

					results = append(results, models.LatencyEntry{
						Domain: matchedPattern,
						Group:  skewGroup,
						Min:    parts[2],
						Max:    parts[3],
						Avg:    parts[4],
						SkewW:  skewW,
					})
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("扫描文件出错: %w", err)
	}

	return results, nil
}

// getModuleConfig 从配置中提取模块专属配置值（支持 string 字面值和 dict{module: value, default: value} 两种模式）
// 与 Python _get_module_specific_config 行为一致
func (p *LatencyParser) getModuleConfig(key, moduleName, fallback string) string {
	var raw interface{}
	switch key {
	case "latency_target_corner":
		raw = p.cfg.LatencyTargetCorner
	default:
		return fallback
	}

	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case map[string]interface{}:
		if val, ok := v[moduleName]; ok {
			if s, ok := val.(string); ok {
				return strings.TrimSpace(s)
			}
		}
		if val, ok := v["default"]; ok {
			if s, ok := val.(string); ok {
				return strings.TrimSpace(s)
			}
		}
		return fallback
	default:
		return fallback
	}
}

// getSkewPatterns 从配置中提取模块的时钟域前缀列表
func (p *LatencyParser) getSkewPatterns(moduleName string) []string {
	raw := p.cfg.LatencySkewPattern

	// 先尝试按模块查找
	if val, ok := raw[moduleName]; ok {
		return toStringSlice(val)
	}
	// 再尝试 default
	if val, ok := raw["default"]; ok {
		return toStringSlice(val)
	}
	// 兜底（与 Python 默认值保持一致）
	return []string{"CLK_PLL_ICL_M_OCC_ROOT"}
}

// toStringSlice 将 interface{} 转为 []string
func toStringSlice(v interface{}) []string {
	switch val := v.(type) {
	case string:
		s := strings.TrimSpace(val)
		if s == "" {
			return nil
		}
		return []string{s}
	case []interface{}:
		var result []string
		for _, item := range val {
			if s, ok := item.(string); ok {
				s = strings.TrimSpace(s)
				if s != "" {
					result = append(result, s)
				}
			}
		}
		return result
	case []string:
		var result []string
		for _, s := range val {
			s = strings.TrimSpace(s)
			if s != "" {
				result = append(result, s)
			}
		}
		return result
	default:
		return nil
	}
}

var _ models.Parser = (*LatencyParser)(nil)
