package main

import (
	"strings"
	"testing"
)

func Test_loadConfig(t *testing.T) {
	cfg, err := loadConfig("testdata/config.json")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if cfg == nil {
		t.Fatalf("expected config, got nil")
	}
	if cfg.Addr != ":9090" {
		t.Errorf("expected Addr to be ':9090', got %s", cfg.Addr)
	}
	if cfg.ConsoleAddr != ":8080" {
		t.Errorf("expected ConsoleAddr to be ':8080', got %s", cfg.ConsoleAddr)
	}
	if cfg.Protocol != "tcp" {
		t.Errorf("expected Protocol to be 'tcp', got %s", cfg.Protocol)
	}
	if len(cfg.Backends) != 2 {
		t.Errorf("expected 2 backends, got %d", len(cfg.Backends))
	}
	if cfg.Backends[0] != "http://127.0.0.1:8000" {
		t.Errorf("expected first backend to be 'http://127.0.0.1:8000', got %s", cfg.Backends[0])
	}
	if cfg.Backends[1] != "http://127.0.0.1:8001" {
		t.Errorf("expected second backend to be 'http://127.0.0.1:8001', got %s", cfg.Backends[1])
	}
	if !cfg.StickySessions {
		t.Errorf("expected StickySessions to be true, got %v", cfg.StickySessions)
	}
	if cfg.HealthcheckInterval != "10s" {
		t.Errorf("expected healthcheckInterval to be 10s, got %v", cfg.HealthcheckInterval)
	}
	if cfg.TLSCertPath != "test_cert.pem" {
		t.Errorf("expected TLSCertPath to be 'test_cert.pem', got %s", cfg.TLSCertPath)
	}
	if cfg.TLSKeyPath != "test_key.pem" {
		t.Errorf("expected TLSKeyPath to be 'test_key.pem', got %s", cfg.TLSKeyPath)
	}
}

func Test_loadConfig_fileDoesNotExist(t *testing.T) {
	_, err := loadConfig("testdata/non_existent.json")
	if err == nil {
		t.Errorf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "could not open config file") {
		t.Errorf("expected file open error, got %v", err)
	}
}

func Test_loadConfig_invalidJSON(t *testing.T) {
	_, err := loadConfig("testdata/invalid.json")
	if err == nil {
		t.Errorf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "could not decode config json") {
		t.Errorf("expected JSON decode error, got %v", err)
	}
}
