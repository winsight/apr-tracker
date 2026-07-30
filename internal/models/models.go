// Package models 定义所有层之间通信的核心数据结构与接口。
// 作为 Clean Architecture 中的 DTO（数据传输对象），严禁包含业务逻辑。
package models

import (
	"context"
	"encoding/json"
	"time"
)

// ---------- 配置相关结构体 ----------

// OwnerModulesConfig 表示 YAML 中 owner_modules 的映射
type OwnerModulesConfig map[string][]string

// AppConfig 全局配置对象，与 config.yaml 结构一一对应
type AppConfig struct {
	UserRootDir    string                 `yaml:"user_root_dir"`
	OwnerModules   OwnerModulesConfig     `yaml:"owner_modules"`
	EnabledParsers []string               `yaml:"enabled_parsers"`
	CareFlag       string                 `yaml:"care_flag"`

	LatencyTargetCorner string                 `yaml:"latency_target_corner"`
	LatencySkewPattern  map[string]interface{} `yaml:"latency_skew_pattern"`

	RuntimeParseMode map[string]string `yaml:"runtime_parse_mode"`

	RunParsersMode string `yaml:"run_parsers_mode"`

	BackupImages    bool   `yaml:"backup_images"`
	ImageBackupPath string `yaml:"image_backup_path"`
}

// ---------- 解析结果模型 ----------

// TimingGroup 单个时序分组（reg2reg / in2reg / reg2out 等）的 WNS/TNS/NVP
type TimingGroup struct {
	WNS string `json:"wns"`
	TNS string `json:"tns"`
	NVP string `json:"nvp"`
}

// Reg2RegSummary 所有 reg2reg 家族分组聚合后的汇总
type Reg2RegSummary struct {
	WNS string `json:"wns"`
	TNS string `json:"tns"`
	NVP string `json:"nvp"`
}

// StageTimingData 一个阶段（如 10.initial）的完整时序数据
// 使用自定义 JSON 序列化以扁平化 Groups 字段
type StageTimingData struct {
	Density        string          `json:"density"`
	Congestion     string          `json:"congestion"`
	Images         []string        `json:"images"`
	AbsDirPath     string          `json:"abs_dir_path"`
	Reg2RegSummary *Reg2RegSummary `json:"reg2reg_summary,omitempty"`
	Groups         map[string]*TimingGroup
}

// MarshalJSON 自定义序列化：将 Groups 扁平化到顶层
func (s *StageTimingData) MarshalJSON() ([]byte, error) {
	m := make(map[string]interface{})
	m["density"] = s.Density
	m["congestion"] = s.Congestion
	m["images"] = s.Images
	m["abs_dir_path"] = s.AbsDirPath
	if s.Reg2RegSummary != nil {
		m["reg2reg_summary"] = s.Reg2RegSummary
	}
	for k, v := range s.Groups {
		m[k] = v
	}
	return json.Marshal(m)
}

// DRCTotal 50.route 阶段的 DRC 违例汇总
type DRCTotal struct {
	ECODRCMax  int `json:"eco_drc_max"`
	ECODRCDp   int `json:"eco_drc_dp"`
	ECODRCQ    int `json:"eco_drc_q"`
	ECOShortDp int `json:"eco_short_dp"`
	ECOShortQ  int `json:"eco_short_q"`
}

// LatencyEntry 单条时钟延迟条目
type LatencyEntry struct {
	Domain string `json:"domain"`
	Group  string `json:"group"`
	Min    string `json:"min"`
	Max    string `json:"max"`
	Avg    string `json:"avg"`
	SkewW  string `json:"skew_w"`
}

// LatencyReport 时钟延迟报告（standard + cluster 两类）
type LatencyReport struct {
	Standard []LatencyEntry `json:"standard,omitempty"`
	Cluster  []LatencyEntry `json:"cluster,omitempty"`
}

// VTUsageEntry 单种 VT 类型的用量数据
type VTUsageEntry struct {
	Area      string `json:"area"`
	AreaPerc  string `json:"area_perc"`
	Count     string `json:"count"`
	CountPerc string `json:"count_perc"`
}

// CellUsageReport 单个阶段的 cell / VT 用量报告
type CellUsageReport map[string]*VTUsageEntry

// RuntimeReport 各阶段的运行耗时
type RuntimeReport map[string]string

// ---------- 数据库记录 ----------

// VersionRecord 一行版本记录，对应 SQLite 中的一条数据
type VersionRecord struct {
	Module        string                       `json:"module"`
	Version       string                       `json:"version"`
	StagesFound   []string                     `json:"stages_found"`
	Timing        map[string]*StageTimingData  `json:"timing,omitempty"`
	DRC           map[string]*DRCTotal         `json:"drc,omitempty"`
	Latency       *LatencyReport               `json:"latency,omitempty"`
	Runtime       RuntimeReport                `json:"runtime,omitempty"`
	CellUsage     map[string]CellUsageReport   `json:"cellusage,omitempty"`
	Note          string                       `json:"note"`
	ParentVersion string                       `json:"parent_version"`
	CreatedAt     time.Time                    `json:"created_at"`
	UpdatedAt     time.Time                    `json:"updated_at"`
}

// ---------- Parser 接口 ----------

// Parser 所有解析器的统一接口
type Parser interface {
	// Name 返回解析器名称
	Name() string
	// Parse 执行解析，返回 JSON 可序列化的结果或 nil
	// ctx: 上下文，用于超时控制与取消操作
	// modulePath: /home/{user}/Documents/Proj/{owner}/{module}/
	// version: 版本名（如 V1）
	// moduleName: 模块名（如 LEON）
	// stagesFound: 该版本下已发现的阶段列表（如 ["10", "20", "30", "40", "50"]）
	Parse(ctx context.Context, modulePath, version, moduleName string, stagesFound []string) (interface{}, error)
}

// ---------- API 请求/响应 ----------

// RunParseRequest 前端发起解析请求
type RunParseRequest struct {
	Module string `json:"module"`
}

// DeleteHistoryRequest 删除版本记录请求
type DeleteHistoryRequest struct {
	Module  string `json:"module"`
	Version string `json:"version"`
}

// UpdateMetaRequest 更新备注与父版本
type UpdateMetaRequest struct {
	Module        string `json:"module"`
	Version       string `json:"version"`
	Note          string `json:"note"`
	ParentVersion string `json:"parent_version"`
}

// APIResponse 统一 API 响应封装
type APIResponse struct {
	Code int         `json:"code"`
	Msg  string      `json:"msg,omitempty"`
	Data interface{} `json:"data,omitempty"`
}
