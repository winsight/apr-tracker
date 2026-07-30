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

// CellUsageParser 解析 Instance VT 类型使用比例
type CellUsageParser struct {
	cfg *models.AppConfig
}

// NewCellUsageParser 构造函数
func NewCellUsageParser(cfg *models.AppConfig) *CellUsageParser {
	return &CellUsageParser{cfg: cfg}
}

// Name 返回解析器名称
func (p *CellUsageParser) Name() string { return "CellUsageParser" }

// Parse 执行 Cell / VT 用量解析
func (p *CellUsageParser) Parse(ctx context.Context, modulePath, version, moduleName string, stagesFound []string) (interface{}, error) {
	targetStages := map[string]string{
		"10": "10.initial",
		"30": "30.place",
		"40": "40.cts",
		"50": "50.route",
	}

	rptBase := filepath.Join(modulePath, "rpt", version)
	result := make(map[string]models.CellUsageReport)

	for _, stageLabel := range targetStages {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		dirName := fmt.Sprintf("%s.%s.timeDesign.dir", moduleName, stageLabel)
		fullDir := filepath.Join(rptBase, dirName)
		fileName := fmt.Sprintf("%s_cellSum.rpt", moduleName)
		fullPath := filepath.Join(fullDir, fileName)

		if !fileExists(fullPath) {
			continue
		}

		stepData, err := p.parseCellSum(ctx, fullPath)
		if err != nil {
			fmt.Printf("[CellUsageParser] 解析 %s 失败: %v\n", fullPath, err)
			continue
		}
		if stepData != nil && len(stepData) > 0 {
			result[stageLabel] = stepData
		}
	}

	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

// parseCellSum 解析 _cellSum.rpt 文件
func (p *CellUsageParser) parseCellSum(ctx context.Context, filepath_ string) (models.CellUsageReport, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	data := make(models.CellUsageReport)
	inVTSection := false

	f, err := os.Open(filepath_)
	if err != nil {
		return nil, fmt.Errorf("打开文件失败: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	for scanner.Scan() {
		lineStr := strings.TrimSpace(scanner.Text())
		if lineStr == "" {
			continue
		}

		// 侦测 VT 数据表格开头: vt_type ... area ... count ...
		if strings.HasPrefix(lineStr, "vt_type") && strings.Contains(lineStr, "area") && strings.Contains(lineStr, "count") {
			inVTSection = true
			continue
		}

		// 跳过表格分隔线
		if inVTSection && strings.HasPrefix(lineStr, "---") {
			continue
		}

		if inVTSection {
			parts := strings.Fields(lineStr)
			if len(parts) >= 5 {
				vtName := parts[0]
				data[vtName] = &models.VTUsageEntry{
					Area:      parts[1],
					AreaPerc:  parts[2],
					Count:     parts[3],
					CountPerc: parts[4],
				}
			}

			// 读到 total 行结束
			if strings.HasPrefix(lineStr, "total") {
				break
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("扫描文件出错: %w", err)
	}

	if len(data) == 0 {
		return nil, nil
	}
	return data, nil
}

var _ models.Parser = (*CellUsageParser)(nil)
