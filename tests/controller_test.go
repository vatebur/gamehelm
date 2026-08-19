package tests

import (
	"context"
	"testing"
	"time"

	"github.com/vatebur/gamehelm/internal/app"
)

func TestReconcileAdoptsExternalStart(t *testing.T) {
	cfg := testConfig(t)
	runner := &fakeRunner{statuses: map[string]app.UnitStatus{
		"palworld.service": {Load: "loaded", Active: "active", Sub: "running"},
	}}
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.Local)
	controller := newTestController(t, cfg, runner, app.WithClock(func() time.Time { return now }))

	controller.Reconcile(context.Background(), "palworld")
	view := viewByID(t, controller, "palworld")
	if got, want := view.DeadlineUnix, now.Add(4*time.Hour).Unix(); got != want {
		t.Fatalf("deadline = %d, want %d", got, want)
	}
	if view.Notice == "" {
		t.Fatal("external start should leave a notice")
	}
}

func TestExtendOnlyDuringEachFinalHour(t *testing.T) {
	cfg := testConfig(t)
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.Local)
	runner := &fakeRunner{statuses: map[string]app.UnitStatus{
		"palworld.service": {Load: "loaded", Active: "inactive", Sub: "dead"},
	}}
	controller := newTestController(t, cfg, runner, app.WithClock(func() time.Time { return now }))
	if err := controller.Start(context.Background(), "palworld", "test"); err != nil {
		t.Fatalf("start: %v", err)
	}
	now = now.Add(3*time.Hour + 30*time.Minute)

	if err := controller.Extend(context.Background(), "palworld", "test"); err != nil {
		t.Fatalf("first extend: %v", err)
	}
	if err := controller.Extend(context.Background(), "palworld", "test"); err == nil {
		t.Fatal("second immediate extend should be rejected outside final hour")
	}
	now = now.Add(time.Hour)
	if err := controller.Extend(context.Background(), "palworld", "test"); err != nil {
		t.Fatalf("extend after entering next final hour: %v", err)
	}
	if got := viewByID(t, controller, "palworld").Extensions; got != 2 {
		t.Fatalf("extensions = %d, want 2", got)
	}
}

func TestExpiredServiceIsStopped(t *testing.T) {
	cfg := testConfig(t)
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.Local)
	runner := &fakeRunner{statuses: map[string]app.UnitStatus{
		"palworld.service": {Load: "loaded", Active: "inactive", Sub: "dead"},
	}}
	controller := newTestController(t, cfg, runner, app.WithClock(func() time.Time { return now }))
	if err := controller.Start(context.Background(), "palworld", "test"); err != nil {
		t.Fatalf("start: %v", err)
	}
	now = now.Add(5 * time.Hour)

	controller.Reconcile(context.Background(), "palworld")
	if len(runner.stops) != 1 || runner.stops[0] != "palworld.service" {
		t.Fatalf("stops = %#v", runner.stops)
	}
	if got := viewByID(t, controller, "palworld").DeadlineUnix; got != 0 {
		t.Fatalf("deadline remains after expiry: %d", got)
	}
}

func TestControllerRestartResetsActiveServiceDeadline(t *testing.T) {
	cfg := testConfig(t)
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.Local)
	runner := &fakeRunner{statuses: map[string]app.UnitStatus{
		"palworld.service": {Load: "loaded", Active: "active", Sub: "running"},
	}}
	first := newTestController(t, cfg, runner, app.WithClock(func() time.Time { return now }))
	first.Reconcile(context.Background(), "palworld")
	firstDeadline := viewByID(t, first, "palworld").DeadlineUnix

	now = now.Add(30 * time.Minute)
	restarted := newTestController(t, cfg, runner, app.WithClock(func() time.Time { return now }))
	restarted.Reconcile(context.Background(), "palworld")
	restartedDeadline := viewByID(t, restarted, "palworld").DeadlineUnix

	if got, want := restartedDeadline, now.Add(4*time.Hour).Unix(); got != want {
		t.Fatalf("deadline after restart = %d, want %d", got, want)
	}
	if restartedDeadline <= firstDeadline {
		t.Fatalf("deadline was not reset: first=%d restarted=%d", firstDeadline, restartedDeadline)
	}
}

