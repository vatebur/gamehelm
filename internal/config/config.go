package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vatebur/gamehelm/internal/auth"
)

const maxConfigBytes = 1024 * 1024

var (
	serviceIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	unitPattern      = regexp.MustCompile(`^[A-Za-z0-9_.@-]+\.service$`)
	containerPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
)

type Config struct {
	Web      WebConfig          `json:"web"`
	Auth     AuthConfig         `json:"auth"`
	Docker   DockerConfig       `json:"docker,omitzero"`
	Services map[string]Service `json:"services"`
}

type WebConfig struct {
	Listen         string   `json:"listen"`
	PublicOrigin   string   `json:"public_origin,omitempty"`
	TrustedProxies []string `json:"trusted_proxies,omitempty"`
}

type AuthConfig struct {
	Password     string `json:"password,omitempty"`
	PasswordHash string `json:"password_hash,omitempty"`
}

func (a *AuthConfig) UnmarshalJSON(data []byte) error {
	type plainAuth AuthConfig
	var parsed plainAuth
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&parsed); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	credentialFields := 0
	for name := range fields {
		if strings.EqualFold(name, "password") || strings.EqualFold(name, "password_hash") {
			credentialFields++
		}
	}
	if credentialFields != 1 {
		return errors.New("认证字段必须且只能填写一项")
	}
	*a = AuthConfig(parsed)
	return nil
}

type DockerConfig struct {
	Socket string `json:"socket"`
}

type Service struct {
	DisplayName       string `json:"display_name"`
	Runtime           string `json:"runtime"`
	Container         string `json:"container,omitempty"`
	Unit              string `json:"unit,omitempty"`
	RunDuration       string `json:"run_duration,omitempty"`
	ExtensionDuration string `json:"extension_duration,omitempty"`

	unitPresent      bool
	containerPresent bool
}

func (s *Service) UnmarshalJSON(data []byte) error {
	type plainService Service
	var parsed plainService
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&parsed); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, name := range []string{"run_duration", "extension_duration"} {
		if raw, exists := fields[name]; exists && (bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte(`""`))) {
			return errors.New("时长字段不能为空或 null")
		}
	}
	*s = Service(parsed)
	_, s.unitPresent = fields["unit"]
	_, s.containerPresent = fields["container"]
	return nil
}

func (s Service) Target() string {
	if s.Runtime == "docker" {
		return s.Container
	}
	return s.Unit
}

func (s Service) Durations() (time.Duration, time.Duration, bool) {
	runDuration := 4 * time.Hour
	extensionDuration := time.Hour
	unlimited := s.RunDuration == "infinite"
	if s.RunDuration != "" && !unlimited {
		runDuration, _ = time.ParseDuration(s.RunDuration)
	}
	if s.ExtensionDuration != "" {
		extensionDuration, _ = time.ParseDuration(s.ExtensionDuration)
	}
	return runDuration, extensionDuration, unlimited
}

