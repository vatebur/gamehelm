package tests

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/vatebur/gamehelm/internal/app"
)

func TestConfigAcceptsAndSortsMultipleServices(t *testing.T) {
	cfg := testConfig(t)
	cfg.Services["factorio"] = app.ServiceConfig{DisplayName: "异星工厂", Unit: "factorio.service"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("three-service config rejected: %v", err)
	}
	if got, want := cfg.ServiceIDs(), []string{"factorio", "palworld", "terraria"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("service IDs = %#v, want %#v", got, want)
	}
}

func TestConfigRejectsUnsafeOrDuplicateUnit(t *testing.T) {
	cfg := testConfig(t)
	cfg.Services["factorio"] = app.ServiceConfig{DisplayName: "异星工厂", Unit: "palworld.service"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("config accepted a duplicate unit")
	}

	cfg = testConfig(t)
	service := cfg.Services["palworld"]
	service.Unit = "palworld.service; reboot"
	cfg.Services["palworld"] = service
	if err := cfg.Validate(); err == nil {
		t.Fatal("config accepted an unsafe unit name")
	}
}

func TestConfigRequiresAtLeastOneService(t *testing.T) {
	cfg := testConfig(t)
	cfg.Services = nil
	if err := cfg.Validate(); err == nil {
		t.Fatal("config accepted no services")
	}
}

func TestConfigIgnoresLegacyStateFile(t *testing.T) {
	path := t.TempDir() + "/config.json"
	data := []byte(`{
  "password": "test-password",
  "state_file": "state.json",
  "services": {
    "palworld": {"display_name": "帕鲁世界", "unit": "palworld.service"}
  }
}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.LoadConfig(path); err != nil {
		t.Fatalf("legacy state_file was rejected: %v", err)
	}
}

func TestConfigRejectsRemovedLogFile(t *testing.T) {
	path := t.TempDir() + "/config.json"
	data := []byte(`{
  "password": "test-password",
  "state_file": "state.json",
  "log_file": "gamehelm.log",
  "services": {
    "palworld": {"display_name": "帕鲁世界", "unit": "palworld.service"}
  }
}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := app.LoadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "unknown field \"log_file\"") {
		t.Fatalf("removed log_file was not rejected: %v", err)
	}
}

func TestConfigAcceptsPerServiceDurationsAndInfiniteMode(t *testing.T) {
	cfg := testConfig(t)
	palworld := cfg.Services["palworld"]
	palworld.RunDuration = "6h"
	palworld.ExtensionDuration = "30m"
	cfg.Services["palworld"] = palworld
	terraria := cfg.Services["terraria"]
	terraria.RunDuration = "infinite"
	terraria.ExtensionDuration = "2h"
	cfg.Services["terraria"] = terraria

	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid duration settings rejected: %v", err)
	}
}

func TestConfigRejectsInvalidServiceDurations(t *testing.T) {
	tests := []struct {
		name      string
		run       string
		extension string
	}{
		{name: "invalid run duration", run: "1d", extension: "1h"},
		{name: "run duration below minimum", run: "4m59s", extension: "5m"},
		{name: "invalid extension duration", run: "4h", extension: "forever"},
		{name: "extension below minimum", run: "4h", extension: "30s"},
		{name: "extension equal to run", run: "30m", extension: "30m"},
		{name: "default extension exceeds short run", run: "30m"},
		{name: "uppercase infinite is not supported", run: "INFINITE", extension: "1h"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig(t)
			service := cfg.Services["palworld"]
			service.RunDuration = tt.run
			service.ExtensionDuration = tt.extension
			cfg.Services["palworld"] = service
			if err := cfg.Validate(); err == nil {
				t.Fatal("invalid duration settings were accepted")
			}
		})
	}
}

func TestConfigRejectsExplicitEmptyOrNullDurations(t *testing.T) {
	for _, field := range []string{"run_duration", "extension_duration"} {
		for _, value := range []string{`""`, `null`} {
			t.Run(field+"="+value, func(t *testing.T) {
				path := t.TempDir() + "/config.json"
				data := []byte(`{
  "password": "test-password",
  "services": {
    "palworld": {
      "display_name": "帕鲁世界",
      "unit": "palworld.service",
      "` + field + `": ` + value + `
    }
  }
}`)
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
				if _, err := app.LoadConfig(path); err == nil {
					t.Fatal("explicit empty duration was accepted")
				}
			})
		}
	}
}
