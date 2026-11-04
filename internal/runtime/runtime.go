package runtime

import (
	"context"
	"errors"

	"github.com/vatebur/gamehelm/internal/config"
)

const (
	Running  = "running"
	Stopped  = "stopped"
	Starting = "starting"
	Stopping = "stopping"
	Abnormal = "abnormal"
)

var ErrChanged = errors.New("运行目标已经变化")

type Snapshot struct {
	State    string
	TargetID string
	RunID    string
}

type Driver interface {
	Inspect(context.Context, string) (Snapshot, error)
	Start(context.Context, string, Snapshot) error
	Stop(context.Context, string, Snapshot) error
}

func NewDrivers(cfg config.Config) (map[string]Driver, func() error, error) {
	drivers := make(map[string]Driver)
	closeDriver := func() error { return nil }
	for _, service := range cfg.Services {
		if _, exists := drivers[service.Runtime]; exists {
			continue
		}
		switch service.Runtime {
		case "systemd":
			drivers["systemd"] = Systemd{}
		case "docker":
			driver, err := NewDocker(cfg.Docker.Socket)
			if err != nil {
				return nil, closeDriver, err
			}
			drivers["docker"] = driver
			closeDriver = driver.Close
		default:
			return nil, closeDriver, errors.New("不支持的运行方式")
		}
	}
	return drivers, closeDriver, nil
}