func (c Config) ServiceIDs() []string {
	ids := make([]string, 0, len(c.Services))
	for id := range c.Services {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (c *Config) Validate() error {
	if c.Web.Listen == "" {
		c.Web.Listen = "0.0.0.0:8231"
	}
	_, port, err := net.SplitHostPort(c.Web.Listen)
	if err != nil {
		return errors.New("web.listen 必须包含主机地址和端口")
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return errors.New("web.listen 端口必须是 0 到 65535 之间的数字")
	}
	if c.Web.PublicOrigin != "" {
		origin, err := url.Parse(c.Web.PublicOrigin)
		if err != nil || (origin.Scheme != "http" && origin.Scheme != "https") || origin.Hostname() == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || (origin.Path != "" && origin.Path != "/") {
			return errors.New("web.public_origin 必须是完整的 HTTP 或 HTTPS 来源地址")
		}
		c.Web.PublicOrigin = strings.TrimSuffix(c.Web.PublicOrigin, "/")
	}
	for _, proxy := range c.Web.TrustedProxies {
		if _, err := netip.ParsePrefix(proxy); err != nil {
			return errors.New("web.trusted_proxies 必须是 IP 网段列表")
		}
	}
	if (c.Auth.Password == "") == (c.Auth.PasswordHash == "") {
		return errors.New("auth.password 和 auth.password_hash 必须且只能填写一项")
	}
	if c.Auth.Password != "" {
		if err := auth.ValidatePassword(c.Auth.Password); err != nil {
			return err
		}
	} else if err := auth.ValidateHash(c.Auth.PasswordHash); err != nil {
		return err
	}
	if len(c.Services) == 0 {
		return errors.New("services 至少需要一个服务")
	}
	targets := make(map[string]bool, len(c.Services))
	for _, id := range c.ServiceIDs() {
		service := c.Services[id]
		if !serviceIDPattern.MatchString(id) {
			return errors.New("服务标识必须由小写字母、数字、下划线和连字符组成，最多 64 个字符")
		}
		if strings.TrimSpace(service.DisplayName) == "" {
			return fmt.Errorf("services.%s.display_name 不能为空", id)
		}
		switch service.Runtime {
		case "systemd":
			if !unitPattern.MatchString(service.Unit) || service.Container != "" || service.containerPresent {
				return fmt.Errorf("services.%s 必须填写合法的 systemd unit，且只能使用 unit 目标字段", id)
			}
		case "docker":
			if !containerPattern.MatchString(service.Container) || service.Unit != "" || service.unitPresent {
				return fmt.Errorf("services.%s 必须填写完整容器名，且只能使用 container 目标字段", id)
			}
			if !filepath.IsAbs(c.Docker.Socket) || strings.ContainsRune(c.Docker.Socket, 0) {
				return errors.New("docker.socket 必须填写本机 Unix socket 的绝对路径")
			}
		default:
			return fmt.Errorf("services.%s.runtime 必须是 systemd 或 docker", id)
		}
		key := service.Runtime + "/" + service.Target()
		if targets[key] {
			return fmt.Errorf("services.%s 与其他服务使用相同目标", id)
		}
		targets[key] = true
		if err := validateDurations(service); err != nil {
			return fmt.Errorf("services.%s 的时长配置无效: %w", id, err)
		}
	}
	return nil
}

func validateDurations(service Service) error {
	if service.RunDuration != "" && service.RunDuration != "infinite" {
		if _, err := time.ParseDuration(service.RunDuration); err != nil {
			return errors.New("run_duration 必须是 Go 时长或 infinite")
		}
	}
	if service.ExtensionDuration != "" {
		if _, err := time.ParseDuration(service.ExtensionDuration); err != nil {
			return errors.New("extension_duration 必须是 Go 时长")
		}
	}
	runDuration, extensionDuration, unlimited := service.Durations()
	if extensionDuration < 5*time.Minute || (!unlimited && runDuration < 5*time.Minute) {
		return errors.New("运行时长与续时时长至少为 5 分钟")
	}
	if !unlimited && extensionDuration >= runDuration {
		return errors.New("续时时长必须小于运行时长")
	}
	return nil
}

func Load(path string) (Config, error) {
	data, err := readConfig(path)
	if err != nil {
		return Config{}, err
	}
	return decodeConfig(data)
}

func readConfig(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开配置文件: %w", err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return nil, errors.New("读取配置文件失败")
	}
	if len(data) > maxConfigBytes {
		return nil, errors.New("配置文件超过 1 MiB")
	}
	return data, nil
}

func decodeConfig(data []byte) (Config, error) {
	data, err := stripComments(data)
	if err != nil {
		return Config{}, err
	}
	if err := validateJSON(data); err != nil {
		return Config{}, err
	}
	var cfg Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, errors.New("配置字段或类型不符合 2.0 格式")
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
