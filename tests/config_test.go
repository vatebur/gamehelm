package tests

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vatebur/gamehelm/internal/auth"
	"github.com/vatebur/gamehelm/internal/config"
)

func configFixture() config.Config {
	return config.Config{
		Auth: config.AuthConfig{Password: "correct-horse-secret"},
		Services: map[string]config.Service{
			"palworld": {DisplayName: "帕鲁世界", Runtime: "systemd", Unit: "palworld.service"},
			"terraria": {DisplayName: "泰拉瑞亚", Runtime: "systemd", Unit: "terraria.service", RunDuration: "infinite"},
		},
	}
}

func writeConfigFixture(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func configFixtureJSON(t *testing.T) []byte {
	t.Helper()
	data, err := json.MarshalIndent(configFixture(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestConfigDefaultsAndStableServiceOrder(t *testing.T) {
	cfg := configFixture()
	cfg.Services["factorio"] = config.Service{DisplayName: "异星工厂", Runtime: "docker", Container: "factorio"}
	cfg.Docker.Socket = "/run/user/1000/docker.sock"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Web.Listen != "0.0.0.0:8231" || cfg.Web.PublicOrigin != "" || len(cfg.Web.TrustedProxies) != 0 {
		t.Fatalf("unexpected default web configuration: %#v", cfg.Web)
	}
	if got, want := cfg.ServiceIDs(), []string{"factorio", "palworld", "terraria"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("service order = %v, want %v", got, want)
	}
}

func TestConfigRejectsInvalidTargets(t *testing.T) {
	for _, service := range []config.Service{
		{DisplayName: "Test", Runtime: "systemd", Unit: "palworld.service; reboot"},
		{DisplayName: "Test", Runtime: "systemd", Unit: "palworld.service", Container: "palworld"},
		{DisplayName: "Test", Runtime: "docker", Container: "palworld"},
		{DisplayName: "Test", Runtime: "shell", Unit: "palworld.service"},
	} {
		cfg := configFixture()
		cfg.Services["palworld"] = service
		if err := cfg.Validate(); err == nil {
			t.Fatalf("invalid target accepted: %#v", service)
		}
	}
	cfg := configFixture()
	cfg.Services["another"] = cfg.Services["palworld"]
	if cfg.Validate() == nil {
		t.Fatal("accepted duplicate target")
	}
	cfg.Services = nil
	if cfg.Validate() == nil {
		t.Fatal("accepted no services")
	}
}

func TestConfigPerServiceDurations(t *testing.T) {
	for _, tt := range []struct {
		run       string
		extension string
		valid     bool
	}{
		{"6h", "30m", true}, {"infinite", "2h", true}, {"", "", true},
		{"1d", "1h", false}, {"4m59s", "5m", false}, {"4h", "forever", false},
		{"4h", "30s", false}, {"30m", "30m", false}, {"30m", "", false}, {"INFINITE", "1h", false},
	} {
		t.Run(tt.run+"/"+tt.extension, func(t *testing.T) {
			cfg := configFixture()
			service := cfg.Services["palworld"]
			service.RunDuration, service.ExtensionDuration = tt.run, tt.extension
			cfg.Services["palworld"] = service
			if err := cfg.Validate(); (err == nil) != tt.valid {
				t.Fatalf("Validate() = %v, valid = %t", err, tt.valid)
			}
		})
	}
}

func TestConfigRejectsEmptyDurationsAndWrongFields(t *testing.T) {
	for _, addition := range []string{
		`"run_duration": "",`, `"run_duration": null,`, `"extension_duration": "",`, `"extension_duration": null,`,
		`"container": "",`,
	} {
		data := `{"auth":{"password":"correct-horse-secret"},"services":{"game":{` + addition + `"display_name":"Test","runtime":"systemd","unit":"game.service"}}}`
		if _, err := config.Load(writeConfigFixture(t, []byte(data))); err == nil {
			t.Fatalf("accepted invalid service field %s", addition)
		}
	}
}

func TestConfigRejectsLegacyDuplicatesAndTrailingContent(t *testing.T) {
	for _, data := range []string{
		`{"password":"correct-horse-secret","services":{"game":{"display_name":"Test","unit":"game.service"}}}`,
		`{"state_file":"state.json","auth":{"password":"correct-horse-secret"},"services":{"game":{"display_name":"Test","runtime":"systemd","unit":"game.service"}}}`,
		`{"auth":{"password":"correct-horse-secret","password":"another-password"}}`,
		`{"auth":{"password":"correct-horse-secret","Password":"another-password"}}`,
		`{"auth":{"Password":"correct-horse-secret","password_hash":""}}`,
		`{"auth":{"password":"correct-horse-secret","Password_Hash":null}}`,
		`{} {}`,
		`{/* unterminated`,
	} {
		_, err := config.Load(writeConfigFixture(t, []byte(data)))
		if err == nil || strings.Contains(err.Error(), "correct-horse") || strings.Contains(err.Error(), "another-password") {
			t.Fatalf("invalid config error must be redacted: %v", err)
		}
	}
}

func TestConfigCommentsOptionalProxyAndReadOnlyLoad(t *testing.T) {
	data := []byte(`{
  "web": {
    "listen": "0.0.0.0:8231"
    // ,"public_origin": "https://games.example.com"
    // ,"trusted_proxies": ["127.0.0.1/32"]
  },
  /* A block comment. */
  "auth": {"password": "correct-horse-secret"},
  "services": {"game": {"display_name":"https://example.com/*game*/", "runtime":"systemd", "unit":"game.service"}}
}`)
	path := writeConfigFixture(t, data)
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Web.PublicOrigin != "" || cfg.Services["game"].DisplayName != "https://example.com/*game*/" {
		t.Fatal("comment parser changed strings or enabled commented fields")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, data) {
		t.Fatal("Load modified config contents")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatal("Load modified permissions")
	}
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Fatal("Load created an instance lock")
	}
}

func TestConfigOptionalOriginValidation(t *testing.T) {
	for _, tt := range []struct {
		origin string
		valid  bool
	}{
		{"", true}, {"http://192.0.2.2:8231", true}, {"https://games.example.com/", true},
		{"https://user:password@example.com", false}, {"https://example.com/path", false}, {"ftp://example.com", false},
	} {
		cfg := configFixture()
		cfg.Web.PublicOrigin = tt.origin
		if err := cfg.Validate(); (err == nil) != tt.valid {
			t.Fatalf("origin %q validity = %v, want %t", tt.origin, err, tt.valid)
		}
	}
	cfg := configFixture()
	cfg.Web.TrustedProxies = []string{"invalid"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("accepted invalid proxy range")
	}
}

func TestPrepareRewritesPasswordAndLocksInstance(t *testing.T) {
	path := writeConfigFixture(t, configFixtureJSON(t))
	cfg, release, err := config.Prepare(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.Password != "" || !auth.VerifyPassword(cfg.Auth.PasswordHash, "correct-horse-secret") {
		release()
		t.Fatal("prepared credentials did not preserve the password")
	}
	data, err := os.ReadFile(path)
	if err != nil || bytes.Contains(data, []byte("correct-horse-secret")) || !bytes.Contains(data, []byte("password_hash")) {
		release()
		t.Fatal("plaintext remains in saved config")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		release()
		t.Fatal("saved config permissions are not 0600")
	}
	if _, secondRelease, err := config.Prepare(path); err == nil {
		secondRelease()
		release()
		t.Fatal("second instance acquired the same config")
	}
	release()
	again, againRelease, err := config.Prepare(path)
	if err != nil {
		t.Fatal(err)
	}
	defer againRelease()
	if again.Auth.PasswordHash != cfg.Auth.PasswordHash {
		t.Fatal("hashed credentials changed on restart")
	}
}

func TestPrepareFailureKeepsOriginalConfig(t *testing.T) {
	original := configFixtureJSON(t)
	path := writeConfigFixture(t, original)
	if err := os.Mkdir(path+".lock", 0o700); err != nil {
		t.Fatal(err)
	}
	if _, release, err := config.Prepare(path); err == nil {
		release()
		t.Fatal("Prepare accepted an unusable lock path")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("failed Prepare changed original config")
	}
}
