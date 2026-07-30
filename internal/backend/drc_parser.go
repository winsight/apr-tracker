package backend

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"apr-tracker/internal/models"
)

// DRCParser 解析 DRC (Design Rule Check) 违例信息
type DRCParser struct {
	cfg *models.AppConfig
}

// NewDRCParser 构造函数
func NewDRCParser(cfg *models.AppConfig) *DRCParser {
	return &DRCParser{cfg: cfg}
}

// Name 返回解析器名称
func (p *DRCParser) Name() string { return "DRCParser" }

// Parse 执行 DRC 解析
func (p *DRCParser) Parse(ctx context.Context, modulePath, version, moduleName string, stagesFound []string) (interface{}, error) {
	// DRC 只在 50.route 之后才有意义
	if !containsStage(stagesFound, "50") {
		return nil, nil
	}

	logDir := filepath.Join(modulePath, "log", version)
	if !dirExists(logDir) {
		return nil, nil
	}

	// 寻找 50.route 的日志文件（按编号排序取最新）
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return nil, fmt.Errorf("读取 log 目录失败: %w", err)
	}

	var logs []string
	logPattern := regexp.MustCompile(`50\.route\..*logv$`)
	for _, entry := range entries {
		if logPattern.MatchString(entry.Name()) {
			logs = append(logs, entry.Name())
		}
	}

	if len(logs) == 0 {
		return nil, nil
	}

	// 按数字后缀排序取最大
	sort.Slice(logs, func(i, j int) bool {
		re := regexp.MustCompile(`50\.route\.(\d+)\.log`)
		mi := re.FindStringSubmatch(logs[i])
		mj := re.FindStringSubmatch(logs[j])
		if len(mi) >= 2 && len(mj) >= 2 {
			ni, _ := strconv.Atoi(mi[1])
			nj, _ := strconv.Atoi(mj[1])
			return ni < nj
		}
		return logs[i] < logs[j]
	})

	latestLog := filepath.Join(logDir, logs[len(logs)-1])
	result, err := p.extractDRC(ctx, latestLog, modulePath, version, moduleName)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// extractDRC 从单个 route 日志中提取 DRC 数据
