package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"kongtan/internal/db"
	"kongtan/internal/model"
	"kongtan/internal/scheduler"
	filesync "kongtan/internal/sync"
)

// ---------- 页面 ----------

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	f, err := s.webFS.Open("index.html")
	if err != nil {
		writeErr(w, http.StatusNotFound, "页面资源缺失")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.Copy(w, f)
}

// ---------- 仪表盘 / 配置 ----------

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	ov := model.Overview{}
	ov.GroupCount, _ = s.store.CountGroups()
	ov.DomainCount, _ = s.store.CountDomains()
	ov.APIKeyCount, _ = s.store.CountAPIKeys()
	ov.APIKeyEnabled, _ = s.store.CountEnabledAPIKeys()
	ov.BlacklistCount, _ = s.store.CountBlacklist()
	ov.TaskCount, _ = s.store.CountTasks()
	ov.URLCount, _ = s.store.CountURLs(db.URLFilter{})
	ov.SubdomainCount, _ = s.store.CountSubdomains(db.SubdomainFilter{})
	now := time.Now()
	ov.MonthPoints, _ = s.store.MonthPoints(now.Year(), int(now.Month()))
	ov.LastTaskAt, _ = s.store.LastTaskAt()
	ov.RunningTasks, ov.SuccessTasks, ov.FailedTasks, ov.PartialTasks, _ = s.store.TaskStatusCounts()
	ov.CreditsTotal, _, ov.CreditsUpdatedAt = s.creditsSnapshot()
	writeOK(w, ov)
}

// ---------- 剩余点数 ----------

type creditsAccount struct {
	Name           string `json:"name"`
	Total          int    `json:"total"`
	MonthRemaining int    `json:"month_remaining"`
	Error          string `json:"error"`
}

func (s *Server) creditsSnapshot() (total, monthRemaining int, updatedAt string) {
	s.creditsMu.Lock()
	defer s.creditsMu.Unlock()
	return s.creditsTotal, s.creditsMonthRemaining, s.creditsUpdatedAt
}

// RefreshCredits 实时查询所有启用账号的剩余点数并更新缓存。
func (s *Server) RefreshCredits() (total, monthRemaining int, accounts []creditsAccount, err error) {
	keys, err := s.store.GetEnabledAPIKeys()
	if err != nil {
		return 0, 0, nil, err
	}
	accounts = make([]creditsAccount, 0, len(keys))
	for _, k := range keys {
		ci, e := s.quakeClient.GetCredit(k.Token)
		if e != nil {
			accounts = append(accounts, creditsAccount{Name: k.Name, Error: e.Error()})
			continue
		}
		total += ci.Total
		monthRemaining += ci.MonthRemaining
		accounts = append(accounts, creditsAccount{Name: k.Name, Total: ci.Total, MonthRemaining: ci.MonthRemaining})
	}

	s.creditsMu.Lock()
	s.creditsTotal = total
	s.creditsMonthRemaining = monthRemaining
	s.creditsUpdatedAt = time.Now().Format(db.TimeLayout)
	s.creditsMu.Unlock()
	return total, monthRemaining, accounts, nil
}

func (s *Server) handleCreditsRefresh(w http.ResponseWriter, r *http.Request) {
	total, monthRemaining, accounts, err := s.RefreshCredits()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeOK(w, map[string]any{
		"total":           total,
		"month_remaining": monthRemaining,
		"accounts":        accounts,
		"updated_at":      time.Now().Format(db.TimeLayout),
	})
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeOK(w, s.cfg.Masked())
}

// ---------- 分组 ----------

type groupStat struct {
	model.Group
	DomainCount int `json:"domain_count"`
}

func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.store.ListGroups()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]groupStat, 0, len(groups))
	for _, g := range groups {
		cnt, _ := s.store.CountDomainsByGroup(g.ID)
		out = append(out, groupStat{Group: g, DomainCount: cnt})
	}
	writeOK(w, out)
}

