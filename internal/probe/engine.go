// Package probe 实现探测任务执行引擎。
//
// 对指定分组的每个域名逐页拉取 Quake 空间资产，完成黑名单过滤、URL/子域名去重、
// ICP 与地理位置抽取、点数预算控制，最后入库并导出 CSV。
package probe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"kongtan/internal/blacklist"
	"kongtan/internal/config"
	"kongtan/internal/db"
	"kongtan/internal/exporter"
	"kongtan/internal/model"
	"kongtan/internal/processor"
	"kongtan/internal/quake"
)

// Engine 探测执行引擎。
type Engine struct {
	store  *db.Store
	client *quake.Client
	cfg    *config.Config
	log    *slog.Logger

	mu      sync.Mutex
	running map[int64]bool // 防止同分组并发执行
}

// New 构造执行引擎。
func New(store *db.Store, client *quake.Client, cfg *config.Config, log *slog.Logger) *Engine {
	return &Engine{store: store, client: client, cfg: cfg, log: log, running: map[int64]bool{}}
}

// accumulator 单任务采集结果累积器。
type accumulator struct {
	urls         []model.URLRecord
	subs         []model.SubdomainRecord
	seenURL      map[string]bool
	seenSub      map[string]bool
	totalRecords int
	consumed     int
	filtered     int
	failedDomain int
	errors       []string
}

// RunGroup 立即执行某个分组的一次探测任务。
func (e *Engine) RunGroup(groupID int64, triggerType string) (*model.Task, error) {
	e.mu.Lock()
	if e.running[groupID] {
		e.mu.Unlock()
		return nil, fmt.Errorf("该分组正在执行中，请勿重复提交")
	}
	e.running[groupID] = true
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.running, groupID)
		e.mu.Unlock()
	}()

	group, err := e.store.GetGroup(groupID)
	if err != nil {
		return nil, err
	}
	if group == nil {
		return nil, fmt.Errorf("分组不存在: %d", groupID)
	}

	tokens, err := e.enabledTokens()
	if err != nil {
		return nil, err
	}
	domains, err := e.store.ListDomains(groupID)
	if err != nil {
		return nil, err
	}

	// 过滤出启用域名。
	var enabledDomains []model.Domain
	for _, d := range domains {
		if d.Enabled {
			enabledDomains = append(enabledDomains, d)
		}
	}

	now := time.Now()
	taskName := fmt.Sprintf("%s_%s", group.Name, now.Format("20060102_150405"))
	task := &model.Task{
		GroupID:     group.ID,
		GroupName:   group.Name,
		TaskName:    taskName,
		TriggerType: triggerType,
		Status:      model.StatusRunning,
		StartedAt:   now.Format(db.TimeLayout),
		PageSize:    e.cfg.Quake.PageSize,
	}
	taskID, err := e.store.CreateTask(task)
	if err != nil {
		return nil, err
	}
	task.ID = taskID

	if e.log != nil {
		e.log.Info("开始探测任务", "task_id", taskID, "task_name", taskName, "group", group.Name,
			"trigger", triggerType, "domains", len(enabledDomains), "tokens", len(tokens))
	}

	if len(tokens) == 0 {
		e.finalize(task, model.StatusFailed, 0, 0, "没有启用的 API Token")
		return task, nil
	}
	if len(enabledDomains) == 0 {
		e.finalize(task, model.StatusFailed, 0, 0, "分组下没有启用的域名")
		return task, nil
	}

	matcher := e.buildMatcher()
	acc := &accumulator{
		seenURL: map[string]bool{},
		seenSub: map[string]bool{},
	}

	budget := e.cfg.Quake.PointBudgetPerTask
	pageSize := e.cfg.Quake.PageSize
	maxPages := e.cfg.Quake.MaxPages
	startPage := e.cfg.Quake.StartPage
	interval := time.Duration(e.cfg.Quake.RequestIntervalMs) * time.Millisecond

	budgetHit := false
