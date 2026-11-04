package control

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/vatebur/gamehelm/internal/config"
	"github.com/vatebur/gamehelm/internal/runtime"
)

type ServiceView struct {
	ID                       string `json:"id"`
	Name                     string `json:"name"`
	Runtime                  string `json:"runtime"`
	Available                bool   `json:"available"`
	On                       bool   `json:"on"`
	Running                  bool   `json:"running"`
	Status                   string `json:"status"`
	Detail                   string `json:"detail"`
	Busy                     string `json:"busy,omitempty"`
	DeadlineUnix             int64  `json:"deadline_unix"`
	RemainingSeconds         int64  `json:"remaining_seconds"`
	CanExtend                bool   `json:"can_extend"`
	Extensions               int    `json:"extensions"`
	Unlimited                bool   `json:"unlimited"`
	RunDurationSeconds       int64  `json:"run_duration_seconds"`
	ExtensionDurationSeconds int64  `json:"extension_duration_seconds"`
	Notice                   string `json:"notice,omitempty"`
	Error                    string `json:"error,omitempty"`
}

type timerRecord struct {
	TargetID   string
	RunID      string
	Deadline   time.Time
	Extensions int
	Notice     string
}

type serviceState struct {
	lock     sync.Mutex
	snapshot runtime.Snapshot
	record   timerRecord
	busy     string
	problem  string
}

type Controller struct {
	cfg      config.Config
	ids      []string
	drivers  map[string]runtime.Driver
	quotas   map[string]chan struct{}
	opQuotas map[string]chan struct{}
	logger   *log.Logger
	now      func() time.Time
	mu       sync.RWMutex
	states   map[string]*serviceState
	ctx      context.Context
	actions  sync.WaitGroup
	closing  bool
}

type Option func(*Controller)

func WithClock(now func() time.Time) Option { return func(c *Controller) { c.now = now } }

func New(cfg config.Config, drivers map[string]runtime.Driver, logger *log.Logger, options ...Option) *Controller {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	c := &Controller{cfg: cfg, ids: cfg.ServiceIDs(), drivers: drivers, logger: logger,
		now: time.Now, states: make(map[string]*serviceState), quotas: make(map[string]chan struct{}), opQuotas: make(map[string]chan struct{}), ctx: context.Background()}
	for _, id := range c.ids {
		c.states[id] = &serviceState{}
		backend := cfg.Services[id].Runtime
		if c.quotas[backend] == nil {
			c.quotas[backend] = make(chan struct{}, 4)
			c.opQuotas[backend] = make(chan struct{}, 4)
		}
	}
	for _, option := range options {
		option(c)
	}
	return c
}

func (c *Controller) BindContext(ctx context.Context) {
	c.mu.Lock()
	c.ctx = ctx
	c.mu.Unlock()
}

func (c *Controller) lifetime() context.Context {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ctx
}

func (c *Controller) ServiceIDs() []string { return append([]string(nil), c.ids...) }

// Wait closes action admission and drains accepted asynchronous operations.
func (c *Controller) Wait() {
	c.mu.Lock()
	c.closing = true
	c.mu.Unlock()
	c.actions.Wait()
}

func (c *Controller) inspect(ctx context.Context, id string) (runtime.Snapshot, error) {
	service := c.cfg.Services[id]
	driver := c.drivers[service.Runtime]
	if driver == nil {
		return runtime.Snapshot{}, errors.New("运行时未配置")
	}
	queryCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	quota := c.quotas[service.Runtime]
	select {
	case quota <- struct{}{}:
		defer func() { <-quota }()
	case <-queryCtx.Done():
		return runtime.Snapshot{}, queryCtx.Err()
	}
	return driver.Inspect(queryCtx, service.Target())
}

