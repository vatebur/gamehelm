package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	runDuration       = 4 * time.Hour
	extensionDuration = 1 * time.Hour
)

type unitStatus struct {
	Load   string
	Active string
	Sub    string
}

type timerRecord struct {
	DeadlineUnix int64
	Extensions   int
	Notice       string
}

type commandRunner interface {
	Status(context.Context, string) (unitStatus, error)
	Start(context.Context, string) error
	Stop(context.Context, string) error
}

type systemdRunner struct{}

func (systemdRunner) Status(ctx context.Context, unit string) (unitStatus, error) {
	cmd := exec.CommandContext(ctx, "/usr/bin/systemctl", "--user", "show", unit,
		"--property=LoadState", "--property=ActiveState", "--property=SubState", "--no-pager")
	out, err := cmd.Output()
	if err != nil {
		return unitStatus{}, fmt.Errorf("读取 systemd 状态: %w", err)
	}
	status := unitStatus{}
	for _, line := range strings.Split(string(out), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "LoadState":
			status.Load = value
		case "ActiveState":
			status.Active = value
		case "SubState":
			status.Sub = value
		}
	}
	return status, nil
}

func (systemdRunner) Start(ctx context.Context, unit string) error {
	return runSystemctl(ctx, "start", unit)
}

func (systemdRunner) Stop(ctx context.Context, unit string) error {
	return runSystemctl(ctx, "stop", unit)
}

func runSystemctl(ctx context.Context, action, unit string) error {
	cmd := exec.CommandContext(ctx, "/usr/bin/systemctl", "--user", action, unit)
	out, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(out))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("systemctl %s 失败: %s", action, message)
	}
	return nil
}

type serviceView struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Available        bool   `json:"available"`
	On               bool   `json:"on"`
	Running          bool   `json:"running"`
	Status           string `json:"status"`
	Detail           string `json:"detail"`
	Busy             string `json:"busy,omitempty"`
	DeadlineUnix     int64  `json:"deadline_unix,omitempty"`
	RemainingSeconds int64  `json:"remaining_seconds,omitempty"`
	CanExtend        bool   `json:"can_extend"`
	Extensions       int    `json:"extensions"`
	Notice           string `json:"notice,omitempty"`
	Error            string `json:"error,omitempty"`
}

type controller struct {
	cfg       Config
	ids       []string
	runner    commandRunner
	logger    *log.Logger
	now       func() time.Time
	mu        sync.RWMutex
	timers    map[string]timerRecord
	statuses  map[string]unitStatus
	statusErr map[string]string
	busy      map[string]string
	locks     map[string]*sync.Mutex
}

func newController(cfg Config, runner commandRunner, logger *log.Logger) *controller {
	ids := cfg.serviceIDs()
	locks := make(map[string]*sync.Mutex, len(ids))
	for _, id := range ids {
		locks[id] = &sync.Mutex{}
	}
	return &controller{
		cfg:       cfg,
		ids:       ids,
		runner:    runner,
		logger:    logger,
		now:       time.Now,
		timers:    make(map[string]timerRecord, len(ids)),
		statuses:  make(map[string]unitStatus),
		statusErr: make(map[string]string),
		busy:      make(map[string]string),
		locks:     locks,
	}
}

func (c *controller) validID(id string) bool {
	_, ok := c.cfg.Services[id]
	return ok
}

func (c *controller) reconcileAll(ctx context.Context) {
	for _, id := range c.ids {
		c.reconcile(ctx, id)
	}
}

func (c *controller) reconcile(ctx context.Context, id string) {
	lock := c.locks[id]
	lock.Lock()
	defer lock.Unlock()

	statusCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	status, err := c.runner.Status(statusCtx, c.cfg.Services[id].Unit)
	cancel()
	if err != nil {
		c.mu.Lock()
		c.statusErr[id] = "无法读取服务状态"
		c.mu.Unlock()
		c.logger.Printf("状态查询失败 service=%s error=%q", id, err)
		return
	}

	now := c.now()
	c.mu.Lock()
	c.statuses[id] = status
	delete(c.statusErr, id)
	record := c.timers[id]
	c.mu.Unlock()

	if status.Active == "active" {
		if record.DeadlineUnix == 0 {
			record.DeadlineUnix = now.Add(runDuration).Unix()
			record.Extensions = 0
			record.Notice = "检测到外部启动，已自动接管 4 小时计时"
			c.updateRecord(id, record)
			c.logger.Printf("外部启动已接管 service=%s deadline=%d", id, record.DeadlineUnix)
		}
		if record.DeadlineUnix <= now.Unix() {
			c.expire(ctx, id)
		}
		return
	}

	if status.Active == "activating" || status.Active == "reloading" || status.Active == "deactivating" {
		return
	}
	if record.DeadlineUnix != 0 {
		record.DeadlineUnix = 0
		record.Extensions = 0
		record.Notice = "进程异常退出，本次计时已结束"
		c.updateRecord(id, record)
		c.logger.Printf("进程异常退出 service=%s active=%s sub=%s", id, status.Active, status.Sub)
	}
}

