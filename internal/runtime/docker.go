package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/moby/moby/client"
)

type Docker struct {
	client *client.Client
}

func NewDocker(socket string) (*Docker, error) {
	api, err := client.New(client.WithHost("unix://"+socket), client.WithUserAgent("GameHelm/2.0"))
	if err != nil {
		return nil, fmt.Errorf("创建 Docker 客户端: %w", err)
	}
	return &Docker{client: api}, nil
}

func (d *Docker) Close() error { return d.client.Close() }

func (d *Docker) Inspect(ctx context.Context, name string) (Snapshot, error) {
	return d.inspect(ctx, name, name)
}

func (d *Docker) inspect(ctx context.Context, reference, name string) (Snapshot, error) {
	response, err := d.client.ContainerInspect(ctx, reference, client.ContainerInspectOptions{})
	if err != nil {
		return Snapshot{}, fmt.Errorf("查询 Docker 容器: %w", err)
	}
	container := response.Container
	if strings.TrimPrefix(container.Name, "/") != name || container.ID == "" || container.State == nil {
		return Snapshot{}, fmt.Errorf("Docker 容器名称或状态不匹配")
	}
	result := Snapshot{State: Abnormal, TargetID: container.ID, RunID: container.State.StartedAt}
	switch {
	case container.State.Paused || container.State.Dead:
		result.State = Abnormal
	case container.State.Restarting:
		result.State = Starting
	case container.State.Running:
		if result.RunID != "" {
			result.State = Running
		}
	case container.State.Status == "created" || container.State.Status == "exited":
		result.State = Stopped
	}
	return result, nil
}

func (d *Docker) verify(ctx context.Context, name string, expected Snapshot) error {
	current, err := d.inspect(ctx, expected.TargetID, name)
	if err != nil {
		return err
	}
	if current.TargetID != expected.TargetID || current.RunID != expected.RunID || current.State != expected.State {
		return ErrChanged
	}
	return nil
}

func (d *Docker) Start(ctx context.Context, name string, expected Snapshot) error {
	if err := d.verify(ctx, name, expected); err != nil {
		return err
	}
	_, err := d.client.ContainerStart(ctx, expected.TargetID, client.ContainerStartOptions{})
	return err
}

func (d *Docker) Stop(ctx context.Context, name string, expected Snapshot) error {
	if err := d.verify(ctx, name, expected); err != nil {
		return err
	}
	_, err := d.client.ContainerStop(ctx, expected.TargetID, client.ContainerStopOptions{})
	return err
}
