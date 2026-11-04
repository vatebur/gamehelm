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

	"github.com/vatebur/gamehelm/internal/config"
	"github.com/vatebur/gamehelm/internal/control"
	"github.com/vatebur/gamehelm/internal/runtime"
	"github.com/vatebur/gamehelm/internal/web"
)

func Run(configPath string) error {
	cfg, release, err := config.Prepare(configPath)
	if err != nil {
		return err
	}
	defer release()
	logger := log.New(os.Stdout, "", 0)
	drivers, closeDrivers, err := runtime.NewDrivers(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = closeDrivers() }()
	ctrl := control.New(cfg, drivers, logger)
	app, err := web.New(cfg, ctrl, logger)
	if err != nil {
		return fmt.Errorf("初始化页面: %w", err)
	}

	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctrl.BindContext(rootCtx)
	controllerDone := make(chan struct{})
	go func() {
		defer close(controllerDone)
		ctrl.Run(rootCtx)
	}()

	server := &http.Server{
		Addr:              cfg.Web.Listen,
		Handler:           app.Handler(),
		ErrorLog:          logger,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       90 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-rootCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Printf("HTTP 服务停止异常: %v", err)
			_ = server.Close()
		}
	}()

	logger.Printf("GameHelm 2.0 启动 listen=%s", cfg.Web.Listen)
	serveErr := server.ListenAndServe()
	stop()
	<-shutdownDone
	<-controllerDone
	ctrl.Wait()
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return fmt.Errorf("HTTP 服务异常退出: %w", serveErr)
	}
	logger.Printf("Web 控制服务已停止")
	return nil
}

func Check(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	drivers, closeDrivers, err := runtime.NewDrivers(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = closeDrivers() }()
	var failures []error
	for _, id := range cfg.ServiceIDs() {
		service := cfg.Services[id]
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		status, statusErr := drivers[service.Runtime].Inspect(ctx, service.Target())
		cancel()
		if statusErr != nil {
			failures = append(failures, fmt.Errorf("检查游戏 %s: %w", id, statusErr))
		} else if status.State == runtime.Abnormal {
			failures = append(failures, fmt.Errorf("游戏 %s 状态异常", id))
		}
	}
	return errors.Join(failures...)
}