// observe updates a snapshot only after the runtime has answered. An unavailable
// runtime leaves the previous run and deadline intact.
func (c *Controller) observe(id string, snapshot runtime.Snapshot, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.states[id]
	if err != nil {
		state.problem = "异常，请查看日志"
		c.logger.Printf("状态查询失败 service=%s error=%q", id, err)
		return
	}
	state.snapshot = snapshot
	state.problem = ""
	switch snapshot.State {
	case runtime.Running:
		if snapshot.TargetID == "" || snapshot.RunID == "" {
			state.problem = "异常，请查看日志"
			return
		}
		if state.record.TargetID != snapshot.TargetID || state.record.RunID != snapshot.RunID {
			run, _, unlimited := c.cfg.Services[id].Durations()
			state.record = timerRecord{TargetID: snapshot.TargetID, RunID: snapshot.RunID, Notice: "已接管本次运行"}
			if !unlimited {
				state.record.Deadline = c.now().Add(run)
			}
			c.logger.Printf("接管运行 service=%s unlimited=%t", id, unlimited)
		}
	case runtime.Stopped:
		state.record = timerRecord{}
	case runtime.Abnormal:
		state.problem = "异常，请查看日志"
	}
}

func (c *Controller) Reconcile(ctx context.Context, id string) {
	state, exists := c.states[id]
	if !exists || !state.lock.TryLock() {
		return
	}
	defer state.lock.Unlock()
	c.mu.RLock()
	busy := state.busy
	c.mu.RUnlock()
	if busy != "" {
		return
	}
	snapshot, err := c.inspect(ctx, id)
	c.observe(id, snapshot, err)
	if err != nil || snapshot.State != runtime.Running {
		return
	}
	c.mu.RLock()
	deadline := state.record.Deadline
	c.mu.RUnlock()
	if !deadline.IsZero() && !c.now().Before(deadline) {
		c.setBusy(id, "stop")
		defer c.setBusy(id, "")
		if err := c.perform(ctx, id, "stop", "timer", snapshot); err != nil {
			c.failOperation(id, "stop", err)
		}
	}
}

func (c *Controller) ReconcileAll(ctx context.Context) {
	var workers sync.WaitGroup
	for _, id := range c.ids {
		workers.Go(func() { c.Reconcile(ctx, id) })
	}
	workers.Wait()
}

// Run gives each target its own polling loop, so a slow stop never delays
// another target's reconciliation.
func (c *Controller) Run(ctx context.Context) {
	c.BindContext(ctx)
	var workers sync.WaitGroup
	for _, id := range c.ids {
		workers.Go(func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				c.Reconcile(ctx, id)
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		})
	}
	workers.Wait()
}

func (c *Controller) setBusy(id, action string) {
	c.mu.Lock()
	c.states[id].busy = action
	c.mu.Unlock()
}

func (c *Controller) failOperation(id, action string, err error) {
	c.mu.Lock()
	c.states[id].problem = "异常，请查看日志"
	c.mu.Unlock()
	c.logger.Printf("操作失败 service=%s action=%s error=%q", id, action, err)
}

func (c *Controller) Action(id, action, source string) (bool, error) {
	state, exists := c.states[id]
	if !exists {
		return false, errors.New("未知服务")
	}
	if action == "extend" {
		return false, c.Extend(c.lifetime(), id, source)
	}
	if action != "start" && action != "stop" {
		return false, errors.New("未知操作")
	}
	c.mu.RLock()
	busy := state.busy
	closing := c.closing
	c.mu.RUnlock()
	if closing {
		return false, errors.New("控制台正在停止")
	}
	if busy != "" {
		if busy == action {
			return true, nil
		}
		return false, errors.New("当前操作尚未完成")
	}
	if !state.lock.TryLock() {
		return false, errors.New("状态正在更新，请稍后重试")
	}
	defer state.lock.Unlock()
	// Busy may have changed while the lock was being acquired.
	c.mu.RLock()
	busy = state.busy
	c.mu.RUnlock()
	if busy != "" {
		if busy == action {
			return true, nil
		}
		return false, errors.New("当前操作尚未完成")
	}
	ctx := c.lifetime()
	if err := ctx.Err(); err != nil {
		return false, errors.New("控制台正在停止")
	}
	snapshot, needed, err := c.prepare(ctx, id, action)
	if err != nil || !needed {
		return false, err
	}
	c.mu.Lock()
	if c.closing || ctx.Err() != nil {
		c.mu.Unlock()
		return false, errors.New("控制台正在停止")
	}
	c.actions.Add(1)
	state.busy = action
	c.mu.Unlock()
	go func() {
		defer c.actions.Done()
		state.lock.Lock()
		defer state.lock.Unlock()
		defer c.setBusy(id, "")
		if err := c.perform(ctx, id, action, source, snapshot); err != nil {
			c.failOperation(id, action, err)
		}
	}()
	return true, nil
}

