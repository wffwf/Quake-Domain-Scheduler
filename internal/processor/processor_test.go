package processor

import (
	"encoding/json"
	"testing"
)

func mustRaw(t *testing.T, s string) json.RawMessage {
	t.Helper()
	return json.RawMessage(s)
}

func TestBareHostnameNoScheme(t *testing.T) {
	// 裸主机名（无 ://），应解析出 subdomain 与注册域。
	p := Process(mustRaw(t, `{"hostname":"wap.js.10086.cn"}`))
	if p.Subdomain != "wap.js.10086.cn" {
		t.Fatalf("subdomain = %q, want wap.js.10086.cn", p.Subdomain)
	}
	if p.Domain != "10086.cn" {
		t.Fatalf("domain = %q, want 10086.cn", p.Domain)
	}
	if p.Host != "wap.js.10086.cn" {
		t.Fatalf("host = %q, want wap.js.10086.cn", p.Host)
	}
}

func TestURLWithoutScheme(t *testing.T) {
	// URL 字段本身不含 scheme（如裸主机名出现在 http_load_url）。
	p := Process(mustRaw(t, `{"service":{"http":{"http_load_url":["wap.js.10086.cn/path"]}}}`))
	if p.URL != "wap.js.10086.cn/path" {
		t.Fatalf("url = %q", p.URL)
	}
	if p.Subdomain != "wap.js.10086.cn" {
		t.Fatalf("subdomain = %q, want wap.js.10086.cn", p.Subdomain)
	}
	if p.Domain != "10086.cn" {
		t.Fatalf("domain = %q, want 10086.cn", p.Domain)
	}
}

func TestURLWithScheme(t *testing.T) {
	p := Process(mustRaw(t, `{"service":{"http":{"http_load_url":["http://a.b.10086.cn:8080/index"]}},"ip":"1.2.3.4","port":8080}`))
	if p.URL != "http://a.b.10086.cn:8080/index" {
		t.Fatalf("url = %q", p.URL)
	}
	if p.Host != "a.b.10086.cn" { // 端口应被去掉
		t.Fatalf("host = %q, want a.b.10086.cn", p.Host)
	}
	if p.Subdomain != "a.b.10086.cn" {
		t.Fatalf("subdomain = %q", p.Subdomain)
	}
	if p.Domain != "10086.cn" {
		t.Fatalf("domain = %q, want 10086.cn", p.Domain)
	}
}

func TestIPNotWrittenAsSubdomain(t *testing.T) {
	// URL 的 host 是 IP：绝不写入 subdomain，但 URL 记录 ip 字段保留。
	p := Process(mustRaw(t, `{"service":{"http":{"http_load_url":["http://10.0.0.1/admin"]}},"ip":"10.0.0.1","port":80}`))
	if p.Subdomain != "" {
		t.Fatalf("subdomain = %q, want empty (IP 不能作为子域名)", p.Subdomain)
	}
	if p.Host != "10.0.0.1" {
		t.Fatalf("host = %q, want 10.0.0.1", p.Host)
	}
	if p.Domain != "" {
		t.Fatalf("domain = %q, want empty for IP host", p.Domain)
	}
	if p.IP != "10.0.0.1" {
		t.Fatalf("ip = %q, want 10.0.0.1", p.IP)
	}
}

func TestFallbackToHostnameField(t *testing.T) {
	// 无 URL 时回退 hostname 字段；hostname 为 IP 时仍不写 subdomain。
	p := Process(mustRaw(t, `{"hostname":"1.2.3.4"}`))
	if p.Subdomain != "" {
		t.Fatalf("subdomain = %q, want empty", p.Subdomain)
	}
	if p.Host != "1.2.3.4" {
		t.Fatalf("host = %q", p.Host)
	}
}

func TestICPAndLocationExtraction(t *testing.T) {
	// 按真实 Quake 响应结构：ICP 位于 service.http.icp。
	p := Process(mustRaw(t, `{
		"hostname":"www.example.com",
		"service":{"name":"http","http":{"http_load_url":["http://www.example.com/"],"icp":{
			"licence":"京ICP备123号-1",
			"main_licence":{"licence":"京ICP备123号","unit":"示例公司","nature":"企业"},
			"is_expired":false
		}}},
		"location":{"gps":[116.4,39.9],"province_cn":"北京市","city_cn":"北京市","country_cn":"中国","isp":"电信","asname":"AS1234","scene_cn":"数据中心"}
	}`))
	if p.ICPLicence != "京ICP备123号-1" {
		t.Fatalf("icp_licence = %q", p.ICPLicence)
	}
	if p.ICPUnit != "示例公司" {
		t.Fatalf("icp_unit = %q", p.ICPUnit)
	}
	if p.ICPNature != "企业" {
		t.Fatalf("icp_nature = %q", p.ICPNature)
	}
	if p.ICPExpired != "false" {
		t.Fatalf("icp_expired = %q, want false", p.ICPExpired)
	}
	if p.GPSLat != "39.9" || p.GPSLng != "116.4" {
		t.Fatalf("gps = %q,%q", p.GPSLat, p.GPSLng)
	}
	if p.ProvinceCN != "北京市" || p.CityCN != "北京市" || p.CountryCN != "中国" {
		t.Fatalf("location = %q,%q,%q", p.ProvinceCN, p.CityCN, p.CountryCN)
	}
	if p.ISP != "电信" || p.ASName != "AS1234" || p.SceneCN != "数据中心" {
		t.Fatalf("isp/asname/scene = %q,%q,%q", p.ISP, p.ASName, p.SceneCN)
	}
}

func TestICPFallbackTopLevel(t *testing.T) {
	// 兜底：某些响应可能把 icp 放在顶层（规格旧路径）。
	p := Process(mustRaw(t, `{
		"icp":{"licence":"京ICP备456号","main_licence":{"unit":"顶层公司","nature":"事业单位"},"is_expired":true}
	}`))
	if p.ICPUnit != "顶层公司" || p.ICPLicence != "京ICP备456号" || p.ICPNature != "事业单位" || p.ICPExpired != "true" {
		t.Fatalf("顶层 icp 兜底失败: %+v", p)
	}
}

func TestMissingFieldsAreEmpty(t *testing.T) {
	p := Process(mustRaw(t, `{}`))
	if p.URL != "" || p.Host != "" || p.Subdomain != "" || p.Domain != "" || p.IP != "" {
		t.Fatalf("empty record should yield empty fields, got %+v", p)
	}
}

func TestRegisteredDomain(t *testing.T) {
	cases := map[string]string{
		"wap.js.10086.cn":  "10086.cn",
		"a.b.example.com":  "example.com",
		"www.example.co.uk": "example.co.uk",
		"10.0.0.1":          "",
	}
	for in, want := range cases {
		if got := RegisteredDomain(in); got != want {
			t.Fatalf("RegisteredDomain(%q) = %q, want %q", in, got, want)
		}
	}
}
