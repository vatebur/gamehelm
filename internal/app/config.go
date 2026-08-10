package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	defaultListen   = "0.0.0.0:8231"
	defaultPassword = "changeme"
)

var (
	unitNamePattern  = regexp.MustCompile(`^[A-Za-z0-9_.@-]+\.service$`)
	serviceIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
)

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
		c.LogFile = "gamehelm.log"
	}
	if !filepath.IsAbs(c.StateFile) {
		c.StateFile = filepath.Join(baseDir, c.StateFile)
	}
	if !filepath.IsAbs(c.LogFile) {
		c.LogFile = filepath.Join(baseDir, c.LogFile)
	}

	if len(c.Services) == 0 {
		return errors.New("services 至少需要包含一个服务")
	}
	units := make(map[string]string, len(c.Services))
	for id, svc := range c.Services {
		if !serviceIDPattern.MatchString(id) {
			return fmt.Errorf("services.%s 的标识不合法，只能使用小写字母、数字、下划线和连字符", id)
		}
		if strings.TrimSpace(svc.DisplayName) == "" {
			return fmt.Errorf("services.%s.display_name 不能为空", id)
		}
		if !unitNamePattern.MatchString(svc.Unit) {
			return fmt.Errorf("services.%s.unit 不是合法的 systemd service 名称", id)
		}
		if otherID, exists := units[svc.Unit]; exists {
			return fmt.Errorf("services.%s.unit 与 services.%s 重复", id, otherID)
		}
		units[svc.Unit] = id
	}
	return nil
}

func (c Config) serviceIDs() []string {
	ids := make([]string, 0, len(c.Services))
	for id := range c.Services {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
