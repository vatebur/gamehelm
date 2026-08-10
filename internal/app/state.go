package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type timerRecord struct {
	StartedUnix  int64  `json:"started_unix,omitempty"`
	DeadlineUnix int64  `json:"deadline_unix,omitempty"`
	Extensions   int    `json:"extensions,omitempty"`
	Notice       string `json:"notice,omitempty"`
}

type persistedState struct {
	Version  int                    `json:"version"`
	Services map[string]timerRecord `json:"services"`
}

func newPersistedState() persistedState {
	return persistedState{
		Version:  1,
		Services: make(map[string]timerRecord),
	}
}

func loadState(path string) (persistedState, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return newPersistedState(), nil
	}
	if err != nil {
		return persistedState{}, fmt.Errorf("打开状态文件: %w", err)
	}
	defer f.Close()
	state := newPersistedState()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return persistedState{}, fmt.Errorf("解析状态文件: %w", err)
	}
	if state.Version != 1 {
		return persistedState{}, fmt.Errorf("不支持的状态文件版本: %d", state.Version)
	}
	if state.Services == nil {
		state.Services = make(map[string]timerRecord)
	}
	return state, nil
}

func saveState(path string, state persistedState) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建状态目录: %w", err)
	}
	f, err := os.CreateTemp(dir, ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时状态文件: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(state); err != nil {
		f.Close()
		return fmt.Errorf("写入状态: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("同步状态: %w", err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("替换状态文件: %w", err)
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
