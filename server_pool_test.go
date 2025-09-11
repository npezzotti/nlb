package main

import (
	"net"
	"slices"
	"testing"
)

func TestNext(t *testing.T) {
	pool := &BaseServerPool{}
	pool.AddBackend("http://localhost:8080")
	pool.AddBackend("http://localhost:8081")
	pool.AddBackend("http://localhost:8082")

	// Mark all backends as alive
	for _, b := range pool.backends {
		b.SetHealthy(true)
	}

	for i := range 3 {
		b := pool.Next(&net.TCPAddr{})
		expected := pool.backends[(i+1)%len(pool.backends)]
		if b == nil || b.URL.String() != expected.URL.String() {
			t.Errorf("expected %s, got %v", expected.URL.String(), b)
		}
	}
}

func TestAddBackend(t *testing.T) {
	pool := &BaseServerPool{}
	pool.AddBackend("http://localhost:8080")

	if len(pool.backends) != 1 {
		t.Errorf("expected 1 backend, got %d", len(pool.backends))
	}
	if pool.backends[0].URL.String() != "http://localhost:8080" {
		t.Errorf("expected backend URL to be http://localhost:8080, got %s", pool.backends[0].URL.String())
	}
}

func TestServerPoolNext_oneDown(t *testing.T) {
	pool := &BaseServerPool{}
	pool.AddBackend("http://localhost:8080")
	pool.AddBackend("http://localhost:8081")
	pool.AddBackend("http://localhost:8082")

	pool.backends[0].SetHealthy(false)
	pool.backends[1].SetHealthy(true)
	pool.backends[2].SetHealthy(true) // Mark one backend as down

	expected := []string{pool.backends[1].URL.String(), pool.backends[2].URL.String()}
	for range 3 {
		b := pool.Next(&net.TCPAddr{})
		if b == nil || !slices.Contains(expected, b.URL.String()) {
			t.Errorf("expected next pool to be in %v, got %v", expected, b)
		}
	}
}

func TestServerPoolNext_allDown(t *testing.T) {
	pool := &BaseServerPool{}
	pool.AddBackend("http://localhost:8080")

	pool.backends[0].SetHealthy(false)
	b := pool.Next(&net.TCPAddr{})
	if b != nil {
		t.Errorf("expected nil, got %v", b)
	}
}

func TestServerPoolNext_sticky(t *testing.T) {
	pool := &BaseServerPool{stickySessions: true}
	pool.AddBackend("http://localhost:8080")
	pool.AddBackend("http://localhost:8081")

	for _, backend := range pool.backends {
		backend.SetHealthy(true)
	}

	remoteAddr := &net.TCPAddr{IP: net.ParseIP("192.168.1.100"), Port: 5678}
	b1 := pool.Next(remoteAddr)
	b2 := pool.Next(remoteAddr)

	if b1 == nil || b2 == nil || b1 != b2 {
		t.Errorf("expected same backend, got %s and %s", b1.URL.String(), b2.URL.String())
	}
}

func TestServerPoolNext_sticky_findsNextHealthy(t *testing.T) {
	pool := &BaseServerPool{stickySessions: true}
	pool.AddBackend("http://localhost:8080")
	pool.AddBackend("http://localhost:8081")
	pool.AddBackend("http://localhost:8082")

	pool.backends[0].SetHealthy(false)
	pool.backends[1].SetHealthy(false)
	pool.backends[2].SetHealthy(true) // Mark one backend as down

	remoteAddr := &net.TCPAddr{IP: net.ParseIP("192.168.1.100"), Port: 5678} // This IP hashes to the down backend at index 0
	b1 := pool.Next(remoteAddr)
	b2 := pool.Next(remoteAddr)
	b3 := pool.Next(remoteAddr)

	if b1 == nil || b2 == nil || b3 == nil ||
		b1 != pool.backends[2] || b2 != pool.backends[2] || b3 != pool.backends[2] {
		t.Errorf("expected backend %q, got %q and %q and %q", pool.backends[2].URL.String(), b1.URL.String(), b2.URL.String(), b3.URL.String())
	}
}

func Test_findNextHealthyBackend(t *testing.T) {
	pool := &BaseServerPool{}
	pool.AddBackend("http://localhost:8080")
	pool.AddBackend("http://localhost:8081")
	pool.AddBackend("http://localhost:8082")

	pool.backends[0].SetHealthy(false)
	pool.backends[1].SetHealthy(false)
	pool.backends[2].SetHealthy(true) // Mark backend at index 2 as healthy

	backend := pool.findNextHealthyBackend(0) // Start from index 0
	if backend == nil || backend != pool.backends[2] {
		t.Errorf("expected backend %q, got %v", pool.backends[2].URL.String(), backend)
	}
}