func (c *controller) expire(ctx context.Context, id string) {
	c.setBusy(id, "到期关闭中")
	defer c.setBusy(id, "")
	stopCtx, cancel := context.WithTimeout(ctx, 75*time.Second)
	err := c.runner.Stop(stopCtx, c.cfg.Services[id].Unit)
	cancel()
	if err != nil {
		c.mu.Lock()
		record := c.timers[id]
		record.Notice = "到期自动关闭失败，系统将继续重试"
		c.timers[id] = record
		c.statusErr[id] = "到期关闭失败"
		c.mu.Unlock()
		c.logger.Printf("到期自动关闭失败 service=%s error=%q", id, err)
		return
	}
	c.mu.Lock()
	record := c.timers[id]
	record.DeadlineUnix = 0
	record.Extensions = 0
	record.Notice = "已运行至时限，服务已自动关闭"
	c.timers[id] = record
	c.statuses[id] = unitStatus{Load: "loaded", Active: "inactive", Sub: "dead"}
	delete(c.statusErr, id)
	c.mu.Unlock()
	c.logger.Printf("到期自动关闭 service=%s", id)
}

func (c *controller) start(ctx context.Context, id, sourceIP string) error {
	if !c.validID(id) {
		return errors.New("未知服务")
	}
	lock := c.locks[id]
	lock.Lock()
	defer lock.Unlock()
	c.setBusy(id, "正在启动")
	defer c.setBusy(id, "")

	statusCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	status, err := c.runner.Status(statusCtx, c.cfg.Services[id].Unit)
	cancel()
	if err != nil {
		return errors.New("无法读取服务状态")
	}
	if status.Load != "loaded" {
		return errors.New("服务未安装")
	}
	if status.Active == "active" {
		now := c.now()
		c.mu.Lock()
		record := c.timers[id]
		if record.DeadlineUnix == 0 {
			record.DeadlineUnix = now.Add(runDuration).Unix()
			record.Notice = "已接管当前运行中的服务"
			c.timers[id] = record
		}
		c.statuses[id] = status
		c.mu.Unlock()
		return nil
	}

	startCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	err = c.runner.Start(startCtx, c.cfg.Services[id].Unit)
	cancel()
	if err != nil {
		c.logger.Printf("手动启动失败 service=%s ip=%s error=%q", id, sourceIP, err)
		return errors.New("启动失败，请查看日志")
	}
	status, err = c.waitFor(ctx, id, "active", 30*time.Second)
	if err != nil {
		c.logger.Printf("等待启动失败 service=%s ip=%s error=%q", id, sourceIP, err)
		return errors.New("服务未能进入运行状态")
	}

	now := c.now()
	record := timerRecord{
		DeadlineUnix: now.Add(runDuration).Unix(),
		Notice:       "服务已启动，4 小时计时开始",
	}
	c.mu.Lock()
	c.timers[id] = record
	c.statuses[id] = status
	delete(c.statusErr, id)
	c.mu.Unlock()
	c.logger.Printf("手动启动 service=%s ip=%s deadline=%d", id, sourceIP, record.DeadlineUnix)
	return nil
}

func (c *controller) stop(ctx context.Context, id, sourceIP string) error {
	if !c.validID(id) {
		return errors.New("未知服务")
	}
	lock := c.locks[id]
	lock.Lock()
	defer lock.Unlock()
	c.setBusy(id, "正在停止")
	defer c.setBusy(id, "")

	stopCtx, cancel := context.WithTimeout(ctx, 75*time.Second)
	err := c.runner.Stop(stopCtx, c.cfg.Services[id].Unit)
	cancel()
	if err != nil {
		c.logger.Printf("手动停止失败 service=%s ip=%s error=%q", id, sourceIP, err)
		return errors.New("停止失败，请查看日志")
	}
	status, _ := c.waitFor(ctx, id, "inactive", 10*time.Second)
	c.mu.Lock()
	record := c.timers[id]
	record.DeadlineUnix = 0
	record.Extensions = 0
	record.Notice = "服务已手动关闭"
	c.timers[id] = record
	if status.Load != "" {
		c.statuses[id] = status
	} else {
		c.statuses[id] = unitStatus{Load: "loaded", Active: "inactive", Sub: "dead"}
	}
	delete(c.statusErr, id)
	c.mu.Unlock()
	c.logger.Printf("手动停止 service=%s ip=%s", id, sourceIP)
	return nil
}

