package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

const (
	defaultListen   = "0.0.0.0:8231"
	defaultPassword = "changeme"
)

var unitNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.@-]+\.service$`)

type ServiceConfig struct {
	DisplayName string `json:"display_name"`
	Unit        string `json:"unit"`
}

type Config struct {
	Listen    string                   `json:"listen"`
	Password  string                   `json:"password"`
	StateFile string                   `json:"state_file"`
	LogFile   string                   `json:"log_file"`
	Services  map[string]ServiceConfig `json:"services"`
}

func loadConfig(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("打开配置文件: %w", err)
	}
	defer f.Close()

	var cfg Config
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("解析配置文件: %w", err)
	}
	if err := cfg.validate(filepath.Dir(path)); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) validate(baseDir string) error {
	if c.Listen == "" {
		c.Listen = defaultListen
	}
	if c.Password == "" {
		return errors.New("配置项 password 不能为空")
	}
	if c.StateFile == "" {
		c.StateFile = "state.json"
	}
	if c.LogFile == "" {
		c.LogFile = "webctrl.log"
	}
	if !filepath.IsAbs(c.StateFile) {
		c.StateFile = filepath.Join(baseDir, c.StateFile)
	}
	if !filepath.IsAbs(c.LogFile) {
		c.LogFile = filepath.Join(baseDir, c.LogFile)
	}

	for _, id := range []string{"palworld", "terraria"} {
		svc, ok := c.Services[id]
		if !ok {
			return fmt.Errorf("缺少 services.%s 配置", id)
		}
		if svc.DisplayName == "" {
			return fmt.Errorf("services.%s.display_name 不能为空", id)
		}
		if !unitNamePattern.MatchString(svc.Unit) {
			return fmt.Errorf("services.%s.unit 不是合法的 systemd service 名称", id)
		}
	}
	if len(c.Services) != 2 {
		return errors.New("services 只能包含 palworld 和 terraria")
	}
	return nil
}
