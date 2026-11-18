// 空探平台（Quake 资产探测与日常信息收集系统）入口。
package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
	_ "time/tzdata" // 内嵌时区数据，确保跨平台 LoadLocation 可用

	"kongtan/internal/api"
	"kongtan/internal/config"
	"kongtan/internal/db"
	"kongtan/internal/probe"
	"kongtan/internal/quake"
	"kongtan/internal/scheduler"
	filesync "kongtan/internal/sync"
)

//go:embed all:web
var webFS embed.FS

func main() {
	cfgPath := "config.yaml"
	useDefaultPath := true
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
		useDefaultPath = false
	}

	// 早期阶段（尚未加载配置、日志文件未就绪）：仅使用控制台输出。
	// §4.1 启动时自动释放配置模板（仅针对当前工作目录下默认的 config.yaml）。
	if useDefaultPath {
		created, absPath, err := config.EnsureFile(cfgPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "初始化配置文件失败:", err)
			os.Exit(1)
		}
		if created {
			fmt.Println("未检测到 config.yaml，已生成默认配置模板：" + absPath + "，请按需修改后重启。")
			os.Exit(0)
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "加载配置失败:", err)
		os.Exit(1)
	}
	absCfg, _ := filepath.Abs(cfgPath)

	// 打开日志文件：全部结构化日志（含 HTTP 访问日志）落盘，控制台保持干净。
	logFile, err := openLogFile(cfg.Server.LogFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "打开日志文件失败:", err)
		os.Exit(1)
	}
	defer logFile.Close()

	logger := slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	// announce 同时输出到控制台（人类可读）与日志文件（落盘）。
	announce := func(msg string) {
		fmt.Println(msg)
		logger.Info(msg)
	}
	announcef := func(format string, args ...any) {
		announce(fmt.Sprintf(format, args...))
	}

	announce("已加载配置文件：" + absCfg)

	store, err := db.Open(cfg.Storage.DBPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "初始化数据库失败:", err)
		logger.Error("初始化数据库失败", "err", err.Error())
		os.Exit(1)
	}
	defer store.Close()

	// 启动导入：API Token 与域名 txt（仅当对应数据为空时）。
	if n, err := filesync.ImportAPIKeys(store, cfg.Storage.InputDir); err != nil {
		logger.Warn("导入 apis.txt 失败", "err", err.Error())
	} else if n > 0 {
		announcef("已从 apis.txt 导入 API Token（%d 个）", n)
	}
	// apis.txt 缺失/为空时，回退从 config.yaml 的 quake.apis 导入。
	if n, err := filesync.ImportConfigAPIs(store, cfg.Quake.APIs); err != nil {
		logger.Warn("从 config.yaml 导入 API 失败", "err", err.Error())
	} else if n > 0 {
		announcef("已从 config.yaml 导入 API Token（%d 个）", n)
	}
	if n, err := filesync.ImportDomains(store, cfg.Storage.InputDir); err != nil {
		logger.Warn("导入域名 txt 失败", "err", err.Error())
	} else if n > 0 {
		announcef("已从域名 txt 导入域名（%d 个）", n)
	}

	quakeClient := quake.New(cfg.Quake.BaseURL, cfg.Quake.TimeoutSec, cfg.Quake.MaxRetries, logger)
	engine := probe.New(store, quakeClient, cfg, logger)

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		fmt.Fprintln(os.Stderr, "加载前端资源失败:", err)
		logger.Error("加载前端资源失败", "err", err.Error())
		os.Exit(1)
	}
	server := api.New(store, engine, quakeClient, cfg, logger, sub)

	// 后台首次查询各账号剩余点数（尽力而为，不阻塞启动；失败可稍后在页面手动刷新）。
	go func() {
		if _, _, _, err := server.RefreshCredits(); err != nil {
			logger.Warn("首次查询剩余点数失败", "err", err.Error())
		}
	}()

	httpSrv := &http.Server{
		Addr:         cfg.ListenAddr(),
		Handler:      server.Handler(),
		ReadTimeout:  time.Duration(cfg.Server.ReadTimeoutSec) * time.Second,
		WriteTimeout: time.Duration(cfg.Server.WriteTimeoutSec) * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 定时调度（可选）。
	if cfg.Schedule.Enabled {
		sch, err := scheduler.New(engine, store, cfg, logger)
		if err != nil {
			fmt.Fprintln(os.Stderr, "初始化定时调度失败:", err)
			logger.Error("初始化定时调度失败", "err", err.Error())
		} else {
			announcef("定时调度已启动 timezone=%s window_hours=%d", cfg.Schedule.Timezone, cfg.Schedule.MonthlyWindowHours)
			go sch.Start(ctx)
		}
	}

	go func() {
		announcef("空探平台已启动 url=http://%s", cfg.ListenAddr())
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintln(os.Stderr, "服务异常退出:", err)
			logger.Error("服务异常退出", "err", err.Error())
			stop()
		}
	}()

	<-ctx.Done()
	announce("定时调度已停止")
	announce("正在关闭服务...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	announce("已退出")
}

// openLogFile 打开（必要时创建）追加写日志文件。
func openLogFile(path string) (*os.File, error) {
	if path == "" {
		path = "./data/kongtan.log"
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return f, nil
}
