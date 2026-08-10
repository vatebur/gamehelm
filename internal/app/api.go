package app

import (
	"context"
	"log"
	"net/http"
	"time"
)

type UnitStatus = unitStatus
type CommandRunner = commandRunner
type Controller = controller
type ServiceView = serviceView

type ControllerOption func(*controller)

func WithClock(now func() time.Time) ControllerOption {
	return func(c *controller) { c.now = now }
}

func LoadConfig(path string) (Config, error) {
	return loadConfig(path)
}

func (c *Config) Validate(baseDir string) error {
	return c.validate(baseDir)
}

func (c Config) ServiceIDs() []string {
	return c.serviceIDs()
}

func NewController(cfg Config, runner CommandRunner, logger *log.Logger, options ...ControllerOption) (*Controller, error) {
	controller, err := newController(cfg, runner, logger)
	if err != nil {
		return nil, err
	}
	for _, option := range options {
		option(controller)
	}
	return controller, nil
}

func (c *controller) Reconcile(ctx context.Context, id string) {
	c.reconcile(ctx, id)
}

func (c *controller) ReconcileAll(ctx context.Context) {
	c.reconcileAll(ctx)
}

func (c *controller) Start(ctx context.Context, id, sourceIP string) error {
	return c.start(ctx, id, sourceIP)
}

func (c *controller) Stop(ctx context.Context, id, sourceIP string) error {
	return c.stop(ctx, id, sourceIP)
}

func (c *controller) Extend(ctx context.Context, id, sourceIP string) error {
	return c.extend(ctx, id, sourceIP)
}

func (c *controller) Views() []ServiceView {
	return c.views()
}

func (c *controller) ServiceIDs() []string {
	return append([]string(nil), c.ids...)
}

type AppServer = appServer

func NewAppServer(cfg Config, ctrl *Controller, logger *log.Logger) (*AppServer, error) {
	return newAppServer(cfg, ctrl, logger)
}

func (s *appServer) Handler() http.Handler {
	return s.handler()
}
