package backend

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"apr-tracker/internal/models"
)

// TimingParser 解析 APR 关键时序信息（WNS / TNS / NVP）
type TimingParser struct {
	cfg *models.AppConfig
}

// NewTimingParser 构造函数
func NewTimingParser(cfg *models.AppConfig) *TimingParser {
	return &TimingParser{cfg: cfg}
}

// Name 返回解析器名称
func (p *TimingParser) Name() string { return "TimingParser" }

// Parse 执行时序解析
func (p *TimingParser) Parse(ctx context.Context, modulePath, version, moduleName string, stagesFound []string) (interface{}, error) {
	// 只关注关键时序阶段: 10.initial, 30.place, 40.cts, 50.route
	targetStages := map[string]string{
		"10": "10.initial",
		"30": "30.place",
		"40": "40.cts",
		"50": "50.route",
	}

	rptBase := filepath.Join(modulePath, "rpt", version)
	result := make(map[string]*models.StageTimingData)

	for stageNum, stageLabel := range targetStages {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		dirName := fmt.Sprintf("%s.%s.timeDesign.dir", moduleName, stageLabel)
		fullDir := filepath.Join(rptBase, dirName)
		fileName := fmt.Sprintf("%s.%s.summary", moduleName, stageLabel)
		fullPath := filepath.Join(fullDir, fileName)

		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			continue
		}

		stepData, err := p.parseSummary(ctx, fullPath)
		if err != nil {
			fmt.Printf("[TimingParser] 解析 %s 失败: %v\n", fullPath, err)
			continue
		}
		if stepData == nil {
			continue
		}

		// 扫描收集 GIF 图片
		gifs := p.scanGIFs(fullDir)
		stepData.Images = gifs

		// 图片备份逻辑
		if p.cfg.BackupImages && p.cfg.ImageBackupPath != "" && len(gifs) > 0 {
			backupDir := filepath.Join(p.cfg.ImageBackupPath, moduleName, version, stageLabel)
			if err := os.MkdirAll(backupDir, 0755); err == nil {
				backedUp := 0
				for _, gif := range gifs {
					src := filepath.Join(fullDir, gif)
					if copyFile(src, filepath.Join(backupDir, gif)) == nil {
						backedUp++
					}
				}
				if backedUp > 0 {
					stepData.AbsDirPath = backupDir
					fmt.Printf("[TimingParser] (%s-%s) 备份 %d 张图片到 %s\n", moduleName, stageLabel, backedUp, backupDir)
				} else {
					stepData.AbsDirPath = fullDir
				}
			} else {
				stepData.AbsDirPath = fullDir
			}
		} else {
			stepData.AbsDirPath = fullDir
		}

		result[stageLabel] = stepData
		_ = stageNum // suppress unused warning
	}

	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

