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
	if err := cfg.Validate(t.TempDir()); err != nil {
		t.Fatalf("three-service config rejected: %v", err)
	}
	if got, want := cfg.ServiceIDs(), []string{"factorio", "palworld", "terraria"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("service IDs = %#v, want %#v", got, want)
	}
}

func TestConfigRejectsUnsafeOrDuplicateUnit(t *testing.T) {
	cfg := testConfig(t)
	cfg.Services["factorio"] = app.ServiceConfig{DisplayName: "异星工厂", Unit: "palworld.service"}
	if err := cfg.Validate(t.TempDir()); err == nil {
		t.Fatal("config accepted a duplicate unit")
	}

	cfg = testConfig(t)
	service := cfg.Services["palworld"]
	service.Unit = "palworld.service; reboot"
	cfg.Services["palworld"] = service
	if err := cfg.Validate(t.TempDir()); err == nil {
		t.Fatal("config accepted an unsafe unit name")
	}
}

func TestConfigRequiresAtLeastOneService(t *testing.T) {
	cfg := testConfig(t)
	cfg.Services = nil
	if err := cfg.Validate(t.TempDir()); err == nil {
		t.Fatal("config accepted no services")
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
