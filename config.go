package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type Config struct {
	Addr                string   `json:"addr"`
	Protocol            string   `json:"protocol"`
	Backends            []string `json:"backends"`
	StickySessions      bool     `json:"sticky_sessions"`
	TLSCertPath         string   `json:"tls_cert_path"`
	TLSKeyPath          string   `json:"tls_key_path"`
	HealthcheckInterval string   `json:"healthcheck_interval"`
}

func loadConfig(filePath string) (*Config, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("could not open config file: %w", err)
	}
	defer f.Close()

	decoder := json.NewDecoder(f)
	config := &Config{}
	if err := decoder.Decode(config); err != nil {
		return nil, fmt.Errorf("could not decode config json: %w", err)
	}

	return config, nil
}
