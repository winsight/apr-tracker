// Package config 负责加载并解析 YAML 配置文件，生成全局可用的配置对象。
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"apr-tracker/internal/models"
)

// Load 从指定路径加载 config.yaml 并返回配置对象
func Load(path string) (*models.AppConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败 [%s]: %w", path, err)
	}

	var cfg models.AppConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析 YAML 配置失败: %w", err)
	}

	// 设置默认值
	if cfg.RunParsersMode == "" {
		cfg.RunParsersMode = "current"
	}
	if cfg.CareFlag == "" {
		cfg.CareFlag = "normal"
	}

	return &cfg, nil
}

// GetAllModules 从配置中提取所有模块列表（扁平化 owner_modules）
func GetAllModules(cfg *models.AppConfig) []string {
	var modules []string
	for _, mods := range cfg.OwnerModules {
		modules = append(modules, mods...)
	}
	return modules
}

// GetModuleOwner 根据模块名反查 owner
func GetModuleOwner(cfg *models.AppConfig, moduleName string) string {
	for owner, mods := range cfg.OwnerModules {
		for _, m := range mods {
			if m == moduleName {
				return owner
			}
		}
	}
	return ""
}

// ResolveModulePath 解析模块的完整基础路径
// 路径范式: /home/{user_root_dir}/{owner}/*{module}/
// 注意：module 目录可能带有前缀数字（如 20.APR.LEON），需要动态模糊匹配
func ResolveModulePath(cfg *models.AppConfig, owner, moduleName string) (string, error) {
	baseDir := fmt.Sprintf("%s/%s", cfg.UserRootDir, owner)
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return "", fmt.Errorf("无法读取目录 [%s]: %w", baseDir, err)
	}

	// 模糊匹配：查找以数字开头、包含 moduleName 的目录
	var matched string
	var latestModTime int64
	suffix := "." + moduleName

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		// 匹配模式: 数字.xxx.moduleName 或直接 moduleName
		if name == moduleName || (len(name) > len(suffix) && name[len(name)-len(suffix):] == suffix) {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			modTime := info.ModTime().Unix()
			if modTime > latestModTime {
				latestModTime = modTime
				matched = name
			}
		}
	}

	if matched == "" {
		return "", fmt.Errorf("未找到模块 [%s] 在目录 [%s] 下", moduleName, baseDir)
	}

	return fmt.Sprintf("%s/%s/%s", cfg.UserRootDir, owner, matched), nil
}