outer:
	for _, d := range enabledDomains {
		query := fmt.Sprintf(`domain: "%s"`, d.Domain)
		start := (startPage - 1) * pageSize
		pageNo := 1
		for pageNo <= maxPages {
			data, meta, usedToken, serr := e.client.Search(tokens, query, start, pageSize)
			if serr != nil {
				acc.failedDomain++
				msg := fmt.Sprintf("域名 %s 查询失败: %v", d.Domain, serr)
				acc.errors = append(acc.errors, msg)
				if e.log != nil {
					e.log.Error(msg, "task_id", taskID)
				}
				break
			}

			points := len(data)
			if meta != nil && meta.ConsumedPoints > 0 {
				points = meta.ConsumedPoints
			}
			acc.totalRecords += len(data)
			acc.consumed += points

			if e.log != nil {
				e.log.Info("翻页", "task_id", taskID, "domain", d.Domain, "page", pageNo,
					"返回条数", len(data), "累计点数", acc.consumed, "token", usedToken, "start", start)
			}

			e.ingest(acc, data, taskID, taskName, group.Name, matcher)

			more := len(data) >= pageSize // 是否可能还有下一页
			if acc.consumed >= budget && more {
				// 预算耗尽且尚有更多数据未拉取：停止并标记 partial。
				budgetHit = true
				if e.log != nil {
					e.log.Info("点数预算耗尽，停止任务", "task_id", taskID, "consumed", acc.consumed, "budget", budget)
				}
				break outer
			}
			if !more {
				break
			}
			start += pageSize
			pageNo++
			time.Sleep(interval)
		}
	}

	// 入库。
	if err := e.store.InsertURLs(acc.urls); err != nil {
		return nil, fmt.Errorf("写入 URL 失败: %w", err)
	}
	if err := e.store.InsertSubdomains(acc.subs); err != nil {
		return nil, fmt.Errorf("写入子域名失败: %w", err)
	}

	status := e.determineStatus(acc, budgetHit, len(enabledDomains))
	errMsg := strings.Join(acc.errors, "; ")
	if budgetHit {
		if errMsg != "" {
			errMsg += "; "
		}
		errMsg += fmt.Sprintf("点数预算耗尽(%d/%d)", acc.consumed, budget)
	}
	e.finalize(task, status, acc.totalRecords, acc.consumed, errMsg)

	// 自动导出 CSV（采集入库前 + 导出前双重过滤）。
	e.export(task, matcher)

	if e.log != nil {
		e.log.Info("探测任务结束", "task_id", taskID, "status", status,
			"记录数", acc.totalRecords, "点数", acc.consumed, "URL", len(acc.urls), "子域名", len(acc.subs),
			"黑名单过滤", acc.filtered)
	}
	return task, nil
}

// ingest 处理一批原始记录：过滤 + 抽取 + 去重。
func (e *Engine) ingest(acc *accumulator, data []json.RawMessage, taskID int64, taskName, groupName string, matcher *blacklist.Matcher) {
	now := db.Now()
	for _, raw := range data {
		p := processor.Process(raw)

		// 采集入库前过滤：URL / host / domain 任一命中即丢弃整条记录。
		if !matcher.Empty() && matcher.Match(p.URL, p.Host, p.Domain) {
			acc.filtered++
			continue
		}

		if p.URL != "" && !acc.seenURL[p.URL] {
			acc.seenURL[p.URL] = true
			acc.urls = append(acc.urls, model.URLRecord{
				TaskID: taskID, TaskName: taskName, GroupName: groupName,
				URL: p.URL, Host: p.Host, IP: p.IP, Port: p.Port, Title: p.Title, Domain: p.Domain,
				ICPLicence: p.ICPLicence, ICPUnit: p.ICPUnit, ICPNature: p.ICPNature, ICPExpired: p.ICPExpired,
				ProvinceCN: p.ProvinceCN, CityCN: p.CityCN, CountryCN: p.CountryCN,
				ISP: p.ISP, ASName: p.ASName, SceneCN: p.SceneCN, GPSLat: p.GPSLat, GPSLng: p.GPSLng,
				RawJSON: string(raw), CreatedAt: now,
			})
		}

		if p.Subdomain != "" {
			key := p.Subdomain + "\x00" + p.Domain
			if !acc.seenSub[key] {
				acc.seenSub[key] = true
				acc.subs = append(acc.subs, model.SubdomainRecord{
					TaskID: taskID, TaskName: taskName, GroupName: groupName,
					Subdomain: p.Subdomain, Domain: p.Domain, Host: p.Host,
					ICPLicence: p.ICPLicence, ICPUnit: p.ICPUnit, ICPNature: p.ICPNature, ICPExpired: p.ICPExpired,
					ProvinceCN: p.ProvinceCN, CityCN: p.CityCN, CountryCN: p.CountryCN,
					ISP: p.ISP, ASName: p.ASName, SceneCN: p.SceneCN, GPSLat: p.GPSLat, GPSLng: p.GPSLng,
					CreatedAt: now,
				})
			}
		}
	}
}

// determineStatus 判定任务最终状态。
func (e *Engine) determineStatus(acc *accumulator, budgetHit bool, totalDomains int) string {
	if budgetHit {
		return model.StatusPartial
	}
	if totalDomains > 0 && acc.failedDomain >= totalDomains {
		return model.StatusFailed
	}
	if acc.failedDomain > 0 {
		return model.StatusPartial
	}
	return model.StatusSuccess
}

