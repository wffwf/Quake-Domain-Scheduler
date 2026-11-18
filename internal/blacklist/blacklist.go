// Package blacklist 实现关键词/正则黑名单过滤。
//
// 匹配对象为完整 URL、host、domain，任一命中即判定过滤。
package blacklist

import (
	"log/slog"
	"regexp"
	"strings"
)

// Matcher 黑名单匹配器。
type Matcher struct {
	keywords []string
	regexes  []*regexp.Regexp
}

// New 构造匹配器。无效正则会被忽略并记日志（不阻断任务）。
func New(keywords, regexes []string, log *slog.Logger) *Matcher {
	m := &Matcher{}
	for _, k := range keywords {
		k = strings.ToLower(strings.TrimSpace(k))
		if k != "" {
			m.keywords = append(m.keywords, k)
		}
	}
	for _, r := range regexes {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		re, err := regexp.Compile(r)
		if err != nil {
			if log != nil {
				log.Warn("黑名单正则无效，已忽略", "regex", r, "err", err.Error())
			}
			continue
		}
		m.regexes = append(m.regexes, re)
	}
	return m
}

// Match 判断 URL / host / domain 是否命中黑名单。
func (m *Matcher) Match(url, host, domain string) bool {
	u := strings.ToLower(url)
	h := strings.ToLower(host)
	d := strings.ToLower(domain)
	for _, k := range m.keywords {
		if strings.Contains(u, k) || strings.Contains(h, k) || strings.Contains(d, k) {
			return true
		}
	}
	for _, re := range m.regexes {
		if re.MatchString(url) || re.MatchString(host) || re.MatchString(domain) {
			return true
		}
	}
	return false
}

// Empty 是否没有任何规则。
func (m *Matcher) Empty() bool {
	return len(m.keywords) == 0 && len(m.regexes) == 0
}
