// Package config 负责加载与校验 config.yaml 配置。
package config

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed config.default.yaml
var defaultConfigTemplate []byte

// Config 顶层配置。
type Config struct {
	Server    ServerConfig    `yaml:"server" json:"server"`
	Quake     QuakeConfig     `yaml:"quake" json:"quake"`
	Schedule  ScheduleConfig  `yaml:"schedule" json:"schedule"`
	Storage   StorageConfig   `yaml:"storage" json:"storage"`
	Blacklist BlacklistConfig `yaml:"blacklist" json:"blacklist"`
}

// ServerConfig 服务监听与超时配置。
type ServerConfig struct {
	ListenIP        string `yaml:"listen_ip" json:"listen_ip"`
	Port            int    `yaml:"port" json:"port"`
	ReadTimeoutSec  int    `yaml:"read_timeout_sec" json:"read_timeout_sec"`
	WriteTimeoutSec int    `yaml:"write_timeout_sec" json:"write_timeout_sec"`
	APIKey          string `yaml:"api_key" json:"-"`         // 可选：非空则启用 Bearer 鉴权
	LogFile         string `yaml:"log_file" json:"log_file"` // 结构化日志落盘路径（控制台仅输出关键生命周期信息）
}

// QuakeConfig Quake 探测相关配置。
type QuakeConfig struct {
	BaseURL            string      `yaml:"base_url" json:"base_url"`
	PageSize           int         `yaml:"page_size" json:"page_size"`
	StartPage          int         `yaml:"start_page" json:"start_page"`
	MaxPages           int         `yaml:"max_pages" json:"max_pages"`
	RequestIntervalMs  int         `yaml:"request_interval_ms" json:"request_interval_ms"`
	TimeoutSec         int         `yaml:"timeout_sec" json:"timeout_sec"`
	MaxRetries         int         `yaml:"max_retries" json:"max_retries"`
	PointBudgetPerTask int         `yaml:"point_budget_per_task" json:"point_budget_per_task"`
	UseScroll          bool        `yaml:"use_scroll" json:"use_scroll"`
	APIs               []APIConfig `yaml:"apis" json:"apis"`
}

// APIConfig 配置文件中静态声明的 API Token（会同步导入数据库）。
type APIConfig struct {
	Name    string `yaml:"name" json:"name"`
	Token   string `yaml:"token" json:"-"`
	Enabled bool   `yaml:"enabled" json:"enabled"`
}

// ScheduleConfig 定时调度配置。
type ScheduleConfig struct {
	MonthlyWindowHours int    `yaml:"monthly_window_hours" json:"monthly_window_hours"`
	Timezone           string `yaml:"timezone" json:"timezone"`
	Enabled            bool   `yaml:"enabled" json:"enabled"`
}

// StorageConfig 存储路径配置。
type StorageConfig struct {
	DBPath    string `yaml:"db_path" json:"db_path"`
	OutputDir string `yaml:"output_dir" json:"output_dir"`
	InputDir  string `yaml:"input_dir" json:"input_dir"`
}

// BlacklistConfig 配置文件中的静态黑名单（会合并进匹配器并导入数据库）。
type BlacklistConfig struct {
	Keywords []string `yaml:"keywords" json:"keywords"`
	Regexes  []string `yaml:"regexes" json:"regexes"`
}

// Default 返回带默认值的配置。
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			ListenIP:        "127.0.0.1",
			Port:            8360,
			ReadTimeoutSec:  30,
			WriteTimeoutSec: 60,
			LogFile:         "./data/kongtan.log",
		},
		Quake: QuakeConfig{
			BaseURL:            "https://quake.360.net/api/v3/search/quake_service",
			PageSize:           1000,
			StartPage:          1,
			MaxPages:           50,
			RequestIntervalMs:  300,
			TimeoutSec:         60,
			MaxRetries:         2,
			PointBudgetPerTask: 50000,
			UseScroll:          false,
		},
		Schedule: ScheduleConfig{
			MonthlyWindowHours: 6,
			Timezone:           "Asia/Shanghai",
			Enabled:            true,
		},
		Storage: StorageConfig{
			DBPath:    "./data/kongtan.db",
			OutputDir: "./output",
			InputDir:  "./data/input",
		},
		Blacklist: BlacklistConfig{
			Keywords: []string{},
			Regexes:  []string{},
		},
	}
}

