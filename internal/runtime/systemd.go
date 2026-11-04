package runtime

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type Systemd struct{}

func (Systemd) Inspect(ctx context.Context, unit string) (Snapshot, error) {
	out, err := exec.CommandContext(ctx, "/usr/bin/systemctl", "--user", "show",
		"--property=LoadState", "--property=ActiveState", "--property=InvocationID", "--no-pager", "--", unit).Output()
	if err != nil {
		return Snapshot{}, fmt.Errorf("查询 systemd 状态: %w", err)
	}
	properties := make(map[string]string)
	for _, line := range strings.Split(string(out), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			properties[key] = value
		}
	}
	result := Snapshot{State: Abnormal, TargetID: unit, RunID: properties["InvocationID"]}
	if properties["LoadState"] != "loaded" {
		return result, fmt.Errorf("systemd unit 未加载: %s", unit)
	}
	switch properties["ActiveState"] {
	case "active", "reloading":
		if result.RunID != "" {
			result.State = Running
		}
	case "inactive", "failed":
		result.State = Stopped
	case "activating":
		result.State = Starting
	case "deactivating":
		result.State = Stopping
	}
	return result, nil
}

func (s Systemd) Start(ctx context.Context, unit string, expected Snapshot) error {
	return s.action(ctx, "start", unit, expected)
}

func (s Systemd) Stop(ctx context.Context, unit string, expected Snapshot) error {
	return s.action(ctx, "stop", unit, expected)
}

func (s Systemd) action(ctx context.Context, action, unit string, expected Snapshot) error {
	current, err := s.Inspect(ctx, unit)
	if err != nil {
		return err
	}
	if current.TargetID != expected.TargetID || current.RunID != expected.RunID {
		return ErrChanged
	}
	if current.State != expected.State {
		return ErrChanged
	}
	if err := exec.CommandContext(ctx, "/usr/bin/systemctl", "--user", action, "--", unit).Run(); err != nil {
		return fmt.Errorf("systemctl %s: %w", action, err)
	}
	return nil
}
