package tests

import (
	"context"
	"io"
	"log"
	"sync"
	"testing"

	"github.com/vatebur/gamehelm/internal/config"
	"github.com/vatebur/gamehelm/internal/control"
	"github.com/vatebur/gamehelm/internal/runtime"
)

type fakeRunner struct {
	mu         sync.Mutex
	statuses   map[string]runtime.Snapshot
	starts     []string
	stops      []string
	startErr   error
	stopErr    error
	inspectErr error
}

func (f *fakeRunner) Inspect(_ context.Context, unit string) (runtime.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statuses[unit], f.inspectErr
}

func (f *fakeRunner) Start(_ context.Context, unit string, expected runtime.Snapshot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts = append(f.starts, unit)
	if f.startErr == nil {
		f.statuses[unit] = runtime.Snapshot{State: runtime.Running, TargetID: expected.TargetID, RunID: "started"}
	}
	return f.startErr
}

func (f *fakeRunner) Stop(_ context.Context, unit string, expected runtime.Snapshot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops = append(f.stops, unit)
	if f.stopErr == nil {
		f.statuses[unit] = runtime.Snapshot{State: runtime.Stopped, TargetID: expected.TargetID, RunID: expected.RunID}
	}
	return f.stopErr
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Config{
		Web:  config.WebConfig{Listen: "127.0.0.1:0"},
		Auth: config.AuthConfig{Password: "test-password"},
		Services: map[string]config.Service{
			"palworld": {DisplayName: "帕鲁世界", Runtime: "systemd", Unit: "palworld.service"},
			"terraria": {DisplayName: "泰拉瑞亚", Runtime: "systemd", Unit: "terraria.service"},
		},
	}
}

func newTestController(t *testing.T, cfg config.Config, runner runtime.Driver, options ...control.Option) *control.Controller {
	t.Helper()
	return control.New(cfg, map[string]runtime.Driver{"systemd": runner, "docker": runner}, log.New(io.Discard, "", 0), options...)
}

func viewByID(t *testing.T, controller *control.Controller, id string) control.ServiceView {
	t.Helper()
	for _, view := range controller.Views() {
		if view.ID == id {
			return view
		}
	}
	t.Fatalf("service view %q not found", id)
	return control.ServiceView{}
}
