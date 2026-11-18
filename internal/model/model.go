// Package model 定义空探平台的数据模型与状态常量。
package model

// 任务触发类型
const (
	TriggerScheduled = "scheduled" // 定时触发
	TriggerManual    = "manual"    // 手动触发
)

// 任务状态
const (
	StatusPending = "pending"
	StatusRunning = "running"
	StatusSuccess = "success"
	StatusFailed  = "failed"
	StatusPartial = "partial"
)

// 黑名单类型
const (
	BlacklistKeyword = "keyword" // 关键词包含匹配
	BlacklistRegex   = "regex"   // 正则匹配
)

// Group 域名分组。
type Group struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
}

// APIKey 360Quake API Token 记录。
type APIKey struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Token     string `json:"token"`
	SortOrder int    `json:"sort_order"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at"`
}

// MaskedToken 返回脱敏后的 Token（仅保留前后少量字符）。
func (k APIKey) MaskedToken() string {
	t := k.Token
	if len(t) <= 8 {
		if len(t) == 0 {
			return ""
		}
		return "****"
	}
	return t[:4] + "****" + t[len(t)-4:]
}

// Domain 探测域名。
type Domain struct {
	ID        int64  `json:"id"`
	Domain    string `json:"domain"`
	GroupID   int64  `json:"group_id"`
	GroupName string `json:"group_name,omitempty"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at"`
}

// BlacklistEntry 黑名单条目。
type BlacklistEntry struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Value     string `json:"value"`
	CreatedAt string `json:"created_at"`
}

// Task 探测任务。
type Task struct {
	ID             int64  `json:"id"`
	GroupID        int64  `json:"group_id"`
	GroupName      string `json:"group_name"`
	TaskName       string `json:"task_name"`
	TriggerType    string `json:"trigger_type"`
	Status         string `json:"status"`
	StartedAt      string `json:"started_at"`
	FinishedAt     string `json:"finished_at"`
	PageSize       int    `json:"page_size"`
	TotalRecords   int    `json:"total_records"`
	ConsumedPoints int    `json:"consumed_points"`
	ErrorMsg       string `json:"error_msg"`
}

// URLRecord URL 采集记录。
type URLRecord struct {
	ID         int64  `json:"id"`
	TaskID     int64  `json:"task_id"`
	TaskName   string `json:"task_name"`
	GroupName  string `json:"group_name"`
	URL        string `json:"url"`
	Host       string `json:"host"`
	IP         string `json:"ip"`
	Port       int    `json:"port"`
	Title      string `json:"title"`
	Domain     string `json:"domain"`
	ICPLicence string `json:"icp_licence"`
	ICPUnit    string `json:"icp_unit"`
	ICPNature  string `json:"icp_nature"`
	ICPExpired string `json:"icp_expired"`
	ProvinceCN string `json:"province_cn"`
	CityCN     string `json:"city_cn"`
	CountryCN  string `json:"country_cn"`
	ISP        string `json:"isp"`
	ASName     string `json:"asname"`
	SceneCN    string `json:"scene_cn"`
	GPSLat     string `json:"gps_lat"`
	GPSLng     string `json:"gps_lng"`
	RawJSON    string `json:"raw_json"`
	CreatedAt  string `json:"created_at"`
	// TaskStartedAt 关联任务的执行开始时间（联表查询填充，仅展示用）。
	TaskStartedAt string `json:"task_started_at"`
}

// SubdomainRecord 子域名记录。
type SubdomainRecord struct {
	ID         int64  `json:"id"`
	TaskID     int64  `json:"task_id"`
	TaskName   string `json:"task_name"`
	GroupName  string `json:"group_name"`
	Subdomain  string `json:"subdomain"`
	Domain     string `json:"domain"`
	Host       string `json:"host"`
	ICPLicence string `json:"icp_licence"`
	ICPUnit    string `json:"icp_unit"`
	ICPNature  string `json:"icp_nature"`
	ICPExpired string `json:"icp_expired"`
	ProvinceCN string `json:"province_cn"`
	CityCN     string `json:"city_cn"`
	CountryCN  string `json:"country_cn"`
	ISP        string `json:"isp"`
	ASName     string `json:"asname"`
	SceneCN    string `json:"scene_cn"`
	GPSLat     string `json:"gps_lat"`
	GPSLng     string `json:"gps_lng"`
	CreatedAt  string `json:"created_at"`
	// TaskStartedAt 关联任务的执行开始时间（联表查询填充，仅展示用）。
	TaskStartedAt string `json:"task_started_at"`
}

// Overview 仪表盘概览数据。
type Overview struct {
	GroupCount     int    `json:"group_count"`
	DomainCount    int    `json:"domain_count"`
	APIKeyCount    int    `json:"apikey_count"`
	APIKeyEnabled  int    `json:"apikey_enabled"`
	BlacklistCount int    `json:"blacklist_count"`
	TaskCount      int    `json:"task_count"`
	URLCount       int    `json:"url_count"`
	SubdomainCount int    `json:"subdomain_count"`
	MonthPoints    int    `json:"month_points"`
	LastTaskAt     string `json:"last_task_at"`
	RunningTasks   int    `json:"running_tasks"`
	SuccessTasks   int    `json:"success_tasks"`
	FailedTasks    int    `json:"failed_tasks"`
	PartialTasks   int    `json:"partial_tasks"`
	// CreditsTotal 各启用账号剩余点数之和（缓存值，未查询过为 0）。
	CreditsTotal     int    `json:"credits_total"`
	CreditsUpdatedAt string `json:"credits_updated_at"`
}
