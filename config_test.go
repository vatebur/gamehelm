package main

import "testing"

func TestConfigRejectsArbitraryService(t *testing.T) {
	cfg := testConfig(t)
	cfg.Services["arbitrary"] = ServiceConfig{DisplayName: "任意服务", Unit: "ssh.service"}
	if err := cfg.validate(t.TempDir()); err == nil {
		t.Fatal("config accepted an arbitrary third service")
	}
}

func TestConfigRejectsUnsafeUnitName(t *testing.T) {
	cfg := testConfig(t)
	service := cfg.Services["palworld"]
	service.Unit = "palworld.service; reboot"
	cfg.Services["palworld"] = service
	if err := cfg.validate(t.TempDir()); err == nil {
		t.Fatal("config accepted an unsafe unit name")
	}
}
