package quake

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUserInfoURL(t *testing.T) {
	c := &Client{BaseURL: "https://quake.360.net/api/v3/search/quake_service"}
	if got := c.userInfoURL(); got != "https://quake.360.net/api/v3/user/info" {
		t.Fatalf("userInfoURL = %q, want https://quake.360.net/api/v3/user/info", got)
	}
	// 自定义 host 也应同源推导。
	c2 := &Client{BaseURL: "https://example.com:8443/api/v3/search/quake_service"}
	if got := c2.userInfoURL(); got != "https://example.com:8443/api/v3/user/info" {
		t.Fatalf("userInfoURL = %q", got)
	}
}

func TestGetCredit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-QuakeToken") != "tok123" {
			t.Errorf("X-QuakeToken = %q, want tok123", r.Header.Get("X-QuakeToken"))
		}
		if r.URL.Path != "/api/v3/user/info" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"Successful.","data":{"credit":45734,"month_remaining_credit":1200,"constant_credit":0,"persistent_credit":0},"meta":{}}`))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL + "/api/v3/search/quake_service", HTTP: srv.Client()}
	ci, err := c.GetCredit("tok123")
	if err != nil {
		t.Fatalf("GetCredit 失败: %v", err)
	}
	if ci.Total != 45734 || ci.MonthRemaining != 1200 {
		t.Fatalf("Credit = %+v, want total=45734 month=1200", ci)
	}
}

func TestGetCreditError(t *testing.T) {
	// 非 0 code 应报错。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":40102,"message":"invalid token"}`))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL + "/api/v3/search/quake_service", HTTP: srv.Client()}
	if _, err := c.GetCredit("bad"); err == nil {
		t.Fatal("期望返回错误，实际成功")
	}

	// 401 状态码应报鉴权失败。
	srv401 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("/quake/login"))
	}))
	defer srv401.Close()
	c2 := &Client{BaseURL: srv401.URL + "/api/v3/search/quake_service", HTTP: srv401.Client()}
	if _, err := c2.GetCredit("bad"); err == nil {
		t.Fatal("401 应报错，实际成功")
	}
}