func (c *Controller) prepare(ctx context.Context, id, action string) (runtime.Snapshot, bool, error) {
	snapshot, err := c.inspect(ctx, id)
	c.observe(id, snapshot, err)
	if err != nil || snapshot.State == runtime.Abnormal || snapshot.State == "" {
		return snapshot, false, errors.New("异常，请查看日志")
	}
	if (action == "start" && snapshot.State == runtime.Running) || (action == "stop" && snapshot.State == runtime.Stopped) {
		return snapshot, false, nil
	}
	if snapshot.State == runtime.Stopping || (snapshot.State == runtime.Starting && action == "start") {
		return snapshot, false, errors.New("运行时正在执行操作，请稍后重试")
	}
	return snapshot, true, nil
}

func (c *Controller) perform(ctx context.Context, id, action, source string, expected runtime.Snapshot) error {
	opCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	service := c.cfg.Services[id]
	driver := c.drivers[service.Runtime]
	quota := c.opQuotas[service.Runtime]
	select {
	case quota <- struct{}{}:
	case <-opCtx.Done():
		return opCtx.Err()
	}
	var err error
	desired := runtime.Running
	if action == "start" {
		err = driver.Start(opCtx, service.Target(), expected)
	} else {
		desired = runtime.Stopped
		err = driver.Stop(opCtx, service.Target(), expected)
	}
	<-quota
	if err != nil {
		return err
	}
	confirmCtx, confirmCancel := context.WithTimeout(opCtx, 30*time.Second)
	defer confirmCancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		snapshot, inspectErr := c.inspect(confirmCtx, id)
		c.observe(id, snapshot, inspectErr)
		if inspectErr == nil {
			if snapshot.TargetID != expected.TargetID {
				return runtime.ErrChanged
			}
			if action == "stop" && snapshot.State == runtime.Running && snapshot.RunID != expected.RunID {
				return runtime.ErrChanged
			}
			if snapshot.State == desired {
				c.logger.Printf("操作完成 service=%s action=%s source=%s", id, action, source)
				return nil
			}
		}
		select {
		case <-confirmCtx.Done():
			return errors.New("操作结果尚未确认")
		case <-ticker.C:
		}
	}
}

func (c *Controller) synchronous(ctx context.Context, id, action, source string) error {
	state, exists := c.states[id]
	if !exists {
		return errors.New("未知服务")
	}
	state.lock.Lock()
	defer state.lock.Unlock()
	snapshot, needed, err := c.prepare(ctx, id, action)
	if err != nil || !needed {
		return err
	}
	c.setBusy(id, action)
	defer c.setBusy(id, "")
	if err := c.perform(ctx, id, action, source, snapshot); err != nil {
		c.failOperation(id, action, err)
		return errors.New("异常，请查看日志")
	}
	return nil
}

func (c *Controller) Start(ctx context.Context, id, source string) error {
	return c.synchronous(ctx, id, "start", source)
}
func (c *Controller) Stop(ctx context.Context, id, source string) error {
	return c.synchronous(ctx, id, "stop", source)
}

