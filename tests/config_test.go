package tests

import (
	"reflect"
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
