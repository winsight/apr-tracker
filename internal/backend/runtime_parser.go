package backend

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"apr-tracker/internal/models"
)

// RuntimeParser 解析各流程阶段的耗时信息
type RuntimeParser struct {
	cfg *models.AppConfig
}

// NewRuntimeParser 构造函数
func NewRuntimeParser(cfg *models.AppConfig) *RuntimeParser {
	return &RuntimeParser{cfg: cfg}
}

// Name 返回解析器名称
func (p *RuntimeParser) Name() string { return "RuntimeParser" }

// Parse 执行运行时解析
func (p *RuntimeParser) Parse(ctx context.Context, modulePath, version, moduleName string, stagesFound []string) (interface{}, error) {
	if len(stagesFound) == 0 {
		return nil, nil
	}

	logDir := filepath.Join(modulePath, "log", version)
	if !dirExists(logDir) {
		return nil, nil
	}

	allLogs, err := os.ReadDir(logDir)
	if err != nil {
		return nil, nil
	}

	// 获取当前模块的解析模式
	parseMode := p.getMode(moduleName)

	result := make(models.RuntimeReport)

	for _, stage := range stagesFound {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// 筛选该阶段的日志文件（排除隐藏文件和 .swp）
		var stageLogs []string
		for _, entry := range allLogs {
			name := entry.Name()
			if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".swp") {
				continue
			}
			if strings.Contains(name, stage+".") &&
				strings.Contains(strings.ToLower(name), "log") &&
				!strings.Contains(strings.ToLower(name), ".report.") {
				stageLogs = append(stageLogs, name)
			}
		}

		if len(stageLogs) == 0 {
			continue
		}

		// 按修改时间排序，取最新的
		sort.Slice(stageLogs, func(i, j int) bool {
			infoI, _ := os.Stat(filepath.Join(logDir, stageLogs[i]))
			infoJ, _ := os.Stat(filepath.Join(logDir, stageLogs[j]))
			if infoI == nil || infoJ == nil {
				return false
			}
			return infoI.ModTime().Before(infoJ.ModTime())
		})

		latestLog := filepath.Join(logDir, stageLogs[len(stageLogs)-1])

		var runtimeVal string
		if parseMode == "db_diff" {
			runtimeVal = p.extractRuntimeDBDiff(ctx, latestLog)
			if runtimeVal == "N/A" {
				runtimeVal = p.extractRuntimeLogEnd(latestLog)
			}
		} else {
			runtimeVal = p.extractRuntimeLogEnd(latestLog)
		}

		if runtimeVal != "" {
			// 使用完整阶段标签
			stageLabel, _ := GetStageFullName(ctx, filepath.Join(modulePath, "rpt", version), moduleName, stage)
			if stageLabel == "" {
				stageLabel = stage
			}
			result[stageLabel] = runtimeVal
		}
	}

	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

// extractRuntimeLogEnd 策略1：从日志末尾逆读 real=xxx
func (p *RuntimeParser) extractRuntimeLogEnd(filepath_ string) string {
	fi, err := os.Stat(filepath_)
	if err != nil || fi.Size() == 0 {
		return "N/A"
	}

	f, err := os.Open(filepath_)
	if err != nil {
		return "N/A"
	}
	defer f.Close()

	// 读取末尾 ~4KB
	offset := int64(4096)
	if fi.Size() < offset {
		offset = fi.Size()
	}

	buf := make([]byte, offset)
	f.ReadAt(buf, fi.Size()-offset)

	lines := strings.Split(string(buf), "\n")
	re := regexp.MustCompile(`real=([\d:]+)`)

	// 倒序查找
	for i := len(lines) - 1; i >= 0; i-- {
		if m := re.FindStringSubmatch(lines[i]); m != nil {
			return m[1]
		}
	}

	return "N/A"
}

// extractRuntimeDBDiff 策略2：通过 restoreDesign 和 saveDesign 时间戳计算差值
func (p *RuntimeParser) extractRuntimeDBDiff(ctx context.Context, filepath_ string) string {
	select {
	case <-ctx.Done():
		return "N/A"
	default:
	}

	var firstRestore, lastSave string
	timePattern := regexp.MustCompile(`^\[(\d{2}/\d{2}\s+\d{2}:\d{2}:\d{2})`)
	restorePattern := regexp.MustCompile(`<CMD>\s+restoreDesign`)

	f, err := os.Open(filepath_)
	if err != nil {
		return "N/A"
	}
	defer f.Close()

	// 扫描查找 restoreDesign（文件头部附近，很快就退出）
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 512*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if restorePattern.MatchString(line) {
			if m := timePattern.FindStringSubmatch(line); m != nil {
				firstRestore = m[1]
				break
			}
		}
	}

	// 逆序查找 saveDesign
	savePattern := regexp.MustCompile(`<CMD>\s+saveDesign`)
	fi, err := os.Stat(filepath_)
	if err != nil {
		return "N/A"
	}

	fileSize := fi.Size()
	chunkSize := int64(5 * 1024 * 1024) // 每次读 5MB
	maxRead := int64(500 * 1024 * 1024)  // 最多往前找 500MB

	rf, err := os.Open(filepath_)
	if err != nil {
		return "N/A"
	}
	defer rf.Close()

	offset := fileSize
	bytesRead := int64(0)
	var tailResidue []byte

	for offset > 0 && bytesRead < maxRead {
		readSize := chunkSize
		if offset < chunkSize {
			readSize = offset
		}
		offset -= readSize

		chunk := make([]byte, readSize)
		rf.ReadAt(chunk, offset)
		bytesRead += readSize

		// 拼接上一轮的残留
		textBlock := append(chunk, tailResidue...)
		lines := bytes.Split(textBlock, []byte("\n"))

		if offset > 0 {
			tailResidue = lines[0]
			lines = lines[1:]
		}

		found := false
		for i := len(lines) - 1; i >= 0; i-- {
			if bytes.Contains(lines[i], []byte("<CMD>")) && bytes.Contains(lines[i], []byte("saveDesign")) {
				decodedLine := string(lines[i])
				if savePattern.MatchString(decodedLine) {
					if m := timePattern.FindStringSubmatch(decodedLine); m != nil {
						lastSave = m[1]
						found = true
						break
					}
				}
			}
		}
		if found {
			break
		}
	}

	// 计算时间差
	if firstRestore != "" && lastSave != "" {
		const layout = "01/02 15:04:05"
		tStart, err1 := time.Parse(layout, firstRestore)
		tEnd, err2 := time.Parse(layout, lastSave)
		if err1 != nil || err2 != nil {
			return "N/A"
		}

		delta := tEnd.Sub(tStart)
		if delta < 0 {
			delta += 365 * 24 * time.Hour
		}

		totalSec := int(delta.Seconds())
		return fmt.Sprintf("%d:%02d:%02d", totalSec/3600, (totalSec%3600)/60, totalSec%60)
	}

	return "N/A"
}

// getMode 获取模块的解析模式
func (p *RuntimeParser) getMode(moduleName string) string {
	if mode, ok := p.cfg.RuntimeParseMode[moduleName]; ok {
		return mode
	}
	if mode, ok := p.cfg.RuntimeParseMode["default"]; ok {
		return mode
	}
	return "log_end"
}

var _ models.Parser = (*RuntimeParser)(nil)

// bytes 类型的辅助（避免 import "bytes" 冲突，在 drc_parser 中未使用，此处复用）
func init() {
	_ = strconv.Itoa(0) // 确保 strconv 被使用
}
