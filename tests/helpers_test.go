package tests

import (
	"context"
	"io"
	"log"
	"sync"
	"testing"

	"github.com/vatebur/gamehelm/internal/app"
)

type fakeRunner struct {
	mu       sync.Mutex
	statuses map[string]app.UnitStatus
	starts   []string
	stops    []string
	startErr error
	stopErr  error
}

func (f *fakeRunner) Status(_ context.Context, unit string) (app.UnitStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statuses[unit], nil
}

func (f *fakeRunner) Start(_ context.Context, unit string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts = append(f.starts, unit)
	if f.startErr == nil {
		f.statuses[unit] = app.UnitStatus{Load: "loaded", Active: "active", Sub: "running"}
	}
	return f.startErr
}

func (f *fakeRunner) Stop(_ context.Context, unit string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops = append(f.stops, unit)
	if f.stopErr == nil {
		f.statuses[unit] = app.UnitStatus{Load: "loaded", Active: "inactive", Sub: "dead"}
	}
	return f.stopErr
}

func testConfig(t *testing.T) app.Config {
	t.Helper()
	return app.Config{
		Listen:   "127.0.0.1:0",
		Password: "test-password",
		Services: map[string]app.ServiceConfig{
			"palworld": {DisplayName: "帕鲁世界", Unit: "palworld.service"},
			"terraria": {DisplayName: "泰拉瑞亚", Unit: "terraria.service"},
		},
	}
}

func newTestController(t *testing.T, cfg app.Config, runner app.CommandRunner, options ...app.ControllerOption) *app.Controller {
	t.Helper()
	return app.NewController(cfg, runner, log.New(io.Discard, "", 0), options...)
}

func viewByID(t *testing.T, controller *app.Controller, id string) app.ServiceView {
	t.Helper()
	for _, view := range controller.Views() {
		if view.ID == id {
			return view
		}
	}
	t.Fatalf("service view %q not found", id)
	return app.ServiceView{}
}
