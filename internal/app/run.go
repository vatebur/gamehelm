package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func Run(configPath string) error {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}
	logger := log.New(os.Stdout, "", 0)

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

func Check(configPath string) error {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}
	runner := systemdRunner{}
	for _, id := range cfg.serviceIDs() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		status, statusErr := runner.Status(ctx, cfg.Services[id].Unit)
		cancel()
		if statusErr != nil {
			return fmt.Errorf("检查 user service %s: %w", cfg.Services[id].Unit, statusErr)
		}
		if status.Load != "loaded" {
			return fmt.Errorf("user service %s 未加载", cfg.Services[id].Unit)
		}
	}
	return nil
}
