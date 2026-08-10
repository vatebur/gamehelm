package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func Run(configPath string, printInstallValues bool) error {
	cfg, err := loadConfig(configPath)
	if err != nil {
		logStartupError(configPath, err)
		return err
	}
	if printInstallValues {
		for _, id := range cfg.serviceIDs() {
			fmt.Println(cfg.Services[id].Unit)
		}
		return nil
	}
	writer, err := newRotatingWriter(cfg.LogFile)
	if err != nil {
		return fmt.Errorf("打开日志文件: %w", err)
	}
	defer writer.Close()
	logger := log.New(writer, "", log.Ldate|log.Ltime|log.Lmicroseconds)

	ctrl, err := newController(cfg, systemdRunner{}, logger)
	if err != nil {
		return fmt.Errorf("初始化控制器: %w", err)
	}
	app, err := newAppServer(cfg, ctrl, logger)
	if err != nil {
		return fmt.Errorf("初始化页面: %w", err)
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
		return fmt.Errorf("HTTP 服务异常退出: %w", err)
	}
	logger.Printf("Web 控制服务已停止")
	return nil
}

func logStartupError(configPath string, startupErr error) {
	dir := filepath.Dir(configPath)
	path := filepath.Join(dir, "gamehelm.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	logger := log.New(f, "", log.LstdFlags)
	logger.Printf("读取配置失败: %v", startupErr)
}