func (c *Controller) Extend(ctx context.Context, id, source string) error {
	state, exists := c.states[id]
	if !exists {
		return errors.New("未知服务")
	}
	if !state.lock.TryLock() {
		return errors.New("当前操作尚未完成")
	}
	defer state.lock.Unlock()
	c.mu.RLock()
	busy := state.busy
	closing := c.closing
	c.mu.RUnlock()
	if closing || ctx.Err() != nil {
		return errors.New("控制台正在停止")
	}
	if busy != "" {
		return errors.New("当前操作尚未完成")
	}
	_, extension, unlimited := c.cfg.Services[id].Durations()
	if unlimited {
		return errors.New("无限运行的服务无需续时")
	}
	snapshot, err := c.inspect(ctx, id)
	c.observe(id, snapshot, err)
	if err != nil || snapshot.State != runtime.Running {
		return errors.New("服务当前状态异常或未运行")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing || ctx.Err() != nil {
		return errors.New("控制台正在停止")
	}
	remaining := state.record.Deadline.Sub(c.now())
	if state.record.Deadline.IsZero() || remaining <= 0 {
		return errors.New("倒计时已结束")
	}
	if remaining > extension {
		return errors.New("尚未进入续时时段")
	}
	state.record.Deadline = state.record.Deadline.Add(extension)
	state.record.Extensions++
	state.record.Notice = fmt.Sprintf("本次累计续时 %d 次", state.record.Extensions)
	c.logger.Printf("续时 service=%s source=%s extensions=%d", id, source, state.record.Extensions)
	return nil
}

func (c *Controller) Views() []ServiceView {
	c.mu.RLock()
	defer c.mu.RUnlock()
	views := make([]ServiceView, 0, len(c.ids))
	for _, id := range c.ids {
		service := c.cfg.Services[id]
		run, extension, unlimited := service.Durations()
		state := c.states[id]
		available := state.problem == "" && state.snapshot.State != "" && state.snapshot.State != runtime.Abnormal
		running := available && state.snapshot.State == runtime.Running
		var deadline, remaining int64
		if !state.record.Deadline.IsZero() {
			deadline = state.record.Deadline.Unix()
			remaining = max(0, int64(state.record.Deadline.Sub(c.now()).Seconds()))
		}
		status := "异常"
		detail := "请查看服务日志"
		if available {
			switch state.snapshot.State {
			case runtime.Running:
				status, detail = "运行中", "自动关闭计时生效"
				if unlimited {
					detail = "保持无限运行"
				}
			case runtime.Stopped:
				status, detail = "已停止", "可以启动服务"
			case runtime.Starting:
				status, detail = "启动中", "正在等待服务启动"
			case runtime.Stopping:
				status, detail = "停止中", "正在等待服务停止"
			}
		}
		busyText := ""
		if state.busy == "start" {
			status, busyText = "启动中", "启动中"
		}
		if state.busy == "stop" {
			status, busyText = "停止中", "停止中"
		}
		runSeconds := int64(run / time.Second)
		if unlimited {
			runSeconds = 0
		}
		views = append(views, ServiceView{
			ID: id, Name: service.DisplayName, Runtime: service.Runtime, Available: available,
			On:      running || state.snapshot.State == runtime.Starting || state.snapshot.State == runtime.Stopping || state.busy == "stop",
			Running: running, Status: status, Detail: detail, Busy: busyText, DeadlineUnix: deadline, RemainingSeconds: remaining,
			CanExtend:  !unlimited && running && state.busy == "" && remaining > 0 && state.record.Deadline.Sub(c.now()) <= extension,
			Extensions: state.record.Extensions, Unlimited: unlimited, RunDurationSeconds: runSeconds,
			ExtensionDurationSeconds: int64(extension / time.Second), Notice: state.record.Notice, Error: state.problem,
		})
	}
	return views
}
