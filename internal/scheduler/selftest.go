package scheduler

import "time"

// SelfTestResult 一个调度自测用例的判定结果。
type SelfTestResult struct {
	Name      string `json:"name"`
	InputTime string `json:"input_time"`
	Timezone  string `json:"timezone"`
	InWindow  bool   `json:"in_window"`
	ShouldRun bool   `json:"should_run"`
	Reason    string `json:"reason"`
	Passed    bool   `json:"passed"`
}

// selfTestCase 内置自测用例（表驱动）。
type selfTestCase struct {
	name          string
	now           time.Time // 虚拟时间（绝对瞬时）
	loc           *time.Location
	tzName        string
	monthExecuted bool
	wantInWindow  bool
	wantShouldRun bool
}

func shanghai() *time.Location { return time.FixedZone("Asia/Shanghai", 8*3600) }

// selfTestCases 构造 8 组用例（时区切换拆为两组，共 9 行）。
// 时间点相对窗口起点计算，因此对任意 monthly_window_hours 均正确。
func selfTestCases(windowHours int) []selfTestCase {
	h := time.Duration(windowHours) * time.Hour
	sh := shanghai()

	// 参考月：2026-01（31 天），月末 = 2026-02-01 00:00 (+08)。
	meJan := time.Date(2026, 2, 1, 0, 0, 0, 0, sh)
	wsJan := meJan.Add(-h)
	// 2026-02 非闰年（28 天）。
	wsFeb := time.Date(2026, 3, 1, 0, 0, 0, 0, sh).Add(-h)
	// 2028-02 闰年（29 天）。
	wsLeap := time.Date(2028, 3, 1, 0, 0, 0, 0, sh).Add(-h)

	// 时区切换用的绝对瞬时：上海窗口起点 + 1 小时（在上海时区内、UTC 时区外）。
	tzInstant := wsJan.Add(time.Hour)

	return []selfTestCase{
		{"月中普通时间", time.Date(2026, 1, 15, 12, 0, 0, 0, sh), sh, "Asia/Shanghai", false, false, false},
		{"窗口起点前一秒", wsJan.Add(-time.Second), sh, "Asia/Shanghai", false, false, false},
		{"窗口起点", wsJan, sh, "Asia/Shanghai", false, true, true},
		{"月末最后一秒", meJan.Add(-time.Second), sh, "Asia/Shanghai", false, true, true},
		{"同月窗口内第二次调用", wsJan.Add(time.Hour), sh, "Asia/Shanghai", true, true, false},
		{"跨月后窗口内首次调用", wsFeb.Add(time.Hour), sh, "Asia/Shanghai", false, true, true},
		{"闰年2月(2028-02-29)", wsLeap.Add(time.Hour), sh, "Asia/Shanghai", false, true, true},
		{"时区切换(Asia/Shanghai)", tzInstant, sh, "Asia/Shanghai", false, true, true},
		{"时区切换(UTC)", tzInstant, time.UTC, "UTC", false, false, false},
	}
}

// RunSelfTest 运行内置 8 用例自测，返回每组的判定结果与是否通过。
func RunSelfTest(windowHours int) []SelfTestResult {
	cases := selfTestCases(windowHours)
	out := make([]SelfTestResult, 0, len(cases))
	for _, c := range cases {
		inW, should, reason := Judge(c.now, c.loc, windowHours, c.monthExecuted)
		out = append(out, SelfTestResult{
			Name:      c.name,
			InputTime: c.now.In(c.loc).Format("2006-01-02 15:04:05"),
			Timezone:  c.tzName,
			InWindow:  inW,
			ShouldRun: should,
			Reason:    reason,
			Passed:    inW == c.wantInWindow && should == c.wantShouldRun,
		})
	}
	return out
}
