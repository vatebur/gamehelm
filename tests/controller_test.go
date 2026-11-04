package tests

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vatebur/gamehelm/internal/config"
	"github.com/vatebur/gamehelm/internal/control"
	"github.com/vatebur/gamehelm/internal/runtime"
)

func TestRuntimeRestartResetsDeadlineAndExtensions(t *testing.T) {
	cfg := testConfig(t)
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	runner := &fakeRunner{statuses: map[string]runtime.Snapshot{
		"palworld.service": {State: runtime.Running, TargetID: "container", RunID: "first"},
	}}
	controller := newTestController(t, cfg, runner, control.WithClock(func() time.Time { return now }))
	controller.Reconcile(context.Background(), "palworld")
	now = now.Add(3*time.Hour + 30*time.Minute)
	if err := controller.Extend(context.Background(), "palworld", "test"); err != nil {
		t.Fatal(err)
	}
	runner.statuses["palworld.service"] = runtime.Snapshot{State: runtime.Running, TargetID: "container", RunID: "second"}
	controller.Reconcile(context.Background(), "palworld")
	view := viewByID(t, controller, "palworld")
	if view.Extensions != 0 || view.DeadlineUnix != now.Add(4*time.Hour).Unix() {
		t.Fatalf("new run retained old timing: %+v", view)
	}
}

func TestOutagePreservesDeadlineAndRecoveryStopsSameExpiredRun(t *testing.T) {
	cfg := testConfig(t)
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	runner := &fakeRunner{statuses: map[string]runtime.Snapshot{
		"palworld.service": {State: runtime.Running, TargetID: "container", RunID: "first"},
	}}
	controller := newTestController(t, cfg, runner, control.WithClock(func() time.Time { return now }))
	controller.Reconcile(context.Background(), "palworld")
	deadline := viewByID(t, controller, "palworld").DeadlineUnix
	now = now.Add(5 * time.Hour)
	runner.inspectErr = errors.New("offline")
	controller.Reconcile(context.Background(), "palworld")
	view := viewByID(t, controller, "palworld")
	if view.Status != "异常" || view.DeadlineUnix != deadline || len(runner.stops) != 0 {
		t.Fatalf("outage changed timer or stopped unconfirmed run: %+v", view)
	}
	runner.inspectErr = nil
	controller.Reconcile(context.Background(), "palworld")
	if len(runner.stops) != 1 || viewByID(t, controller, "palworld").DeadlineUnix != 0 {
		t.Fatal("recovered expired run was not stopped")
	}
}

func TestGameHelmRestartAfterOldDeadlineAdoptsFullDuration(t *testing.T) {
	cfg := testConfig(t)
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	runner := &fakeRunner{statuses: map[string]runtime.Snapshot{
		"palworld.service": {State: runtime.Running, TargetID: "container", RunID: "first"},
	}}
	first := newTestController(t, cfg, runner, control.WithClock(func() time.Time { return now }))
	first.Reconcile(context.Background(), "palworld")
	now = now.Add(5 * time.Hour)
	restarted := newTestController(t, cfg, runner, control.WithClock(func() time.Time { return now }))
	restarted.Reconcile(context.Background(), "palworld")
	if len(runner.stops) != 0 || viewByID(t, restarted, "palworld").DeadlineUnix != now.Add(4*time.Hour).Unix() {
		t.Fatal("restart should adopt a complete new duration")
	}
}

func TestFailedStopPreservesRunningDeadline(t *testing.T) {
	cfg := testConfig(t)
	runner := &fakeRunner{stopErr: errors.New("unavailable"), statuses: map[string]runtime.Snapshot{
		"palworld.service": {State: runtime.Running, TargetID: "container", RunID: "first"},
	}}
	controller := newTestController(t, cfg, runner)
	controller.Reconcile(context.Background(), "palworld")
	deadline := viewByID(t, controller, "palworld").DeadlineUnix
	if err := controller.Stop(context.Background(), "palworld", "test"); err == nil {
		t.Fatal("stop failure was ignored")
	}
	view := viewByID(t, controller, "palworld")
	if view.DeadlineUnix != deadline || view.Status != "异常" {
		t.Fatalf("failed stop changed running deadline: %+v", view)
	}
}

