package probe

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kongtan/internal/config"
	"kongtan/internal/db"
	"kongtan/internal/model"
	"kongtan/internal/quake"
)

// TestRunGroupIntegration 端到端验证：分页、黑名单过滤、去重、IP 不作为子域名、ICP/位置抽取、CSV 导出。
func TestRunGroupIntegration(t *testing.T) {
	var bodies []map[string]any
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("解析请求体失败: %v", err)
			return
		}
		bodies = append(bodies, body)

		// 验收：请求中不得出现滚动相关参数。
		for _, k := range []string{"scroll", "scroll_id", "cursor"} {
			if _, ok := body[k]; ok {
				t.Errorf("请求中出现了禁止的滚动参数 %q", k)
			}
		}

		start := int(body["start"].(float64))
		var data []any
		if start == 0 {
			data = []any{
				map[string]any{
					"hostname": "wap.js.10086.cn", "ip": "1.2.3.4", "port": 80,
					"service": map[string]any{"name": "http", "http": map[string]any{
						"http_load_url": []any{"http://wap.js.10086.cn/index"}, "title": "移动门户",
						"icp": map[string]any{"licence": "京ICP备123号", "main_licence": map[string]any{"unit": "中国移动", "nature": "企业"}, "is_expired": false},
					}},
					"location": map[string]any{"gps": []any{116.4, 39.9}, "province_cn": "北京市", "city_cn": "北京市", "country_cn": "中国", "isp": "电信", "asname": "AS9808", "scene_cn": "数据中心"},
				},
				map[string]any{
					"hostname": "1.2.3.4", "ip": "1.2.3.4", "port": 443,
					"service": map[string]any{"name": "https", "http": map[string]any{"http_load_url": []any{"https://1.2.3.4/admin"}}},
				},
				map[string]any{
					"hostname": "x.ottcn.com", "ip": "5.6.7.8", "port": 80,
					"service": map[string]any{"name": "http", "http": map[string]any{"http_load_url": []any{"http://x.ottcn.com/"}}},
				},
				map[string]any{ // 与第 1 条 URL 重复，应去重
					"hostname": "wap.js.10086.cn", "ip": "1.2.3.4", "port": 80,
					"service": map[string]any{"name": "http", "http": map[string]any{"http_load_url": []any{"http://wap.js.10086.cn/index"}}},
				},
			}
		} else {
			data = []any{} // 空页，触发停止
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
	}))
	defer mock.Close()

	cfg := config.Default()
	cfg.Quake.BaseURL = mock.URL + "/api/v3/search/quake_service"
	cfg.Quake.PageSize = 4
	cfg.Quake.StartPage = 1
	cfg.Quake.MaxPages = 10
	cfg.Quake.RequestIntervalMs = 0
	cfg.Quake.PointBudgetPerTask = 1000
	cfg.Blacklist.Keywords = []string{"ottcn.com"}
	cfg.Storage.OutputDir = t.TempDir()

	store, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	defer store.Close()

	gid, err := store.CreateGroup("移动集团", "")
	if err != nil {
		t.Fatalf("建组失败: %v", err)
	}
	if _, err := store.AddDomains(gid, []string{"js.10086.cn"}); err != nil {
		t.Fatalf("加域名失败: %v", err)
	}
	if _, err := store.CreateAPIKey("primary", "faketoken", 0, true); err != nil {
		t.Fatalf("加 API 失败: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	qc := quake.New(cfg.Quake.BaseURL, 5, 0, logger)
	e := New(store, qc, cfg, logger)

	task, err := e.RunGroup(gid, model.TriggerManual)
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if task.Status != model.StatusSuccess {
		t.Fatalf("任务状态 = %s, want success (error: %s)", task.Status, task.ErrorMsg)
	}
	if task.TotalRecords != 4 {
		t.Fatalf("total_records = %d, want 4", task.TotalRecords)
	}
	if task.ConsumedPoints != 4 {
		t.Fatalf("consumed_points = %d, want 4", task.ConsumedPoints)
	}

	// URL：应 2 条（wap + IP），黑名单 ottcn 被过滤、重复被去重。
	urls, _ := store.URLsByTask(task.ID)
	if len(urls) != 2 {
		t.Fatalf("URL 数 = %d, want 2: %+v", len(urls), urls)
	}
	// 子域名：仅 wap.js.10086.cn（IP 不作为子域名、黑名单过滤、去重）。
	subs, _ := store.SubdomainsByTask(task.ID)
	if len(subs) != 1 {
		t.Fatalf("子域名数 = %d, want 1: %+v", len(subs), subs)
	}
	if subs[0].Subdomain != "wap.js.10086.cn" || subs[0].Domain != "10086.cn" {
		t.Fatalf("子域名 = %q, domain = %q, want wap.js.10086.cn / 10086.cn", subs[0].Subdomain, subs[0].Domain)
	}

	// ICP 与位置抽取。
	foundICP := false
	for _, u := range urls {
		if u.URL == "http://wap.js.10086.cn/index" {
			foundICP = true
			if u.ICPUnit != "中国移动" || u.ICPLicence != "京ICP备123号" || u.ICPNature != "企业" || u.ICPExpired != "false" {
				t.Fatalf("ICP 抽取错误: %+v", u)
			}
			if u.ProvinceCN != "北京市" || u.CityCN != "北京市" || u.ISP != "电信" || u.GPSLat != "39.9" || u.GPSLng != "116.4" {
				t.Fatalf("位置抽取错误: %+v", u)
			}
		}
	}
	if !foundICP {
		t.Fatalf("未找到含 ICP 的 URL")
	}

	// 黑名单未出现在任何结果中。
	for _, u := range urls {
		if strings.Contains(u.URL, "ottcn.com") || strings.Contains(u.Host, "ottcn.com") {
			t.Fatalf("黑名单 ottcn.com 未被过滤: %+v", u)
		}
	}

	// 导出文件存在且为 UTF-8 BOM。
	urlPath := filepath.Join(cfg.Storage.OutputDir, task.TaskName+"_urls.csv")
	b, err := os.ReadFile(urlPath)
	if err != nil {
		t.Fatalf("读取 URL CSV 失败: %v", err)
	}
	if !strings.HasPrefix(string(b), "\uFEFF") {
		t.Fatalf("URL CSV 缺少 UTF-8 BOM")
	}
	if !strings.Contains(string(b), "task_name,group_name,url,host,ip,port,title,domain") {
		t.Fatalf("URL CSV 表头顺序错误: %s", string(b[:120]))
	}

	// 请求体应包含 query/start/size，且无滚动参数。
	if len(bodies) == 0 {
		t.Fatalf("未发出任何请求")
	}
	if bodies[0]["query"] != `domain: "js.10086.cn"` {
		t.Fatalf("query = %v", bodies[0]["query"])
	}
}

// TestPointBudgetPartial 验证点数预算耗尽后停止并标记 partial。
func TestPointBudgetPartial(t *testing.T) {
	var reqCount int
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount++
		// 始终返回满页，制造「还有更多数据」的假象。
		data := []any{
			map[string]any{"hostname": "a.example.com", "service": map[string]any{"http": map[string]any{"http_load_url": []any{"http://a.example.com/1"}}}},
			map[string]any{"hostname": "b.example.com", "service": map[string]any{"http": map[string]any{"http_load_url": []any{"http://b.example.com/2"}}}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
	}))
	defer mock.Close()

	cfg := config.Default()
	cfg.Quake.BaseURL = mock.URL + "/api/v3/search/quake_service"
	cfg.Quake.PageSize = 2
	cfg.Quake.MaxPages = 100
	cfg.Quake.RequestIntervalMs = 0
	cfg.Quake.PointBudgetPerTask = 3
	cfg.Storage.OutputDir = t.TempDir()

	store, _ := db.Open(":memory:")
	defer store.Close()
	gid, _ := store.CreateGroup("g", "")
	_, _ = store.AddDomains(gid, []string{"example.com"})
	_, _ = store.CreateAPIKey("k", "t", 0, true)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	e := New(store, quake.New(cfg.Quake.BaseURL, 5, 0, logger), cfg, logger)

	task, err := e.RunGroup(gid, model.TriggerManual)
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if task.Status != model.StatusPartial {
		t.Fatalf("状态 = %s, want partial", task.Status)
	}
	if reqCount != 2 {
		t.Fatalf("请求次数 = %d, want 2（预算耗尽应立即停止）", reqCount)
	}
	if task.ConsumedPoints != 4 {
		t.Fatalf("consumed_points = %d, want 4", task.ConsumedPoints)
	}
	if !strings.Contains(task.ErrorMsg, "点数预算耗尽") {
		t.Fatalf("error_msg 应包含点数预算耗尽: %s", task.ErrorMsg)
	}
}
