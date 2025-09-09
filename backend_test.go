package main

import "testing"

func TestBackendIsAlive(t *testing.T) {
	b := &Backend{}
	b.SetHealthy(true)
	if !b.Healthy() {
		t.Errorf("Expected backend to be alive")
	}
	b.SetHealthy(false)
	if b.Healthy() {
		t.Errorf("Expected backend to be dead")
	}
}

func TestBackendSetAlive(t *testing.T) {
	b := &Backend{}
	b.SetHealthy(true)
	if !b.isHealthy {
		t.Errorf("Expected backend to be alive")
	}
	b.SetHealthy(false)
	if b.isHealthy {
		t.Errorf("Expected backend to be dead")
	}
}