type unconfirmedStopRunner struct{ *fakeRunner }

func (*unconfirmedStopRunner) Stop(context.Context, string, runtime.Snapshot) error { return nil }

func TestUnconfirmedStopPreservesRunningDeadline(t *testing.T) {
	runner := &unconfirmedStopRunner{fakeRunner: &fakeRunner{statuses: map[string]runtime.Snapshot{
		"palworld.service": {State: runtime.Running, TargetID: "container", RunID: "first"},
	}}}
	controller := newTestController(t, testConfig(t), runner)
	controller.Reconcile(context.Background(), "palworld")
	deadline := viewByID(t, controller, "palworld").DeadlineUnix
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := controller.Stop(ctx, "palworld", "test"); err == nil {
		t.Fatal("unconfirmed stop was reported successful")
	}
	if viewByID(t, controller, "palworld").DeadlineUnix != deadline {
		t.Fatal("unconfirmed stop discarded deadline")
	}
}

type blockingStopRunner struct {
	*fakeRunner
	entered chan struct{}
	release chan struct{}
}

func (r *blockingStopRunner) Stop(ctx context.Context, target string, expected runtime.Snapshot) error {
	close(r.entered)
	select {
	case <-r.release:
		return r.fakeRunner.Stop(ctx, target, expected)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestAsyncStopKeepsOtherTargetsAvailable(t *testing.T) {
	cfg := testConfig(t)
	runner := &blockingStopRunner{fakeRunner: &fakeRunner{statuses: map[string]runtime.Snapshot{
		"palworld.service": {State: runtime.Running, TargetID: "first", RunID: "first"},
		"terraria.service": {State: runtime.Stopped, TargetID: "second"},
	}}, entered: make(chan struct{}), release: make(chan struct{})}
	controller := newTestController(t, cfg, runner)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	controller.BindContext(ctx)
	controller.ReconcileAll(ctx)
	accepted, err := controller.Action("palworld", "stop", "test")
	if !accepted || err != nil {
		t.Fatalf("stop not accepted: %t %v", accepted, err)
	}
	select {
	case <-runner.entered:
	case <-time.After(time.Second):
		t.Fatal("background stop did not begin")
	}
	if accepted, err := controller.Action("palworld", "stop", "test"); !accepted || err != nil {
		t.Fatalf("duplicate stop not reused: %t %v", accepted, err)
	}
	if _, err := controller.Action("palworld", "start", "test"); err == nil {
		t.Fatal("conflicting action was accepted")
	}
	if err := controller.Start(ctx, "terraria", "test"); err != nil {
		t.Fatal(err)
	}
	if !viewByID(t, controller, "terraria").Running || viewByID(t, controller, "palworld").Busy == "" {
		t.Fatal("slow stop blocked independent target")
	}
	close(runner.release)
	deadline := time.Now().Add(time.Second)
	for viewByID(t, controller, "palworld").Busy != "" {
		if time.Now().After(deadline) {
			t.Fatal("stop did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	if viewByID(t, controller, "palworld").DeadlineUnix != 0 {
		t.Fatal("confirmed stop retained deadline")
	}
}

func TestWaitDrainsCanceledAsyncActions(t *testing.T) {
	runner := &blockingStopRunner{fakeRunner: &fakeRunner{statuses: map[string]runtime.Snapshot{
		"palworld.service": {State: runtime.Running, TargetID: "first", RunID: "first"},
	}}, entered: make(chan struct{}), release: make(chan struct{})}
	controller := newTestController(t, testConfig(t), runner)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	controller.BindContext(ctx)
	accepted, err := controller.Action("palworld", "stop", "test")
	if !accepted || err != nil {
		t.Fatalf("stop not accepted: %t %v", accepted, err)
	}
	select {
	case <-runner.entered:
	case <-time.After(time.Second):
		t.Fatal("background stop did not begin")
	}
	cancel()
	done := make(chan struct{})
	go func() { controller.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("accepted operation did not drain after cancellation")
	}
	if viewByID(t, controller, "palworld").Busy != "" {
		t.Fatal("operation remained busy after Wait")
	}
}

type delayedInspectRunner struct {
	*fakeRunner
	entered chan struct{}
	release chan struct{}
}

func (r *delayedInspectRunner) Inspect(ctx context.Context, target string) (runtime.Snapshot, error) {
	close(r.entered)
	select {
	case <-r.release:
		return r.fakeRunner.Inspect(ctx, target)
	case <-ctx.Done():
		return runtime.Snapshot{}, ctx.Err()
	}
}

func TestWaitRejectsActionWhosePreparationFinishesLate(t *testing.T) {
	runner := &delayedInspectRunner{fakeRunner: &fakeRunner{statuses: map[string]runtime.Snapshot{
		"palworld.service": {State: runtime.Stopped, TargetID: "first"},
	}}, entered: make(chan struct{}), release: make(chan struct{})}
	controller := newTestController(t, testConfig(t), runner)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	controller.BindContext(ctx)
	result := make(chan error, 1)
	go func() {
		accepted, err := controller.Action("palworld", "start", "test")
		if accepted || err == nil {
			result <- errors.New("late action was accepted")
			return
		}
		result <- nil
	}()
	select {
	case <-runner.entered:
	case <-time.After(time.Second):
		t.Fatal("action did not begin preparation")
	}
	controller.Wait()
	close(runner.release)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("late preparation did not return")
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.starts) != 0 {
		t.Fatal("late action mutated runtime")
	}
}

func TestReconcileAdoptsExternalStart(t *testing.T) {
	cfg := testConfig(t)
	runner := &fakeRunner{statuses: map[string]runtime.Snapshot{
		"palworld.service": {State: runtime.Running, TargetID: "existing", RunID: "run-1"},
	}}
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.Local)
	controller := newTestController(t, cfg, runner, control.WithClock(func() time.Time { return now }))

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
	runner := &fakeRunner{statuses: map[string]runtime.Snapshot{
		"palworld.service": {State: runtime.Stopped, TargetID: "existing"},
	}}
	controller := newTestController(t, cfg, runner, control.WithClock(func() time.Time { return now }))
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
	runner := &fakeRunner{statuses: map[string]runtime.Snapshot{
		"palworld.service": {State: runtime.Stopped, TargetID: "existing"},
	}}
	controller := newTestController(t, cfg, runner, control.WithClock(func() time.Time { return now }))
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
	runner := &fakeRunner{statuses: map[string]runtime.Snapshot{
		"palworld.service": {State: runtime.Running, TargetID: "existing", RunID: "run-1"},
	}}
	first := newTestController(t, cfg, runner, control.WithClock(func() time.Time { return now }))
	first.Reconcile(context.Background(), "palworld")
	firstDeadline := viewByID(t, first, "palworld").DeadlineUnix

	now = now.Add(30 * time.Minute)
	restarted := newTestController(t, cfg, runner, control.WithClock(func() time.Time { return now }))
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
	cfg.Services["factorio"] = config.Service{DisplayName: "异星工厂", Runtime: "systemd", Unit: "factorio.service"}
	runner := &fakeRunner{statuses: map[string]runtime.Snapshot{
		"factorio.service": {State: runtime.Stopped, TargetID: "existing"},
		"palworld.service": {State: runtime.Stopped, TargetID: "existing"},
		"terraria.service": {State: runtime.Running, TargetID: "existing", RunID: "run-1"},
	}}
	controller := newTestController(t, cfg, runner)
	controller.Reconcile(context.Background(), "terraria")

	if err := controller.Start(context.Background(), "factorio", "test"); err != nil {
		t.Fatalf("start factorio: %v", err)
	}
	if got := runner.statuses["terraria.service"].State; got != runtime.Running {
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
	runner := &fakeRunner{statuses: map[string]runtime.Snapshot{
		"palworld.service": {State: runtime.Running, TargetID: "existing", RunID: "run-1"},
		"terraria.service": {State: runtime.Running, TargetID: "existing", RunID: "run-1"},
	}}
	controller := newTestController(t, cfg, runner, control.WithClock(func() time.Time { return now }))

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
	runner := &fakeRunner{statuses: map[string]runtime.Snapshot{
		"palworld.service": {State: runtime.Stopped, TargetID: "existing"},
	}}
	controller := newTestController(t, cfg, runner, control.WithClock(func() time.Time { return now }))
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