func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !parseJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeErr(w, http.StatusBadRequest, "分组名称不能为空")
		return
	}
	id, err := s.store.CreateGroup(strings.TrimSpace(req.Name), strings.TrimSpace(req.Description))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = filesync.SyncDomains(s.store, s.cfg.Storage.InputDir)
	g, _ := s.store.GetGroup(id)
	writeOK(w, g)
}

func (s *Server) handleUpdateGroup(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !parseJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeErr(w, http.StatusBadRequest, "分组名称不能为空")
		return
	}
	if err := s.store.UpdateGroup(id, strings.TrimSpace(req.Name), strings.TrimSpace(req.Description)); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = filesync.SyncDomains(s.store, s.cfg.Storage.InputDir)
	g, _ := s.store.GetGroup(id)
	writeOK(w, g)
}

func (s *Server) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if err := s.store.DeleteGroup(id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = filesync.SyncDomains(s.store, s.cfg.Storage.InputDir)
	writeOK(w, map[string]any{"deleted": id})
}

// ---------- 域名 ----------

func (s *Server) handleListGroupDomains(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	domains, err := s.store.ListDomains(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeOK(w, domains)
}

func (s *Server) handleAddDomains(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GroupID int64    `json:"group_id"`
		Domains []string `json:"domains"`
		Domain  string   `json:"domain"`
	}
	if !parseJSON(w, r, &req) {
		return
	}
	if req.GroupID <= 0 {
		writeErr(w, http.StatusBadRequest, "group_id 非法")
		return
	}
	ds := req.Domains
	if len(ds) == 0 && strings.TrimSpace(req.Domain) != "" {
		ds = []string{req.Domain}
	}
	if len(ds) == 0 {
		writeErr(w, http.StatusBadRequest, "域名不能为空")
		return
	}
	added, err := s.store.AddDomains(req.GroupID, ds)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = filesync.SyncDomains(s.store, s.cfg.Storage.InputDir)
	writeOK(w, map[string]any{"added": added})
}

func (s *Server) handleUpdateDomain(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	var req struct {
		Domain  string `json:"domain"`
		Enabled *bool  `json:"enabled"`
	}
	if !parseJSON(w, r, &req) {
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if err := s.store.UpdateDomain(id, req.Domain, enabled); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = filesync.SyncDomains(s.store, s.cfg.Storage.InputDir)
	writeOK(w, map[string]any{"updated": id})
}

func (s *Server) handleDeleteDomain(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if err := s.store.DeleteDomain(id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = filesync.SyncDomains(s.store, s.cfg.Storage.InputDir)
	writeOK(w, map[string]any{"deleted": id})
}

// ---------- API Token ----------

type apiKeyView struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	TokenMasked string `json:"token_masked"`
	SortOrder   int    `json:"sort_order"`
	Enabled     bool   `json:"enabled"`
	CreatedAt   string `json:"created_at"`
}

func toView(k model.APIKey) apiKeyView {
	return apiKeyView{
		ID: k.ID, Name: k.Name, TokenMasked: k.MaskedToken(),
		SortOrder: k.SortOrder, Enabled: k.Enabled, CreatedAt: k.CreatedAt,
	}
}

func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.store.ListAPIKeys()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]apiKeyView, 0, len(keys))
	for _, k := range keys {
		out = append(out, toView(k))
	}
	writeOK(w, out)
}

func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string `json:"name"`
		Token   string `json:"token"`
		Enabled *bool  `json:"enabled"`
	}
	if !parseJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Token) == "" {
		writeErr(w, http.StatusBadRequest, "Token 不能为空")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	cnt, _ := s.store.CountAPIKeys()
	id, err := s.store.CreateAPIKey(strings.TrimSpace(req.Name), strings.TrimSpace(req.Token), cnt, enabled)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = filesync.SyncAPIKeys(s.store, s.cfg.Storage.InputDir)
	k, _ := s.store.GetAPIKey(id)
	writeOK(w, toView(*k))
}