// finalize 更新任务汇总信息。
func (e *Engine) finalize(task *model.Task, status string, totalRecords, consumed int, errMsg string) {
	task.Status = status
	task.TotalRecords = totalRecords
	task.ConsumedPoints = consumed
	task.ErrorMsg = errMsg
	task.FinishedAt = db.Now()
	if err := e.store.FinalizeTask(task.ID, status, totalRecords, consumed, errMsg); err != nil && e.log != nil {
		e.log.Error("更新任务状态失败", "task_id", task.ID, "err", err.Error())
	}
}

// export 导出 CSV（导出前再次黑名单过滤）。
func (e *Engine) export(task *model.Task, matcher *blacklist.Matcher) {
	urls := make([]model.URLRecord, 0)
	for _, u := range e.urlsByTask(task.ID) {
		if !matcher.Empty() && matcher.Match(u.URL, u.Host, u.Domain) {
			continue
		}
		urls = append(urls, u)
	}
	subs := make([]model.SubdomainRecord, 0)
	for _, s := range e.subsByTask(task.ID) {
		if !matcher.Empty() && matcher.Match(s.Subdomain, s.Host, s.Domain) {
			continue
		}
		subs = append(subs, s)
	}

	outDir := e.cfg.Storage.OutputDir
	if len(urls) > 0 {
		p := outDir + "/" + task.TaskName + "_urls.csv"
		if err := exporter.WriteURLs(p, urls); err != nil && e.log != nil {
			e.log.Error("导出 URL CSV 失败", "path", p, "err", err.Error())
		}
	}
	if len(subs) > 0 {
		p := outDir + "/" + task.TaskName + "_subdomains.csv"
		if err := exporter.WriteSubdomains(p, subs); err != nil && e.log != nil {
			e.log.Error("导出子域名 CSV 失败", "path", p, "err", err.Error())
		}
	}
}

// urlsByTask 从 DB 取任务 URL（导出用）。
func (e *Engine) urlsByTask(taskID int64) []model.URLRecord {
	recs, err := e.store.URLsByTask(taskID)
	if err != nil {
		return nil
	}
	return recs
}

// subsByTask 从 DB 取任务子域名（导出用）。
func (e *Engine) subsByTask(taskID int64) []model.SubdomainRecord {
	recs, err := e.store.SubdomainsByTask(taskID)
	if err != nil {
		return nil
	}
	return recs
}

// enabledTokens 取启用且排序的 API Token。
func (e *Engine) enabledTokens() ([]quake.Token, error) {
	keys, err := e.store.GetEnabledAPIKeys()
	if err != nil {
		return nil, err
	}
	tokens := make([]quake.Token, 0, len(keys))
	for _, k := range keys {
		if strings.TrimSpace(k.Token) != "" {
			tokens = append(tokens, quake.Token{Name: k.Name, Value: k.Token})
		}
	}
	return tokens, nil
}

// ExportURLsCSVBytes 按任务导出 URL CSV（导出前黑名单过滤），返回含 BOM 的字节。
func (e *Engine) ExportURLsCSVBytes(taskID int64) ([]byte, error) {
	recs, err := e.store.URLsByTask(taskID)
	if err != nil {
		return nil, err
	}
	matcher := e.buildMatcher()
	out := make([]model.URLRecord, 0, len(recs))
	for _, r := range recs {
		if !matcher.Empty() && matcher.Match(r.URL, r.Host, r.Domain) {
			continue
		}
		out = append(out, r)
	}
	var buf bytes.Buffer
	if err := exporter.WriteURLsTo(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ExportSubdomainsCSVBytes 按任务导出子域名 CSV（导出前黑名单过滤）。
func (e *Engine) ExportSubdomainsCSVBytes(taskID int64) ([]byte, error) {
	recs, err := e.store.SubdomainsByTask(taskID)
	if err != nil {
		return nil, err
	}
	matcher := e.buildMatcher()
	out := make([]model.SubdomainRecord, 0, len(recs))
	for _, r := range recs {
		if !matcher.Empty() && matcher.Match(r.Subdomain, r.Host, r.Domain) {
			continue
		}
		out = append(out, r)
	}
	var buf bytes.Buffer
	if err := exporter.WriteSubdomainsTo(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// buildMatcher 合并配置与数据库黑名单构造匹配器。
func (e *Engine) buildMatcher() *blacklist.Matcher {
	keywords := append([]string{}, e.cfg.Blacklist.Keywords...)
	regexes := append([]string{}, e.cfg.Blacklist.Regexes...)
	entries, err := e.store.ListBlacklist()
	if err == nil {
		for _, en := range entries {
			switch en.Type {
			case model.BlacklistKeyword:
				keywords = append(keywords, en.Value)
			case model.BlacklistRegex:
				regexes = append(regexes, en.Value)
			}
		}
	}
	return blacklist.New(keywords, regexes, e.log)
}
