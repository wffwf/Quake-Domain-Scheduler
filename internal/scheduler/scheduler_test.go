package scheduler

import (
	"testing"
	"time"
)

func cst() *time.Location {
	return time.FixedZone("CST", 8*3600) // 与 Asia/Shanghai 一致（无 DST）
}

func TestInMonthEndWindow(t *testing.T) {
	loc := cst()
	cases := []struct {
		name    string
		tm      string
		windowH int
		want    bool
	}{
		{"闰年2月最后一天窗口内", "2024-02-29T20:00:00", 6, true},
		{"闰年2月最后一天窗口外", "2024-02-29T10:00:00", 6, false},
		{"恰好月末0点（不含）", "2024-03-01T00:00:00", 6, false},
		{"次月1日凌晨（不含）", "2024-03-01T00:30:00", 6, false},
		{"年末12月31日窗口内", "2023-12-31T20:00:00", 6, true},
		{"1月31日窗口内", "2024-01-31T19:00:00", 6, true},
		{"1月31日窗口外", "2024-01-31T17:00:00", 6, false},
		{"非闰年2月28日窗口内", "2023-02-28T23:00:00", 6, true},
		{"窗口起始点含", "2024-01-31T18:00:00", 6, true},
		{"窗口小时为1", "2024-01-31T23:30:00", 1, true},
		{"窗口小时为1但过早", "2024-01-31T22:30:00", 1, false},
	}
	for _, c := range cases {
		tm, err := time.ParseInLocation("2006-01-02T15:04:05", c.tm, loc)
		if err != nil {
			t.Fatalf("解析时间失败 %s: %v", c.tm, err)
		}
		got := InMonthEndWindow(tm, loc, c.windowH)
		if got != c.want {
			t.Errorf("%s: InMonthEndWindow(%s, window=%d) = %v, want %v", c.name, c.tm, c.windowH, got, c.want)
		}
	}
}

func TestMonthKey(t *testing.T) {
	loc := cst()
	tm := time.Date(2024, 2, 29, 23, 59, 0, 0, loc)
	if got := MonthKey(tm, loc); got != "2024-02" {
		t.Fatalf("MonthKey = %q, want 2024-02", got)
	}
	// UTC 时区下跨月边界。
	utc := time.UTC
	tm = time.Date(2024, 1, 1, 0, 0, 0, 0, utc)
	if got := MonthKey(tm, utc); got != "2024-01" {
		t.Fatalf("MonthKey = %q, want 2024-01", got)
	}
}

// TestSelfTestCases 表驱动覆盖 §6.8.2 的 8 组自测用例。
func TestSelfTestCases(t *testing.T) {
	cases := selfTestCases(6)
	if len(cases) != 9 {
		t.Fatalf("自测用例数 = %d, want 9（8 组场景，时区切换拆为 2 行）", len(cases))
	}
	for _, c := range cases {
		inW, should, _ := Judge(c.now, c.loc, 6, c.monthExecuted)
		if inW != c.wantInWindow || should != c.wantShouldRun {
			t.Errorf("%s: in_window=%v should_run=%v, want %v/%v",
				c.name, inW, should, c.wantInWindow, c.wantShouldRun)
		}
	}
}

// TestRunSelfTest 验证自测结果整体通过，且原因文案正确。
func TestRunSelfTest(t *testing.T) {
	results := RunSelfTest(6)
	for _, r := range results {
		if !r.Passed {
			t.Errorf("自测用例未通过: %s (%s/%v/%v/%s)", r.Name, r.InputTime, r.InWindow, r.ShouldRun, r.Reason)
		}
	}
	// 抽查关键原因文案。
	byName := map[string]SelfTestResult{}
	for _, r := range results {
		byName[r.Name] = r
	}
	if byName["窗口起点前一秒"].Reason != "未进入窗口" {
		t.Errorf("窗口起点前一秒 reason = %q, want 未进入窗口", byName["窗口起点前一秒"].Reason)
	}
	if byName["窗口起点"].Reason != "窗口内且未执行" {
		t.Errorf("窗口起点 reason = %q, want 窗口内且未执行", byName["窗口起点"].Reason)
	}
	if byName["同月窗口内第二次调用"].Reason != "本月已执行" {
		t.Errorf("同月第二次 reason = %q, want 本月已执行", byName["同月窗口内第二次调用"].Reason)
	}
}

// TestNextWindowStart 验证"下一次窗口起点"计算。
func TestNextWindowStart(t *testing.T) {
	sh := cst()
	h := 6
	// 月中：下一次 = 本月窗口起点（1 月 31 日 18:00）。
	mid := time.Date(2026, 1, 15, 12, 0, 0, 0, sh)
	want := time.Date(2026, 1, 31, 18, 0, 0, 0, sh)
	if got := NextWindowStart(mid, sh, h); !got.Equal(want) {
		t.Fatalf("月中 NextWindowStart = %v, want %v", got, want)
	}
	// 窗口内：下一次 = 下月窗口起点（2 月 28 日 18:00）。
	inWin := time.Date(2026, 1, 31, 20, 0, 0, 0, sh)
	want = time.Date(2026, 2, 28, 18, 0, 0, 0, sh)
	if got := NextWindowStart(inWin, sh, h); !got.Equal(want) {
		t.Fatalf("窗口内 NextWindowStart = %v, want %v", got, want)
	}
}