func (s *Server) handleUpdateAPIKey(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	existing, err := s.store.GetAPIKey(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		writeErr(w, http.StatusNotFound, "API 不存在")
		return
	}
	var req struct {
		Name      string `json:"name"`
		Token     string `json:"token"`
		Enabled   *bool  `json:"enabled"`
		SortOrder *int   `json:"sort_order"`
	}
	if !parseJSON(w, r, &req) {
		return
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		token = existing.Token // 未提供则保留原 Token
	}
	enabled := existing.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	sortOrder := existing.SortOrder
	if req.SortOrder != nil {
		sortOrder = *req.SortOrder
	}
	if err := s.store.UpdateAPIKey(id, strings.TrimSpace(req.Name), token, sortOrder, enabled); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = filesync.SyncAPIKeys(s.store, s.cfg.Storage.InputDir)
	k, _ := s.store.GetAPIKey(id)
	writeOK(w, toView(*k))
}

func (s *Server) handleDeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if err := s.store.DeleteAPIKey(id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = filesync.SyncAPIKeys(s.store, s.cfg.Storage.InputDir)
	writeOK(w, map[string]any{"deleted": id})
}

func (s *Server) handleReorderAPIKeys(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if !parseJSON(w, r, &req) {
		return
	}
	if err := s.store.ReorderAPIKeys(req.IDs); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = filesync.SyncAPIKeys(s.store, s.cfg.Storage.InputDir)
	writeOK(w, map[string]any{"reordered": true})
}

func (s *Server) handleTestAPIKey(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	k, err := s.store.GetAPIKey(id)
	if err != nil || k == nil {
		writeErr(w, http.StatusNotFound, "API 不存在")
		return
	}
	if err := s.quakeClient.TestToken(k.Token); err != nil {
		writeOK(w, map[string]any{"valid": false, "error": err.Error()})
		return
	}
	writeOK(w, map[string]any{"valid": true})
}

// ---------- 黑名单 ----------

func (s *Server) handleListBlacklist(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListBlacklist()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeOK(w, items)
}

func (s *Server) handleAddBlacklist(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	}
	if !parseJSON(w, r, &req) {
		return
	}
	if req.Type != model.BlacklistKeyword && req.Type != model.BlacklistRegex {
		writeErr(w, http.StatusBadRequest, "type 必须是 keyword 或 regex")
		return
	}
	if strings.TrimSpace(req.Value) == "" {
		writeErr(w, http.StatusBadRequest, "值不能为空")
		return
	}
	if _, err := s.store.AddBlacklist(req.Type, strings.TrimSpace(req.Value)); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	items, _ := s.store.ListBlacklist()
	writeOK(w, items)
}

func (s *Server) handleDeleteBlacklist(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if err := s.store.DeleteBlacklist(id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeOK(w, map[string]any{"deleted": id})
}

// ---------- 任务 ----------

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	limit, offset := pageParams(r)
	tasks, err := s.store.ListTasks(limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	total, _ := s.store.CountTasks()
	writeOK(w, map[string]any{"items": tasks, "total": total})
}

func (s *Server) handleRunTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GroupID int64 `json:"group_id"`
	}
	if !parseJSON(w, r, &req) {
		return
	}
	if req.GroupID <= 0 {
		writeErr(w, http.StatusBadRequest, "group_id 非法")
		return
	}
	g, err := s.store.GetGroup(req.GroupID)
	if err != nil || g == nil {
		writeErr(w, http.StatusNotFound, "分组不存在")
		return
	}
	go func() {
		if _, err := s.engine.RunGroup(req.GroupID, model.TriggerManual); err != nil {
			s.log.Error("手动执行任务失败", "group_id", req.GroupID, "err", err.Error())
		}
	}()
	writeOK(w, map[string]any{"started": true, "group_id": req.GroupID, "group_name": g.Name})
}

// ---------- 计划任务（只读） ----------

