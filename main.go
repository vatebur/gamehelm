package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	configPath := flag.String("config", "config.json", "配置文件路径")
	printInstallValues := flag.Bool("print-install-values", false, "输出安装模板所需的非敏感配置")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		logStartupError(*configPath, err)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *printInstallValues {
		fmt.Println(cfg.Services["palworld"].Unit)
		fmt.Println(cfg.Services["terraria"].Unit)
		return
	}
	writer, err := newRotatingWriter(cfg.LogFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "打开日志文件: %v\n", err)
		os.Exit(1)
	}
	defer writer.Close()
	logger := log.New(writer, "", log.Ldate|log.Ltime|log.Lmicroseconds)

	ctrl, err := newController(cfg, systemdRunner{}, logger)
	if err != nil {
		logger.Fatalf("初始化控制器失败: %v", err)
	}
	app, err := newAppServer(cfg, ctrl, logger)
	if err != nil {
		logger.Fatalf("初始化页面失败: %v", err)
	}

	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctrl.reconcileAll(rootCtx)
	go ctrl.run(rootCtx)

	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           app.handler(),
		ErrorLog:          logger,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       90 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	go func() {
		<-rootCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Printf("HTTP 服务停止异常: %v", err)
		}
	}()

	logger.Printf("Web 控制服务启动 listen=%s", cfg.Listen)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Fatalf("HTTP 服务异常退出: %v", err)
	}
	logger.Printf("Web 控制服务已停止")
}

func logStartupError(configPath string, startupErr error) {
	dir := filepath.Dir(configPath)
	path := filepath.Join(dir, "webctrl.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	logger := log.New(f, "", log.LstdFlags)
	logger.Printf("读取配置失败: %v", startupErr)
}
