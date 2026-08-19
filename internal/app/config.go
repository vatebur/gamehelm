package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	defaultListen            = "0.0.0.0:8231"
	defaultPassword          = "changeme"
	defaultRunDuration       = 4 * time.Hour
	defaultExtensionDuration = time.Hour
	minimumServiceDuration   = 5 * time.Minute
	infiniteDurationValue    = "infinite"
)

var (
	unitNamePattern  = regexp.MustCompile(`^[A-Za-z0-9_.@-]+\.service$`)
	serviceIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
)

type ServiceConfig struct {
	DisplayName       string `json:"display_name"`
	Unit              string `json:"unit"`
	RunDuration       string `json:"run_duration,omitempty"`
	ExtensionDuration string `json:"extension_duration,omitempty"`

	runDurationSet        bool
	runDurationNull       bool
	extensionDurationSet  bool
	extensionDurationNull bool
}

func (s *ServiceConfig) UnmarshalJSON(data []byte) error {
	type serviceConfigJSON struct {
		DisplayName       string          `json:"display_name"`
		Unit              string          `json:"unit"`
		RunDuration       json.RawMessage `json:"run_duration"`
		ExtensionDuration json.RawMessage `json:"extension_duration"`
	}
	var raw serviceConfigJSON
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return err
	}

	*s = ServiceConfig{DisplayName: raw.DisplayName, Unit: raw.Unit}
	if raw.RunDuration != nil {
		s.runDurationSet = true
		if bytes.Equal(raw.RunDuration, []byte("null")) {
			s.runDurationNull = true
		} else if err := json.Unmarshal(raw.RunDuration, &s.RunDuration); err != nil {
			return fmt.Errorf("run_duration 必须是字符串: %w", err)
		}
	}
	if raw.ExtensionDuration != nil {
		s.extensionDurationSet = true
		if bytes.Equal(raw.ExtensionDuration, []byte("null")) {
			s.extensionDurationNull = true
		} else if err := json.Unmarshal(raw.ExtensionDuration, &s.ExtensionDuration); err != nil {
			return fmt.Errorf("extension_duration 必须是字符串: %w", err)
		}
	}
	return nil
}

func (s ServiceConfig) durations() (time.Duration, time.Duration, bool) {
	runDuration := defaultRunDuration
	unlimited := s.RunDuration == infiniteDurationValue
	if s.RunDuration != "" && !unlimited {
		runDuration, _ = time.ParseDuration(s.RunDuration)
	}
	extensionDuration := defaultExtensionDuration
	if s.ExtensionDuration != "" {
		extensionDuration, _ = time.ParseDuration(s.ExtensionDuration)
	}
	return runDuration, extensionDuration, unlimited
}

type Config struct {
	Listen   string                   `json:"listen"`
	Password string                   `json:"password"`
	Services map[string]ServiceConfig `json:"services"`
}

type configFile struct {
	Config
	StateFile json.RawMessage `json:"state_file"`
}

func loadConfig(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("打开配置文件: %w", err)
	}
	defer func() { _ = f.Close() }()

	var file configFile
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return Config{}, fmt.Errorf("解析配置文件: %w", err)
	}
	cfg := file.Config
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if c.Listen == "" {
		c.Listen = defaultListen
	}
	if c.Password == "" {
		return errors.New("配置项 password 不能为空")
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
		if err := validateServiceDurations(id, svc); err != nil {
			return err
		}
		units[svc.Unit] = id
	}
	return nil
}

func validateServiceDurations(id string, svc ServiceConfig) error {
	runSet := svc.runDurationSet || svc.RunDuration != ""
	if svc.runDurationNull || (runSet && svc.RunDuration == "") {
		return fmt.Errorf("services.%s.run_duration 不能为空或 null", id)
	}
	extensionSet := svc.extensionDurationSet || svc.ExtensionDuration != ""
	if svc.extensionDurationNull || (extensionSet && svc.ExtensionDuration == "") {
		return fmt.Errorf("services.%s.extension_duration 不能为空或 null", id)
	}

	runDuration, extensionDuration, unlimited := svc.durations()
	if !unlimited {
		if svc.RunDuration != "" {
			var err error
			runDuration, err = time.ParseDuration(svc.RunDuration)
			if err != nil {
				return fmt.Errorf("services.%s.run_duration 不是合法时长: %w", id, err)
			}
		}
		if runDuration < minimumServiceDuration {
			return fmt.Errorf("services.%s.run_duration 不能少于 5 分钟", id)
		}
	}
	if svc.ExtensionDuration != "" {
		var err error
		extensionDuration, err = time.ParseDuration(svc.ExtensionDuration)
		if err != nil {
			return fmt.Errorf("services.%s.extension_duration 不是合法时长: %w", id, err)
		}
	}
	if extensionDuration < minimumServiceDuration {
		return fmt.Errorf("services.%s.extension_duration 不能少于 5 分钟", id)
	}
	if !unlimited && extensionDuration >= runDuration {
		return fmt.Errorf("services.%s.extension_duration 必须小于 run_duration", id)
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