type scheduleTaskSummary struct {
	ID             int64  `json:"id"`
	GroupName      string `json:"group_name"`
	TaskName       string `json:"task_name"`
	Status         string `json:"status"`
	StartedAt      string `json:"started_at"`
	FinishedAt     string `json:"finished_at"`
	DurationSec    int    `json:"duration_sec"`
	ConsumedPoints int    `json:"consumed_points"`
}

func (s *Server) handleSchedule(w http.ResponseWriter, r *http.Request) {
	loc, err := time.LoadLocation(s.cfg.Schedule.Timezone)
	if err != nil {
		loc = time.Local
	}
	now := time.Now()
	nowLocal := now.In(loc)
	monthKey := scheduler.MonthKey(now, loc)

	nextRun := scheduler.NextWindowStart(now, loc, s.cfg.Schedule.MonthlyWindowHours)

	executedAt, _ := s.store.GetScheduleRun(monthKey)
	monthExecuted := executedAt != ""

	var monthTaskID int64
	var monthTaskTime string
	if mt, err := s.store.FirstScheduledTaskInMonth(nowLocal.Year(), int(nowLocal.Month())); err == nil && mt != nil {
		monthTaskID = mt.ID
		monthTaskTime = mt.StartedAt
	}

	history, _ := s.store.ListScheduledTasks(100)
	histOut := make([]scheduleTaskSummary, 0, len(history))
	for _, t := range history {
		histOut = append(histOut, toTaskSummary(t))
	}
	var last *scheduleTaskSummary
	if len(histOut) > 0 {
		last = &histOut[0]
	}

	writeOK(w, map[string]any{
		"enabled":           s.cfg.Schedule.Enabled,
		"window_hours":      s.cfg.Schedule.MonthlyWindowHours,
		"timezone":          s.cfg.Schedule.Timezone,
		"next_run_at":       nextRun.Format("2006-01-02 15:04:05"),
		"month_executed":    monthExecuted,
		"month_executed_at": executedAt,
		"month_task_id":     monthTaskID,
		"month_task_time":   monthTaskTime,
		"last_auto_task":    last,
		"history":           histOut,
	})
}

func (s *Server) handleSchedulerSelfTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cases []struct {
			Time          string `json:"time"`
			Timezone      string `json:"timezone"`
			MonthExecuted bool   `json:"month_executed"`
		} `json:"cases"`
	}
	// 允许空请求体：默认运行内置 8 用例。
	_ = json.NewDecoder(r.Body).Decode(&req)

	if len(req.Cases) > 0 {
		results := make([]scheduler.SelfTestResult, 0, len(req.Cases))
		for _, c := range req.Cases {
			loc := loadLoc(c.Timezone)
			tm := parseVirtualTime(c.Time, loc)
			inW, should, reason := scheduler.Judge(tm, loc, s.cfg.Schedule.MonthlyWindowHours, c.MonthExecuted)
			results = append(results, scheduler.SelfTestResult{
				Name:      c.Time,
				InputTime: tm.In(loc).Format("2006-01-02 15:04:05"),
				Timezone:  loc.String(),
				InWindow:  inW,
				ShouldRun: should,
				Reason:    reason,
			})
		}
		writeOK(w, map[string]any{"cases": results, "all_passed": true})
		return
	}

	results := scheduler.RunSelfTest(s.cfg.Schedule.MonthlyWindowHours)
	allPassed := true
	for _, res := range results {
		if !res.Passed {
			allPassed = false
			break
		}
	}
	writeOK(w, map[string]any{"cases": results, "all_passed": allPassed})
}

func toTaskSummary(t model.Task) scheduleTaskSummary {
	return scheduleTaskSummary{
		ID:             t.ID,
		GroupName:      t.GroupName,
		TaskName:       t.TaskName,
		Status:         t.Status,
		StartedAt:      t.StartedAt,
		FinishedAt:     t.FinishedAt,
		DurationSec:    durationSec(t.StartedAt, t.FinishedAt),
		ConsumedPoints: t.ConsumedPoints,
	}
}

