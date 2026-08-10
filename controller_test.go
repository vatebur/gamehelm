package main

import (
	"context"
	"io"
	"log"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeRunner struct {
	mu       sync.Mutex
	statuses map[string]unitStatus
	starts   []string
	stops    []string
	startErr error
	stopErr  error
}

func (f *fakeRunner) Status(_ context.Context, unit string) (unitStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statuses[unit], nil
}

func (f *fakeRunner) Start(_ context.Context, unit string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts = append(f.starts, unit)
	if f.startErr == nil {
		f.statuses[unit] = unitStatus{Load: "loaded", Active: "active", Sub: "running"}
	}
	return f.startErr
}

func (f *fakeRunner) Stop(_ context.Context, unit string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops = append(f.stops, unit)
	if f.stopErr == nil {
		f.statuses[unit] = unitStatus{Load: "loaded", Active: "inactive", Sub: "dead"}
	}
	return f.stopErr
}

func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		Listen:    "127.0.0.1:0",
		Password:  "test-password",
		StateFile: filepath.Join(t.TempDir(), "state.json"),
		LogFile:   filepath.Join(t.TempDir(), "webctrl.log"),
		Services: map[string]ServiceConfig{
			"palworld": {DisplayName: "帕鲁世界", Unit: "palworld.service"},
			"terraria": {DisplayName: "泰拉瑞亚", Unit: "terraria.service"},
		},
	}
}

func newTestController(t *testing.T, cfg Config, runner commandRunner) *controller {
	t.Helper()
	c, err := newController(cfg, runner, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestReconcileAdoptsExternalStart(t *testing.T) {
	cfg := testConfig(t)
	runner := &fakeRunner{statuses: map[string]unitStatus{
		"palworld.service": {Load: "loaded", Active: "active", Sub: "running"},
		"terraria.service": {Load: "loaded", Active: "inactive", Sub: "dead"},
	}}
	c := newTestController(t, cfg, runner)
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.Local)
	c.now = func() time.Time { return now }

	c.reconcile(context.Background(), "palworld")
	record := c.state.Services["palworld"]
	if got, want := record.DeadlineUnix, now.Add(4*time.Hour).Unix(); got != want {
		t.Fatalf("deadline = %d, want %d", got, want)
	}
	if record.Notice == "" {
		t.Fatal("external start should leave a notice")
	}
}

func TestExtendOnlyDuringEachFinalHour(t *testing.T) {
	cfg := testConfig(t)
	runner := &fakeRunner{statuses: map[string]unitStatus{
		"palworld.service": {Load: "loaded", Active: "active", Sub: "running"},
	}}
	c := newTestController(t, cfg, runner)
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.Local)
	c.now = func() time.Time { return now }
	c.state.Services["palworld"] = timerRecord{StartedUnix: now.Add(-3 * time.Hour).Unix(), DeadlineUnix: now.Add(30 * time.Minute).Unix()}

	if err := c.extend(context.Background(), "palworld", "test"); err != nil {
		t.Fatalf("first extend: %v", err)
	}
	if err := c.extend(context.Background(), "palworld", "test"); err == nil {
		t.Fatal("second immediate extend should be rejected outside final hour")
	}
	now = now.Add(time.Hour)
	if err := c.extend(context.Background(), "palworld", "test"); err != nil {
		t.Fatalf("extend after entering next final hour: %v", err)
	}
	if got := c.state.Services["palworld"].Extensions; got != 2 {
		t.Fatalf("extensions = %d, want 2", got)
	}
}

func TestExpiredServiceIsStopped(t *testing.T) {
	cfg := testConfig(t)
	runner := &fakeRunner{statuses: map[string]unitStatus{
		"palworld.service": {Load: "loaded", Active: "active", Sub: "running"},
	}}
	c := newTestController(t, cfg, runner)
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.Local)
	c.now = func() time.Time { return now }
	c.state.Services["palworld"] = timerRecord{StartedUnix: now.Add(-5 * time.Hour).Unix(), DeadlineUnix: now.Add(-time.Hour).Unix()}

	c.reconcile(context.Background(), "palworld")
	if len(runner.stops) != 1 || runner.stops[0] != "palworld.service" {
		t.Fatalf("stops = %#v", runner.stops)
	}
	if got := c.state.Services["palworld"].DeadlineUnix; got != 0 {
		t.Fatalf("deadline remains after expiry: %d", got)
	}
}

func TestServicesCanRunIndependently(t *testing.T) {
	cfg := testConfig(t)
	runner := &fakeRunner{statuses: map[string]unitStatus{
		"palworld.service": {Load: "loaded", Active: "inactive", Sub: "dead"},
		"terraria.service": {Load: "loaded", Active: "active", Sub: "running"},
	}}
	c := newTestController(t, cfg, runner)
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.Local)
	c.now = func() time.Time { return now }
	c.state.Services["terraria"] = timerRecord{StartedUnix: now.Unix(), DeadlineUnix: now.Add(4 * time.Hour).Unix()}

	if err := c.start(context.Background(), "palworld", "test"); err != nil {
		t.Fatalf("start palworld: %v", err)
	}
	if got := runner.statuses["terraria.service"].Active; got != "active" {
		t.Fatalf("terraria was changed: %s", got)
	}
	if c.state.Services["palworld"].DeadlineUnix == 0 || c.state.Services["terraria"].DeadlineUnix == 0 {
		t.Fatal("both independent timers should remain active")
	}
}