func (c *controller) extend(ctx context.Context, id, sourceIP string) error {
	if !c.validID(id) {
		return errors.New("未知服务")
	}
	lock := c.locks[id]
	lock.Lock()
	defer lock.Unlock()

	statusCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	status, err := c.runner.Status(statusCtx, c.cfg.Services[id].Unit)
	cancel()
	if err != nil || status.Active != "active" {
		return errors.New("服务当前未运行")
	}
	now := c.now()
	c.mu.Lock()
	record := c.timers[id]
	remaining := time.Unix(record.DeadlineUnix, 0).Sub(now)
	if record.DeadlineUnix == 0 || remaining <= 0 {
		c.mu.Unlock()
		return errors.New("倒计时已结束")
	}
	if remaining > extensionDuration {
		c.mu.Unlock()
		return errors.New("仅可在最后 1 小时内续时")
	}
	record.DeadlineUnix += int64(extensionDuration / time.Second)
	record.Extensions++
	record.Notice = fmt.Sprintf("已续时 1 小时，本次累计续时 %d 次", record.Extensions)
	c.timers[id] = record
	c.statuses[id] = status
	c.mu.Unlock()
	c.logger.Printf("续时 service=%s ip=%s extensions=%d deadline=%d", id, sourceIP, record.Extensions, record.DeadlineUnix)
	return nil
}

func (c *controller) waitFor(ctx context.Context, id, desired string, timeout time.Duration) (unitStatus, error) {
	deadline := time.Now().Add(timeout)
	for {
		statusCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		status, err := c.runner.Status(statusCtx, c.cfg.Services[id].Unit)
		cancel()
		if err == nil && status.Active == desired {
			return status, nil
		}
		if err == nil && status.Active == "failed" {
			return status, errors.New("systemd 报告服务启动失败")
		}
		if time.Now().After(deadline) {
			return status, errors.New("等待 systemd 状态超时")
		}
		select {
		case <-ctx.Done():
			return status, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (c *controller) setBusy(id, value string) {
	c.mu.Lock()
	if value == "" {
		delete(c.busy, id)
	} else {
		c.busy[id] = value
	}
	c.mu.Unlock()
}

func (c *controller) updateRecord(id string, record timerRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.timers[id] = record
}

func (c *controller) views() []serviceView {
	c.mu.RLock()
	defer c.mu.RUnlock()
	now := c.now().Unix()
	views := make([]serviceView, 0, len(c.ids))
	for _, id := range c.ids {
		cfg := c.cfg.Services[id]
		status := c.statuses[id]
		record := c.timers[id]
		busy := c.busy[id]
		remaining := record.DeadlineUnix - now
		if remaining < 0 {
			remaining = 0
		}
		running := status.Active == "active"
		on := running || status.Active == "activating" || status.Active == "deactivating"
		view := serviceView{
			ID:               id,
			Name:             cfg.DisplayName,
			Available:        status.Load == "loaded",
			On:               on,
			Running:          running,
			Status:           chineseStatus(status, busy),
			Detail:           chineseDetail(status),
			Busy:             busy,
			DeadlineUnix:     record.DeadlineUnix,
			RemainingSeconds: remaining,
			CanExtend:        running && busy == "" && remaining > 0 && remaining <= int64(extensionDuration/time.Second),
			Extensions:       record.Extensions,
			Notice:           record.Notice,
			Error:            c.statusErr[id],
		}
		if status.Load == "not-found" {
			view.Error = "服务未安装"
		}
		views = append(views, view)
	}
	return views
}

func chineseStatus(status unitStatus, busy string) string {
	if busy != "" {
		return busy
	}
	if status.Load == "not-found" {
		return "未安装"
	}
	switch status.Active {
	case "active":
		return "运行中"
	case "activating":
		return "启动中"
	case "deactivating":
		return "停止中"
	case "failed":
		return "运行失败"
	case "inactive":
		return "已停止"
	default:
		return "状态检测中"
	}
}

func chineseDetail(status unitStatus) string {
	if status.Active == "active" {
		return "服务器在线，自动关服计时生效"
	}
	if status.Active == "activating" {
		return "正在等待服务完成启动"
	}
	if status.Active == "deactivating" {
		return "正在安全保存并关闭服务"
	}
	if status.Active == "failed" {
		return "systemd 报告进程启动或运行失败"
	}
	if status.Load == "not-found" {
		return "未找到对应的 systemd 服务"
	}
	return "服务器离线，可随时启动新的 4 小时会话"
}

func (c *controller) run(ctx context.Context) {
	c.reconcileAll(ctx)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.reconcileAll(ctx)
		}
	}
}