func TestThreeServicesRunIndependently(t *testing.T) {
	cfg := testConfig(t)
	cfg.Services["factorio"] = app.ServiceConfig{DisplayName: "异星工厂", Unit: "factorio.service"}
	runner := &fakeRunner{statuses: map[string]app.UnitStatus{
		"factorio.service": {Load: "loaded", Active: "inactive", Sub: "dead"},
		"palworld.service": {Load: "loaded", Active: "inactive", Sub: "dead"},
		"terraria.service": {Load: "loaded", Active: "active", Sub: "running"},
	}}
	controller := newTestController(t, cfg, runner)
	controller.Reconcile(context.Background(), "terraria")

	if err := controller.Start(context.Background(), "factorio", "test"); err != nil {
		t.Fatalf("start factorio: %v", err)
	}
	if got := runner.statuses["terraria.service"].Active; got != "active" {
		t.Fatalf("terraria was changed: %s", got)
	}
	if got := len(controller.Views()); got != 3 {
		t.Fatalf("service views = %d, want 3", got)
	}
	if !viewByID(t, controller, "factorio").Running || !viewByID(t, controller, "terraria").Running {
		t.Fatal("independent services should both remain running")
	}
}

func TestServicesUseIndependentDurations(t *testing.T) {
	cfg := testConfig(t)
	palworld := cfg.Services["palworld"]
	palworld.RunDuration = "6h"
	palworld.ExtensionDuration = "30m"
	cfg.Services["palworld"] = palworld
	terraria := cfg.Services["terraria"]
	terraria.RunDuration = "infinite"
	terraria.ExtensionDuration = "2h"
	cfg.Services["terraria"] = terraria
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.Local)
	startedAt := now
	runner := &fakeRunner{statuses: map[string]app.UnitStatus{
		"palworld.service": {Load: "loaded", Active: "active", Sub: "running"},
		"terraria.service": {Load: "loaded", Active: "active", Sub: "running"},
	}}
	controller := newTestController(t, cfg, runner, app.WithClock(func() time.Time { return now }))

	controller.ReconcileAll(context.Background())
	palworldView := viewByID(t, controller, "palworld")
	if got, want := palworldView.DeadlineUnix, now.Add(6*time.Hour).Unix(); got != want {
		t.Fatalf("palworld deadline = %d, want %d", got, want)
	}
	if palworldView.RunDurationSeconds != int64((6*time.Hour)/time.Second) || palworldView.ExtensionDurationSeconds != int64((30*time.Minute)/time.Second) {
		t.Fatalf("palworld durations = %d/%d", palworldView.RunDurationSeconds, palworldView.ExtensionDurationSeconds)
	}
	now = now.Add(5*time.Hour + 31*time.Minute)
	if err := controller.Extend(context.Background(), "palworld", "test"); err != nil {
		t.Fatalf("custom extension: %v", err)
	}
	if got, want := viewByID(t, controller, "palworld").DeadlineUnix, startedAt.Add(6*time.Hour+30*time.Minute).Unix(); got != want {
		t.Fatalf("extended deadline = %d, want %d", got, want)
	}
	if err := controller.Extend(context.Background(), "palworld", "test"); err == nil {
		t.Fatal("immediate repeated custom extension was accepted")
	}

	terrariaView := viewByID(t, controller, "terraria")
	if !terrariaView.Unlimited || terrariaView.DeadlineUnix != 0 || terrariaView.RunDurationSeconds != 0 || terrariaView.CanExtend {
		t.Fatalf("unexpected unlimited view: %+v", terrariaView)
	}
}

func TestUnlimitedServiceNeverExpiresOrExtends(t *testing.T) {
	cfg := testConfig(t)
	service := cfg.Services["palworld"]
	service.RunDuration = "infinite"
	cfg.Services["palworld"] = service
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.Local)
	runner := &fakeRunner{statuses: map[string]app.UnitStatus{
		"palworld.service": {Load: "loaded", Active: "inactive", Sub: "dead"},
	}}
	controller := newTestController(t, cfg, runner, app.WithClock(func() time.Time { return now }))
	if err := controller.Start(context.Background(), "palworld", "test"); err != nil {
		t.Fatalf("start: %v", err)
	}
	now = now.Add(365 * 24 * time.Hour)
	controller.Reconcile(context.Background(), "palworld")
	if len(runner.stops) != 0 {
		t.Fatalf("unlimited service was stopped: %#v", runner.stops)
	}
	if err := controller.Extend(context.Background(), "palworld", "test"); err == nil {
		t.Fatal("unlimited service accepted an extension")
	}
}
