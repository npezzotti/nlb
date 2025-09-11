package main

import (
	"strings"
	"testing"
)

func Test_loadConfig(t *testing.T) {
	cfg, err := loadConfig("testdata/config.json")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if cfg == nil {
		t.Fatalf("Expected config, got nil")
	}
	if cfg.Addr != ":9090" {
		t.Errorf("Expected Addr to be ':9090', got %s", cfg.Addr)
	}
	if cfg.Protocol != "tcp" {
		t.Errorf("Expected Protocol to be 'tcp', got %s", cfg.Protocol)
	}
	if len(cfg.Backends) != 2 {
		t.Errorf("Expected 2 backends, got %d", len(cfg.Backends))
	}
	if cfg.Backends[0] != "http://127.0.0.1:8000" {
		t.Errorf("Expected first backend to be 'http://127.0.0.1:8000', got %s", cfg.Backends[0])
	}
	if cfg.Backends[1] != "http://127.0.0.1:8001" {
		t.Errorf("Expected second backend to be 'http://127.0.0.1:8001', got %s", cfg.Backends[1])
	}
	if !cfg.StickySessions {
		t.Errorf("Expected StickySessions to be true, got %v", cfg.StickySessions)
	}
	if cfg.HealthcheckInterval != "10s" {
		t.Errorf("Expected healthcheckInterval to be 10s, got %v", cfg.HealthcheckInterval)
	}
	if cfg.TLSCertPath != "test_cert.pem" {
		t.Errorf("Expected TLSCertPath to be 'test_cert.pem', got %s", cfg.TLSCertPath)
	}
	if cfg.TLSKeyPath != "test_key.pem" {
		t.Errorf("Expected TLSKeyPath to be 'test_key.pem', got %s", cfg.TLSKeyPath)
	}
}

func Test_loadConfig_fileDoesNotExist(t *testing.T) {
	_, err := loadConfig("testdata/non_existent.json")
	if err == nil {
		t.Errorf("Expected error, got nil")
	}
	if !strings.Contains(err.Error(), "could not open config file") {
		t.Errorf("Expected file open error, got %v", err)
	}
}

func Test_loadConfig_invalidJSON(t *testing.T) {
	_, err := loadConfig("testdata/invalid.json")
	if err == nil {
		t.Errorf("Expected error, got nil")
	}
	if !strings.Contains(err.Error(), "could not decode config json") {
		t.Errorf("Expected JSON decode error, got %v", err)
	}
}
