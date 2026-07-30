// Package backend 负责耗时的 I/O 和 CPU 密集型计算（如正则解析、文件分块读取）。
// 与 UI 层完全解耦。
package backend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ScanVersions 扫描指定模块路径下的所有版本目录
// 版本目录位于 {modulePath}/rpt/ 下
func ScanVersions(ctx context.Context, modulePath string) ([]string, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	rptDir := filepath.Join(modulePath, "rpt")
	entries, err := os.ReadDir(rptDir)
	if err != nil {
		return nil, fmt.Errorf("无法读取 rpt 目录 [%s]: %w", rptDir, err)
	}

	var versions []string
	for _, entry := range entries {
		if entry.IsDir() {
			versions = append(versions, entry.Name())
		}
	}

	sort.Strings(versions)
	return versions, nil
}

// ScanStages 扫描某版本目录下已完成的流程阶段
// 通过匹配模块名 + 阶段号 + timeDesign.dir 的目录存在性来判断
func ScanStages(ctx context.Context, rptBaseDir, moduleName string) ([]string, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// 关键阶段号（按流程顺序）
	allStages := []string{"10", "20", "30", "40", "50"}

	var found []string
	for _, stageNum := range allStages {
		// 扫描匹配: {module}.{stage_num}.*.timeDesign.dir
		entries, err := os.ReadDir(rptBaseDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			if strings.HasPrefix(name, moduleName+"."+stageNum+".") && strings.Contains(name, "timeDesign.dir") {
				found = append(found, stageNum)
				break
			}
		}
	}

	return found, nil
}

// GetStageFullName 根据阶段号与模块名还原完整的阶段标签（如 10.initial）
func GetStageFullName(ctx context.Context, rptBaseDir, moduleName, stageNum string) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	entries, err := os.ReadDir(rptBaseDir)
	if err != nil {
		return "", err
	}

	prefix := moduleName + "." + stageNum + "."
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, prefix) && strings.Contains(name, "timeDesign.dir") {
			// 提取阶段名: {module}.{stage_num}.{stage_label}.timeDesign.dir → {stage_num}.{stage_label}
			trimmed := strings.TrimPrefix(name, moduleName+".")
			trimmed = strings.TrimSuffix(trimmed, ".timeDesign.dir")
			return trimmed, nil
		}
	}

	return stageNum, nil // 兜底：只返回阶段号
}

// ResolveStageDir 解析某个阶段的 timeDesign.dir 完整路径
func ResolveStageDir(rptBaseDir, moduleName, stageNum string) (string, error) {
	entries, err := os.ReadDir(rptBaseDir)
	if err != nil {
		return "", err
	}

	prefix := moduleName + "." + stageNum + "."
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, prefix) && strings.Contains(name, "timeDesign.dir") {
			return filepath.Join(rptBaseDir, name), nil
		}
	}

	return "", fmt.Errorf("未找到阶段 %s 的 timeDesign.dir", stageNum)
}

// GetModuleNameFromPath 从完整路径中提取模块短名（如 LEON）
// 路径格式：.../20.APR.LEON → LEON
func GetModuleNameFromPath(modulePath string) string {
	base := filepath.Base(modulePath)
	// 查找最后一个点号后的部分
	idx := strings.LastIndex(base, ".")
	if idx >= 0 {
		return base[idx+1:]
	}
	return base
}