// Load 从 path 读取 YAML，合并默认值并做基本校验。
func Load(path string) (*Config, error) {
	cfg := Default()

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, fmt.Errorf("配置文件不存在: %s（请先创建 config.yaml）", path)
		}
		return nil, fmt.Errorf("读取配置失败: %w", err)
	}
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}

	// 页面固定为 page 翻页，禁止滚动查询。
	cfg.Quake.UseScroll = false
	// 兼容旧配置：日志路径缺省时回退默认。
	if cfg.Server.LogFile == "" {
		cfg.Server.LogFile = "./data/kongtan.log"
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// EnsureFile 若 path 不存在，写入内置默认配置模板；返回 (是否新建, 绝对路径, error)。
// 若文件已存在则不覆盖。
func EnsureFile(path string) (created bool, absPath string, err error) {
	absPath, err = filepath.Abs(path)
	if err != nil {
		return false, "", fmt.Errorf("解析配置路径失败: %w", err)
	}
	if _, err := os.Stat(absPath); err == nil {
		return false, absPath, nil
	} else if !os.IsNotExist(err) {
		return false, absPath, err
	}
	if dir := filepath.Dir(absPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return false, absPath, fmt.Errorf("创建配置目录失败: %w", err)
		}
	}
	if err := os.WriteFile(absPath, defaultConfigTemplate, 0o600); err != nil {
		return false, absPath, fmt.Errorf("写出配置模板失败: %w", err)
	}
	return true, absPath, nil
}

func (c *Config) validate() error {
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port 非法: %d", c.Server.Port)
	}
	if c.Quake.PageSize <= 0 || c.Quake.PageSize > 1000 {
		return fmt.Errorf("quake.page_size 必须在 1~1000 之间: %d", c.Quake.PageSize)
	}
	if c.Quake.StartPage < 1 {
		return fmt.Errorf("quake.start_page 必须 >= 1: %d", c.Quake.StartPage)
	}
	if c.Quake.MaxPages <= 0 {
		return fmt.Errorf("quake.max_pages 必须 > 0")
	}
	if c.Quake.PointBudgetPerTask <= 0 {
		return fmt.Errorf("quake.point_budget_per_task 必须 > 0")
	}
	if c.Quake.BaseURL == "" {
		return fmt.Errorf("quake.base_url 不能为空")
	}
	if c.Schedule.MonthlyWindowHours <= 0 || c.Schedule.MonthlyWindowHours > 24*31 {
		return fmt.Errorf("schedule.monthly_window_hours 非法: %d", c.Schedule.MonthlyWindowHours)
	}
	return nil
}

// ListenAddr 返回监听地址。
func (c *Config) ListenAddr() string {
	return fmt.Sprintf("%s:%d", c.Server.ListenIP, c.Server.Port)
}

// Masked 返回脱敏后的配置副本（Token 全部打码，供配置查看页使用）。
func (c *Config) Masked() map[string]any {
	masked := *c
	masked.Server.APIKey = maskSecret(masked.Server.APIKey)
	apis := make([]APIConfig, len(c.Quake.APIs))
	copy(apis, c.Quake.APIs)
	for i := range apis {
		apis[i].Token = maskSecret(apis[i].Token)
	}
	masked.Quake.APIs = apis
	// 转成 map，便于 JSON 序列化时排除敏感字段。
	out := map[string]any{
		"server":    masked.Server,
		"quake":     masked.Quake,
		"schedule":  masked.Schedule,
		"storage":   masked.Storage,
		"blacklist": masked.Blacklist,
	}
	return out
}

func maskSecret(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "****" + s[len(s)-4:]
}