func durationSec(start, end string) int {
	if start == "" || end == "" {
		return 0
	}
	st, err1 := time.ParseInLocation(db.TimeLayout, start, time.Local)
	en, err2 := time.ParseInLocation(db.TimeLayout, end, time.Local)
	if err1 != nil || err2 != nil {
		return 0
	}
	d := en.Sub(st)
	if d < 0 {
		d = 0
	}
	return int(d.Seconds())
}

func loadLoc(name string) *time.Location {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Asia/Shanghai"
	}
	if loc, err := time.LoadLocation(name); err == nil {
		return loc
	}
	return time.Local
}

func parseVirtualTime(s string, loc *time.Location) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Now()
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", s, loc); err == nil {
		return t
	}
	return time.Now()
}

// ---------- 数据查看 ----------

func (s *Server) handleListURLs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := db.URLFilter{
		GroupName: q.Get("group"),
		TaskID:    intParam(q, "task_id"),
		Domain:    q.Get("domain"),
		Host:      q.Get("host"),
		ICP:       q.Get("icp"),
		Query:     q.Get("q"),
		Limit:     intParamDefault(q, "limit", 100),
		Offset:    intParamDefault(q, "offset", 0),
		Dedup:     true, // 数据查看：以 url 为索引去重，保留最新记录
	}
	items, err := s.store.ListURLs(f)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	total, _ := s.store.CountURLs(f)
	writeOK(w, map[string]any{"items": items, "total": total})
}

func (s *Server) handleListSubdomains(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := db.SubdomainFilter{
		GroupName: q.Get("group"),
		TaskID:    intParam(q, "task_id"),
		Domain:    q.Get("domain"),
		Host:      q.Get("host"),
		ICP:       q.Get("icp"),
		Query:     q.Get("q"),
		Limit:     intParamDefault(q, "limit", 100),
		Offset:    intParamDefault(q, "offset", 0),
		Dedup:     true, // 数据查看：以 subdomain 为索引去重，保留最新记录
	}
	items, err := s.store.ListSubdomains(f)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	total, _ := s.store.CountSubdomains(f)
	writeOK(w, map[string]any{"items": items, "total": total})
}

// ---------- CSV 下载 ----------

func (s *Server) handleExportURLs(w http.ResponseWriter, r *http.Request) {
	taskID := intParam(r.URL.Query(), "task_id")
	task, _ := s.store.GetTask(taskID)
	b, err := s.engine.ExportURLsCSVBytes(taskID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	name := fmt.Sprintf("urls_%d.csv", taskID)
	if task != nil {
		name = task.TaskName + "_urls.csv"
	}
	writeCSVAttachment(w, name, b)
}

func (s *Server) handleExportSubdomains(w http.ResponseWriter, r *http.Request) {
	taskID := intParam(r.URL.Query(), "task_id")
	task, _ := s.store.GetTask(taskID)
	b, err := s.engine.ExportSubdomainsCSVBytes(taskID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	name := fmt.Sprintf("subdomains_%d.csv", taskID)
	if task != nil {
		name = task.TaskName + "_subdomains.csv"
	}
	writeCSVAttachment(w, name, b)
}

func writeCSVAttachment(w http.ResponseWriter, name string, b []byte) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+name+"\"")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	_, _ = w.Write(b)
}

// ---------- 工具 ----------

func pageParams(r *http.Request) (limit, offset int) {
	q := r.URL.Query()
	limit = intParamDefault(q, "limit", 50)
	offset = intParamDefault(q, "offset", 0)
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	return limit, offset
}

func intParam(q url.Values, key string) int64 {
	v, _ := strconv.ParseInt(q.Get(key), 10, 64)
	return v
}

func intParamDefault(q url.Values, key string, def int) int {
	if v, err := strconv.Atoi(q.Get(key)); err == nil {
		return v
	}
	return def
}
