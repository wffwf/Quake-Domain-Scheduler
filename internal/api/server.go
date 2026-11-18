// Package api 提供 HTTP 路由、中间件与 JSON API Handler。
package api

import (
	"crypto/subtle"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"kongtan/internal/config"
	"kongtan/internal/db"
	"kongtan/internal/probe"
	"kongtan/internal/quake"
)

// Server 聚合 HTTP 服务依赖。
type Server struct {
	store       *db.Store
	engine      *probe.Engine
	quakeClient *quake.Client
	cfg         *config.Config
	log         *slog.Logger
	webFS       fs.FS

	// 剩余点数缓存（内存态，通过 RefreshCredits 更新）。
	creditsMu             sync.Mutex
	creditsTotal          int
	creditsMonthRemaining int
	creditsUpdatedAt      string
}

// New 构造 Server 并装配路由。
func New(store *db.Store, engine *probe.Engine, quakeClient *quake.Client, cfg *config.Config, log *slog.Logger, webFS fs.FS) *Server {
	s := &Server{store: store, engine: engine, quakeClient: quakeClient, cfg: cfg, log: log, webFS: webFS}
	return s
}

// Handler 返回带中间件的根 Handler。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// 页面与静态资源（公开）。
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServerFS(s.webFS)))

	// API。
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /api/overview", s.handleOverview)
	apiMux.HandleFunc("GET /api/config", s.handleConfig)
	apiMux.HandleFunc("POST /api/credits/refresh", s.handleCreditsRefresh)

	apiMux.HandleFunc("GET /api/groups", s.handleListGroups)
	apiMux.HandleFunc("POST /api/groups", s.handleCreateGroup)
	apiMux.HandleFunc("PUT /api/groups/{id}", s.handleUpdateGroup)
	apiMux.HandleFunc("DELETE /api/groups/{id}", s.handleDeleteGroup)
	apiMux.HandleFunc("GET /api/groups/{id}/domains", s.handleListGroupDomains)

	apiMux.HandleFunc("POST /api/domains", s.handleAddDomains)
	apiMux.HandleFunc("PUT /api/domains/{id}", s.handleUpdateDomain)
	apiMux.HandleFunc("DELETE /api/domains/{id}", s.handleDeleteDomain)

	apiMux.HandleFunc("GET /api/apikeys", s.handleListAPIKeys)
	apiMux.HandleFunc("POST /api/apikeys", s.handleCreateAPIKey)
	apiMux.HandleFunc("PUT /api/apikeys/{id}", s.handleUpdateAPIKey)
	apiMux.HandleFunc("DELETE /api/apikeys/{id}", s.handleDeleteAPIKey)
	apiMux.HandleFunc("POST /api/apikeys/reorder", s.handleReorderAPIKeys)
	apiMux.HandleFunc("POST /api/apikeys/{id}/test", s.handleTestAPIKey)

	apiMux.HandleFunc("GET /api/blacklist", s.handleListBlacklist)
	apiMux.HandleFunc("POST /api/blacklist", s.handleAddBlacklist)
	apiMux.HandleFunc("DELETE /api/blacklist/{id}", s.handleDeleteBlacklist)

	apiMux.HandleFunc("GET /api/tasks", s.handleListTasks)
	apiMux.HandleFunc("POST /api/tasks/run", s.handleRunTask)

	apiMux.HandleFunc("GET /api/schedule", s.handleSchedule)
	apiMux.HandleFunc("POST /api/scheduler/selftest", s.handleSchedulerSelfTest)

	apiMux.HandleFunc("GET /api/urls", s.handleListURLs)
	apiMux.HandleFunc("GET /api/subdomains", s.handleListSubdomains)
	apiMux.HandleFunc("GET /api/export/urls", s.handleExportURLs)
	apiMux.HandleFunc("GET /api/export/subdomains", s.handleExportSubdomains)

	mux.Handle("/api/", apiMux)

	return s.logging(s.recover(s.securityHeaders(s.auth(mux))))
}

// ---------- 中间件 ----------

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panic recovered", "err", rec, "stack", string(debug.Stack()))
				writeErr(w, http.StatusInternalServerError, "服务器内部错误")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		s.log.Info("http", "method", r.Method, "path", r.URL.Path, "status", sw.status,
			"dur_ms", time.Since(start).Milliseconds(), "remote", r.RemoteAddr)
	})
}

// auth 对 /api/* 应用 Bearer 鉴权（api_key 为空则不鉴权）。
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") || s.cfg.Server.APIKey == "" {
			next.ServeHTTP(w, r)
			return
		}
		got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.Server.APIKey)) != 1 {
			writeErr(w, http.StatusUnauthorized, "未授权：请提供正确的 API Key")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// ---------- 响应工具 ----------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeOK(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": data})
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": msg})
}

// parseJSON 解析请求体，失败时写错误响应并返回 false。
func parseJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return false
	}
	return true
}

// pathID 解析路径参数 {id}。
func pathID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id
}