func (p *DRCParser) extractDRC(ctx context.Context, filepath_ string, modulePath, version, moduleName string) (map[string]*models.DRCTotal, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	flag := 0          // 是否处于 Start Fix Iteration 区间
	trustable := 0     // 是否遇到 saveDesign {version}.enc
	var drcRptLines []string

	res := &models.DRCTotal{}
	var drcBlock []string
	inDRCBlock := false
	var shortBlock []string
	inShortBlock := false

	f, err := os.Open(filepath_)
	if err != nil {
		return nil, fmt.Errorf("打开日志文件失败: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 10*1024*1024) // 10MB 缓冲区以应对长行

	for scanner.Scan() {
		line := scanner.Text()
		stripped := strings.TrimSpace(line)

		if strings.Contains(line, "Start Fix Iteration") {
			flag = 1
			drcRptLines = nil
		}
		if strings.Contains(line, "Complete Detail Routing") {
			flag = 0
		}
		if strings.Contains(line, "saveDesign") && strings.Contains(line, version+".enc") {
			trustable = 1
		}
		if flag == 1 {
			drcRptLines = append(drcRptLines, line)
		}

		// 抓取 DRC 违例数字 Block
		if strings.Contains(stripped, "#Total number of DRC violations =") {
			drcBlock = []string{stripped}
			inDRCBlock = true
		} else if inDRCBlock {
			drcBlock = append(drcBlock, stripped)
			if strings.Contains(stripped, "Total number of violations on LAYER T2M2") {
				inDRCBlock = false
			}
		}

		// 抓取 Short 违例 Block
		if strings.Contains(stripped, "By Layer and Type") {
			shortBlock = nil
			inShortBlock = true
			continue
		}
		if inShortBlock {
			if strings.Contains(stripped, "#cpu time") || strings.Contains(stripped, "Complete Detail Routing") {
				inShortBlock = false
			} else if strings.Contains(stripped, "By Non-Default Rule") {
				if len(shortBlock) > 0 {
					shortBlock = shortBlock[:len(shortBlock)-1]
				}
				inShortBlock = false
			} else if stripped != "" {
				shortBlock = append(shortBlock, stripped)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Printf("[DRCParser] 扫描日志出错: %v\n", err)
	}

	// 写出 drc.rpt（如果可信）
	if trustable == 1 && len(drcRptLines) > 0 {
		outDir := filepath.Join(modulePath, "rpt", version, fmt.Sprintf("%s.50.route.timeDesign.dir", moduleName))
		os.MkdirAll(outDir, 0755)
		outFile := filepath.Join(outDir, "drc.rpt")
		if err := writeLines(outFile, drcRptLines); err != nil {
			fmt.Printf("[DRCParser Warning] 写入 drc.rpt 失败: %v\n", err)
		}
	}

	// 解析 DRC 的 dp 和 q 数据
	if len(drcBlock) > 0 {
		firstLineVal := strings.Split(drcBlock[0], "=")
		if len(firstLineVal) >= 2 {
			maxDR, _ := strconv.Atoi(strings.TrimSpace(firstLineVal[1]))
			res.ECODRCMax = maxDR

			if maxDR == 0 {
				res.ECODRCDp = 0
				res.ECODRCQ = 0
			} else {
				dpSum := 0
				targetLayers := []string{"LAYER M1 =", "LAYER M2 =", "LAYER M3 =", "LAYER M4 ="}
				for _, lineToCheck := range drcBlock {
					for _, layerKey := range targetLayers {
						if strings.Contains(lineToCheck, layerKey) {
							parts := strings.Split(lineToCheck, "=")
							if len(parts) >= 2 {
								valStr := strings.TrimSpace(parts[1])
								if v, err := strconv.Atoi(valStr); err == nil {
									dpSum += v
								}
							}
						}
					}
				}
				res.ECODRCDp = dpSum
				res.ECODRCQ = maxDR - dpSum
			}
		}
	}

	// 解析 Short 的 dp 和 q 数据
	if len(shortBlock) >= 2 {
		header := shortBlock[0]
		parts := strings.Split(header, "#")
		headerParts := strings.Fields(parts[len(parts)-1])

		shortIdx := -1
		for i, p := range headerParts {
			if p == "Short" {
				shortIdx = i
				break
			}
		}

		if shortIdx >= 0 {
			nu := 0
			totalShort := 0
			targetLayers := map[string]bool{"M1": true, "M2": true, "M3": true, "M4": true}

			for _, row := range shortBlock {
				if !strings.Contains(row, "#") {
					continue
				}
				rowParts := strings.Split(row, "#")
				rowFields := strings.Fields(rowParts[len(rowParts)-1])
				if len(rowFields) == 0 {
					continue
				}

				layerName := rowFields[0]
				if targetLayers[layerName] && len(rowFields) > shortIdx+1 {
					if v, err := strconv.Atoi(rowFields[shortIdx+1]); err == nil {
						nu += v
					}
				}
				if layerName == "Totals" && len(rowFields) > shortIdx+1 {
					if v, err := strconv.Atoi(rowFields[shortIdx+1]); err == nil {
						totalShort = v
					}
				}
			}
			res.ECOShortDp = nu
			res.ECOShortQ = totalShort - nu
		}
	}

	return map[string]*models.DRCTotal{"50": res}, nil
}

// writeLines 将字符串切片写入文件
func writeLines(path string, lines []string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	for _, line := range lines {
		fmt.Fprintln(w, line)
	}
	return w.Flush()
}

// containsStage 判断阶段列表中是否包含某阶段号
func containsStage(stages []string, stage string) bool {
	for _, s := range stages {
		if s == stage {
			return true
		}
	}
	return false
}

var _ models.Parser = (*DRCParser)(nil)
