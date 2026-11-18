// Package processor 负责把 Quake 返回的原始资产记录解析为结构化数据。
//
// 关键规则（依据需求 §6.6）：
//   - URL 取 service.http.http_load_url 首个元素，兜底 service.http.load_url；
//   - host 优先从 URL 解析，失败回退 hostname / domain 字段，绝不回退到 ip；
//   - host 为 IP 时绝不写入 subdomain（URL 记录的 ip 字段仍保留）；
//   - 裸主机名（无 scheme）也能解析出 subdomain 与注册域。
package processor

import (
	"encoding/json"
	"net"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// Processed 一条资产处理后的结构化结果。
type Processed struct {
	URL        string
	Host       string
	IP         string
	Port       int
	Title      string
	Domain     string
	Subdomain  string
	ICPLicence string
	ICPUnit    string
	ICPNature  string
	ICPExpired string
	ProvinceCN string
	CityCN     string
	CountryCN  string
	ISP        string
	ASName     string
	SceneCN    string
	GPSLat     string
	GPSLng     string
}

// Process 解析一条 Quake 原始资产 JSON。
func Process(raw json.RawMessage) *Processed {
	p := &Processed{}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return p
	}

	// URL：http_load_url 首个非空元素，兜底 load_url 字符串。
	p.URL = firstString(pick(m, "service", "http", "http_load_url"))
	if p.URL == "" {
		p.URL = asString(pick(m, "service", "http", "load_url"))
	}
	p.URL = strings.TrimSpace(p.URL)

	p.IP = asString(pick(m, "ip"))
	p.Port = asInt(pick(m, "port"))
	p.Title = strings.Join(strings.Fields(asString(pick(m, "service", "http", "title"))), " ")

	// 候选 host：URL → hostname 字段 → domain 字段（绝不用 ip）。
	p.Host = hostFromURL(p.URL)
	if p.Host == "" {
		p.Host = strings.ToLower(strings.TrimSpace(asString(pick(m, "hostname"))))
	}
	if p.Host == "" {
		p.Host = strings.ToLower(strings.TrimSpace(asString(pick(m, "domain"))))
	}
	p.Host = strings.TrimSuffix(p.Host, ".")

	// subdomain：host 非空且非 IP 才写入。
	if p.Host != "" && !IsIP(p.Host) {
		p.Subdomain = p.Host
	}
	p.Domain = RegisteredDomain(p.Host)

	// ICP 抽取：真实响应位于 service.http.icp，兜底顶层 icp（兼容旧版响应/规格描述）。
	icp := icpObject(m)
	p.ICPLicence = asString(pick(icp, "licence"))
	p.ICPUnit = asString(pick(icp, "main_licence", "unit"))
	p.ICPNature = asString(pick(icp, "main_licence", "nature"))
	p.ICPExpired = asBoolStr(pick(icp, "is_expired"))

	// 地理位置抽取。
	p.GPSLat, p.GPSLng = gps(pick(m, "location", "gps"))
	p.ProvinceCN = pickStr(m, "province_cn")
	p.CityCN = pickStr(m, "city_cn")
	p.CountryCN = pickStr(m, "country_cn")
	p.ISP = pickStr(m, "isp")
	p.ASName = pickStr(m, "asname")
	p.SceneCN = pickStr(m, "scene_cn")

	return p
}

// RegisteredDomain 提取注册域（eTLD+1），失败回退最后两级标签。
func RegisteredDomain(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.TrimSuffix(host, ".")
	if host == "" || IsIP(host) {
		return ""
	}
	if d, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil && d != "" {
		return d
	}
	parts := strings.Split(host, ".")
	if len(parts) >= 2 {
		return strings.Join(parts[len(parts)-2:], ".")
	}
	return host
}

// IsIP 判断字符串是否为 IP 字面量。
func IsIP(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	// 去掉端口（如有）。
	if h, _, err := net.SplitHostPort(s); err == nil {
		s = h
	}
	return net.ParseIP(s) != nil
}

// hostFromURL 从 URL 解析 hostname（无 scheme 自动补 http://，去掉端口）。
func hostFromURL(rawURL string) string {
	u := strings.TrimSpace(rawURL)
	if u == "" {
		return ""
	}
	if !strings.Contains(u, "://") {
		u = "http://" + u
	}
	p, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return strings.ToLower(p.Hostname())
}

// pick 沿路径取嵌套值。
func pick(m map[string]any, keys ...string) any {
	var cur any = m
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[k]
	}
	return cur
}

// icpObject 取 ICP 对象：优先 service.http.icp（Quake 实际线协议），兜底顶层 icp。
func icpObject(m map[string]any) map[string]any {
	if v, ok := pick(m, "service", "http", "icp").(map[string]any); ok && len(v) > 0 {
		return v
	}
	if v, ok := pick(m, "icp").(map[string]any); ok && len(v) > 0 {
		return v
	}
	return nil
}

// asString 把任意 JSON 值转为字符串（数组取首个非空元素）。
func asString(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case []any:
		for _, e := range t {
			if s := asString(e); s != "" {
				return s
			}
		}
	case []string:
		for _, e := range t {
			if s := strings.TrimSpace(e); s != "" {
				return s
			}
		}
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case json.Number:
		return t.String()
	}
	return ""
}

// firstString 数组取首个非空字符串元素。
func firstString(v any) string {
	return asString(v)
}

// asInt 转整数（JSON 数字）。
func asInt(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(t))
		return n
	}
	return 0
}

// asBoolStr 转 "true"/"false"/""。
func asBoolStr(v any) string {
	switch t := v.(type) {
	case bool:
		if t {
			return "true"
		}
		return "false"
	case string:
		return strings.TrimSpace(t)
	}
	return ""
}

// pickStr 优先取 location 对象下的字段，缺失回退顶层同名字段。
func pickStr(m map[string]any, key string) string {
	if v := asString(pick(m, "location", key)); v != "" {
		return v
	}
	return asString(pick(m, key))
}

// gps 解析 location.gps 数组为经纬度字符串。
// Quake 的 gps 采用 [经度, 纬度]（GeoJSON 约定），故返回 (lat, lng) = (gps[1], gps[0])。
func gps(v any) (lat, lng string) {
	arr, ok := v.([]any)
	if !ok || len(arr) < 2 {
		return "", ""
	}
	return fmtFloat(arr[1]), fmtFloat(arr[0])
}

func fmtFloat(v any) string {
	switch t := v.(type) {
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case string:
		return strings.TrimSpace(t)
	}
	return ""
}
