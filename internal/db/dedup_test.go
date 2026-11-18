package db

import (
	"testing"

	"kongtan/internal/model"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:) 失败: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestListURLsDedupAndTaskStartedAt(t *testing.T) {
	s := newTestStore(t)

	t1 := &model.Task{GroupID: 1, GroupName: "g1", TaskName: "g1_t1", TriggerType: model.TriggerManual, Status: model.StatusSuccess, StartedAt: "2026-01-01 10:00:00", FinishedAt: "2026-01-01 10:00:10"}
	id1, _ := s.CreateTask(t1)
	t2 := &model.Task{GroupID: 1, GroupName: "g1", TaskName: "g1_t2", TriggerType: model.TriggerManual, Status: model.StatusSuccess, StartedAt: "2026-01-02 11:00:00", FinishedAt: "2026-01-02 11:00:05"}
	id2, _ := s.CreateTask(t2)

	// 同一 URL 两条记录，第二条更新（id 更大）。
	recs := []model.URLRecord{
		{TaskID: id1, TaskName: "g1_t1", GroupName: "g1", URL: "http://a.example.com/x", Host: "a.example.com", Domain: "example.com", Title: "旧"},
		{TaskID: id2, TaskName: "g1_t2", GroupName: "g1", URL: "http://a.example.com/x", Host: "a.example.com", Domain: "example.com", Title: "新"},
		{TaskID: id1, TaskName: "g1_t1", GroupName: "g1", URL: "http://b.example.com/y", Host: "b.example.com", Domain: "example.com", Title: "独立"},
	}
	if err := s.InsertURLs(recs); err != nil {
		t.Fatalf("InsertURLs 失败: %v", err)
	}

	// 不去重：3 条。
	all, _ := s.ListURLs(URLFilter{Limit: 100})
	if len(all) != 3 {
		t.Fatalf("不去重应返回 3 条, got %d", len(all))
	}

	// 去重：按 url 保留最新（title=新 的那条，task=id2）。
	dedup, _ := s.ListURLs(URLFilter{Limit: 100, Dedup: true})
	if len(dedup) != 2 {
		t.Fatalf("去重应返回 2 条, got %d", len(dedup))
	}
	byURL := map[string]model.URLRecord{}
	for _, r := range dedup {
		byURL[r.URL] = r
	}
	if got := byURL["http://a.example.com/x"].Title; got != "新" {
		t.Errorf("去重后应保留最新记录(title=新), got %q", got)
	}
	if got := byURL["http://a.example.com/x"].TaskStartedAt; got != "2026-01-02 11:00:00" {
		t.Errorf("task_started_at 应为最新任务时间, got %q", got)
	}
	if got := byURL["http://a.example.com/x"].TaskID; got != id2 {
		t.Errorf("去重后应保留最新 task_id=%d, got %d", id2, got)
	}

	// 去重计数。
	n, _ := s.CountURLs(URLFilter{Dedup: true})
	if n != 2 {
		t.Errorf("去重计数应为 2, got %d", n)
	}
}

func TestListSubdomainsDedup(t *testing.T) {
	s := newTestStore(t)
	t1 := &model.Task{GroupID: 1, GroupName: "g", TaskName: "g_1", TriggerType: model.TriggerManual, Status: model.StatusSuccess, StartedAt: "2026-01-01 10:00:00"}
	id1, _ := s.CreateTask(t1)
	t2 := &model.Task{GroupID: 1, GroupName: "g", TaskName: "g_2", TriggerType: model.TriggerManual, Status: model.StatusSuccess, StartedAt: "2026-01-02 10:00:00"}
	id2, _ := s.CreateTask(t2)

	recs := []model.SubdomainRecord{
		{TaskID: id1, TaskName: "g_1", GroupName: "g", Subdomain: "wap.js.10086.cn", Domain: "10086.cn", Host: "wap.js.10086.cn", ISP: "old"},
		{TaskID: id2, TaskName: "g_2", GroupName: "g", Subdomain: "wap.js.10086.cn", Domain: "10086.cn", Host: "wap.js.10086.cn", ISP: "new"},
		{TaskID: id1, TaskName: "g_1", GroupName: "g", Subdomain: "m.10086.cn", Domain: "10086.cn", Host: "m.10086.cn", ISP: "x"},
	}
	if err := s.InsertSubdomains(recs); err != nil {
		t.Fatalf("InsertSubdomains 失败: %v", err)
	}

	dedup, _ := s.ListSubdomains(SubdomainFilter{Limit: 100, Dedup: true})
	if len(dedup) != 2 {
		t.Fatalf("子域名去重应返回 2 条, got %d", len(dedup))
	}
	bySub := map[string]model.SubdomainRecord{}
	for _, r := range dedup {
		bySub[r.Subdomain] = r
	}
	if got := bySub["wap.js.10086.cn"].ISP; got != "new" {
		t.Errorf("子域名去重应保留最新(isp=new), got %q", got)
	}
	if got := bySub["wap.js.10086.cn"].TaskID; got != id2 {
		t.Errorf("子域名去重应保留最新 task_id=%d, got %d", id2, got)
	}
}

func TestScheduleQueries(t *testing.T) {
	s := newTestStore(t)

	if got, _ := s.GetScheduleRun("2026-01"); got != "" {
		t.Errorf("未执行月份应返回空, got %q", got)
	}
	if err := s.MarkScheduleRun("2026-01"); err != nil {
		t.Fatalf("MarkScheduleRun 失败: %v", err)
	}
	if got, _ := s.GetScheduleRun("2026-01"); got == "" {
		t.Errorf("标记后应返回时间, got empty")
	}
	if has, _ := s.HasScheduleRun("2026-01"); !has {
		t.Errorf("HasScheduleRun 应为 true")
	}

	// 定时任务与手动任务。
	sch := &model.Task{GroupID: 1, GroupName: "g", TaskName: "g_20260131_180000", TriggerType: model.TriggerScheduled, Status: model.StatusSuccess, StartedAt: "2026-01-31 18:00:00", FinishedAt: "2026-01-31 18:00:12", ConsumedPoints: 99}
	idSch, _ := s.CreateTask(sch)
	_, _ = s.CreateTask(&model.Task{GroupID: 1, GroupName: "g", TaskName: "g_manual", TriggerType: model.TriggerManual, Status: model.StatusSuccess, StartedAt: "2026-01-30 10:00:00"})

	list, _ := s.ListScheduledTasks(10)
	if len(list) != 1 {
		t.Fatalf("ListScheduledTasks 应只含 1 条定时任务, got %d", len(list))
	}
	if list[0].ID != idSch {
		t.Errorf("定时任务 id 应为 %d, got %d", idSch, list[0].ID)
	}

	mt, _ := s.FirstScheduledTaskInMonth(2026, 1)
	if mt == nil || mt.ID != idSch {
		t.Errorf("FirstScheduledTaskInMonth 应返回 %d, got %+v", idSch, mt)
	}
	if mt2, _ := s.FirstScheduledTaskInMonth(2026, 2); mt2 != nil {
		t.Errorf("2 月无定时任务应返回 nil, got %+v", mt2)
	}
}
