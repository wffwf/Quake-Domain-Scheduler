// Package scheduler 实现"每月最后 N 小时"窗口内的自动探测调度。
//
// 使用 robfig/cron 提供周期 tick，结合自实现的月末窗口判断；同一自然月内
// 自动任务只执行一次（记录到 schedule_runs 表）。
package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/robfig/cron/v3"

	"kongtan/internal/config"
	"kongtan/internal/db"
	"kongtan/internal/model"
	"kongtan/internal/probe"
)

// Scheduler 月末窗口调度器。
type Scheduler struct {
	engine *probe.Engine
	store  *db.Store
	cfg    *config.Config
	loc    *time.Location
	log    *slog.Logger
	// now 可注入的时钟（测试用），默认 time.Now。
	now func() time.Time
}

// New 构造调度器并解析时区。
func New(engine *probe.Engine, store *db.Store, cfg *config.Config, log *slog.Logger) (*Scheduler, error) {
	loc, err := time.LoadLocation(cfg.Schedule.Timezone)
	if err != nil {
		return nil, err
	}
	return &Scheduler{
		engine: engine,
		store:  store,
		cfg:    cfg,
		loc:    loc,
		log:    log,
		now:    time.Now,
	}, nil
}

// SetNow 注入时钟（测试用）。
func (s *Scheduler) SetNow(fn func() time.Time) { s.now = fn }

// Start 启动定时检查（每分钟 tick 一次）。
func (s *Scheduler) Start(ctx context.Context) {
	c := cron.New()
	_, _ = c.AddFunc("@every 1m", func() {
		s.checkAndRun()
	})
	c.Start()
	<-ctx.Done()
	c.Stop()
}

// checkAndRun 判断是否处于月末窗口且本月未执行，若是则触发。
func (s *Scheduler) checkAndRun() {
	now := s.now()
	if !InMonthEndWindow(now, s.loc, s.cfg.Schedule.MonthlyWindowHours) {
		return
	}
	key := MonthKey(now, s.loc)
	ran, err := s.store.HasScheduleRun(key)
	if err != nil {
		s.log.Error("查询调度标记失败", "err", err.Error())
		return
	}
	if ran {
		return
	}
	if err := s.store.MarkScheduleRun(key); err != nil {
		s.log.Error("写入调度标记失败", "err", err.Error())
		return
	}
	s.log.Info("进入月末窗口，开始自动探测", "month", key)
	go s.runAllGroups()
}

// runAllGroups 顺序执行所有含启用域名的分组。
func (s *Scheduler) runAllGroups() {
	domains, err := s.store.ListAllEnabledDomains()
	if err != nil {
		s.log.Error("读取启用域名失败", "err", err.Error())
		return
	}
	groupSet := map[int64]bool{}
	for _, d := range domains {
		groupSet[d.GroupID] = true
	}
	groups, err := s.store.ListGroups()
	if err != nil {
		s.log.Error("读取分组失败", "err", err.Error())
		return
	}
	ran := 0
	for _, g := range groups {
		if !groupSet[g.ID] {
			continue
		}
		if _, err := s.engine.RunGroup(g.ID, model.TriggerScheduled); err != nil {
			s.log.Error("定时执行分组失败", "group", g.Name, "err", err.Error())
			continue
		}
		ran++
	}
	s.log.Info("自动探测完成", "执行分组数", ran)
}

// InMonthEndWindow 判断 now 是否处于其所在月份的"最后 windowHours 小时"窗口内。
// 正确处理跨月、闰月与时区。
func InMonthEndWindow(now time.Time, loc *time.Location, windowHours int) bool {
	n := now.In(loc)
	year, month, _ := n.Date()
	// 下月 1 日 00:00（time.Date 会自动归一化 month+1 与闰年）。
	firstOfNext := time.Date(year, month+1, 1, 0, 0, 0, 0, loc)
	windowStart := firstOfNext.Add(-time.Duration(windowHours) * time.Hour)
	return !n.Before(windowStart) && n.Before(firstOfNext)
}

// MonthKey 返回某时刻所在自然月标识（如 "2026-11"）。
func MonthKey(t time.Time, loc *time.Location) string {
	return t.In(loc).Format("2006-01")
}

// WindowStart 返回 now 所在月份的"月末窗口"起点（月最后一天的 00:00 往前推 windowHours）。
func WindowStart(now time.Time, loc *time.Location, windowHours int) time.Time {
	n := now.In(loc)
	firstOfNext := time.Date(n.Year(), n.Month()+1, 1, 0, 0, 0, 0, loc)
	return firstOfNext.Add(-time.Duration(windowHours) * time.Hour)
}

// NextWindowStart 返回下一次窗口起点：若 now 尚未到本月窗口起点，返回本月窗口起点；
// 否则返回下个月的窗口起点。
func NextWindowStart(now time.Time, loc *time.Location, windowHours int) time.Time {
	n := now.In(loc)
	ws := WindowStart(n, loc, windowHours)
	if !n.Before(ws) {
		ws = WindowStart(time.Date(n.Year(), n.Month()+1, 1, 0, 0, 0, 0, loc), loc, windowHours)
	}
	return ws
}

// Judge 纯判定：返回是否在窗口内、是否应触发、以及原因说明。
// 不发起任何外部请求、不写库，仅做判定（供调度自测复用）。
func Judge(now time.Time, loc *time.Location, windowHours int, monthExecuted bool) (inWindow, shouldRun bool, reason string) {
	inWindow = InMonthEndWindow(now, loc, windowHours)
	switch {
	case !inWindow:
		return false, false, "未进入窗口"
	case monthExecuted:
		return true, false, "本月已执行"
	default:
		return true, true, "窗口内且未执行"
	}
}