// parseSummary 解析单个 .summary 文件（流式读取）
func (p *TimingParser) parseSummary(ctx context.Context, filepath_ string) (*models.StageTimingData, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	data := &models.StageTimingData{
		Density:    "N/A",
		Congestion: "N/A",
		Groups:     make(map[string]*models.TimingGroup),
	}

	groupMap := make(map[string]string) // ID → Group Name
	mapFlag := false

	var groups []string
	var wnsList []string
	var tnsList []string
	var nvpList []string

	f, err := os.Open(filepath_)
	if err != nil {
		return nil, fmt.Errorf("打开文件失败: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// 增大缓冲区以处理长行
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	idMappingRe := regexp.MustCompile(`^\|.*\|.*\|$`)

	for scanner.Scan() {
		lineStr := strings.TrimSpace(scanner.Text())
		if lineStr == "" {
			continue
		}

		// 1. ID Number 映射表区域
		if strings.Contains(lineStr, "ID Number") {
			mapFlag = true
			continue
		}
		if strings.Contains(lineStr, "Setup mode") && mapFlag {
			mapFlag = false
		}

		if mapFlag && idMappingRe.MatchString(lineStr) {
			spl := strings.Split(lineStr, "|")
			// 格式: | 1 | reg2reg |
			cleaned := make([]string, 0, len(spl))
			for _, s := range spl {
				s = strings.TrimSpace(s)
				if s != "" {
					cleaned = append(cleaned, s)
				}
			}
			if len(cleaned) >= 2 {
				if _, err := strconv.Atoi(cleaned[0]); err == nil {
					groupMap[cleaned[0]] = cleaned[1]
				}
			}
		}

		// 2. 提取 Setup mode 行（组名）
		if strings.Contains(lineStr, "Setup mode") && !mapFlag {
			// 替换 Group ID 为 Group Name
			for k, v := range groupMap {
				re := regexp.MustCompile(`\|\s*` + regexp.QuoteMeta(k) + `:[^|]*\|`)
				lineStr = re.ReplaceAllString(lineStr, "| "+v+" |")
			}
			parts := splitTableRow(lineStr)
			if len(parts) > 1 {
				groups = parts[1:]
			}
		} else if strings.Contains(lineStr, "WNS (ns)") {
			parts := splitTableRow(lineStr)
			if len(parts) > 1 {
				wnsList = parts[1:]
			}
		} else if strings.Contains(lineStr, "TNS (ns)") {
			parts := splitTableRow(lineStr)
			if len(parts) > 1 {
				tnsList = parts[1:]
			}
		} else if strings.Contains(lineStr, "Violating Paths") {
			parts := splitTableRow(lineStr)
			if len(parts) > 1 {
				nvpList = parts[1:]
			}
		} else if strings.Contains(lineStr, "Density:") {
			re := regexp.MustCompile(`Density:\s*(.*)`)
			if m := re.FindStringSubmatch(lineStr); m != nil {
				data.Density = strings.TrimSpace(m[1])
			}
		} else if strings.Contains(lineStr, "Routing Overflow:") {
			re := regexp.MustCompile(`Routing Overflow:\s*(.*)`)
			if m := re.FindStringSubmatch(lineStr); m != nil {
				data.Congestion = strings.TrimSpace(m[1])
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("扫描文件出错: %w", err)
	}

	// 3. 组装分组数据
	for i, groupName := range groups {
		tg := &models.TimingGroup{
			WNS: safeIndex(wnsList, i, "N/A"),
			TNS: safeIndex(tnsList, i, "N/A"),
			NVP: safeIndex(nvpList, i, "N/A"),
		}
		data.Groups[groupName] = tg
	}

	// 4. 计算 reg2reg_summary（聚合所有 reg2reg 家族分组）
	worstWNS := math.Inf(1)
	totalTNS := 0.0
	totalNVP := 0
	hasReg2Reg := false

	for k, v := range data.Groups {
		if !strings.HasPrefix(k, "reg2reg") {
			continue
		}
		hasReg2Reg = true

		cWNS := parseFloatSafe(v.WNS)
		cTNS := parseFloatSafe(v.TNS)
		cNVP := parseIntSafe(v.NVP)

		if cWNS < worstWNS {
			worstWNS = cWNS
		}
		if cTNS < 0 {
			totalTNS += cTNS
		}
		totalNVP += cNVP
	}

	if hasReg2Reg {
		if math.IsInf(worstWNS, 1) {
			worstWNS = 0.0
		}
		data.Reg2RegSummary = &models.Reg2RegSummary{
			WNS: fmt.Sprintf("%.3f", worstWNS),
			TNS: fmt.Sprintf("%.3f", totalTNS),
			NVP: strconv.Itoa(totalNVP),
		}
	}

	return data, nil
}

// scanGIFs 扫描目录下的 GIF 文件
func (p *TimingParser) scanGIFs(dir string) []string {
	var gifs []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		return gifs
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".gif") {
			gifs = append(gifs, entry.Name())
		}
	}
	return gifs
}

// ---------- 辅助函数 ----------

// splitTableRow 按 | 拆分表格行，返回非空 trim 后的字符串切片
func splitTableRow(line string) []string {
	parts := strings.Split(line, "|")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// safeIndex 安全取数组元素，越界返回默认值
func safeIndex(arr []string, i int, def string) string {
	if i < len(arr) {
		return arr[i]
	}
	return def
}

// parseFloatSafe 安全解析浮点数
func parseFloatSafe(s string) float64 {
	if s == "N/A" || s == "" {
		return 0.0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0.0
	}
	return v
}

// parseIntSafe 安全解析整数
func parseIntSafe(s string) int {
	if s == "N/A" || s == "" {
		return 0
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return v
}

// copyFile 复制文件（用于图片备份），保留权限
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, info.Mode())
}

// 确保实现接口
var _ models.Parser = (*TimingParser)(nil)

// 辅助：检查目录是否存在
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// 辅助：检查文件是否存在
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// FileExists 导出版本
var FileExists = fileExists

// DirExists 导出版本
var DirExists = dirExists

// 抑制未使用的 import
var _ = fs.ErrNotExist
