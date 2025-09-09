package main

import (
	"bytes"
	"crypto/tls"
	"io"
	"log"
	"net"
	"slices"
	"sync"
	"testing"
	"time"
)

func Test_proxy(t *testing.T) {
	// Start three backend servers
	addrs := []string{"localhost:8080", "localhost:8081", "localhost:8082"}
	var wg sync.WaitGroup
	for _, addr := range addrs {
		wg.Add(1)
		go func(addr string) {
			defer wg.Done()
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				t.Errorf("failed to start backend server on %s: %v", addr, err)
				return
			}
			defer ln.Close()

			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			_, err = io.Copy(conn, bytes.NewBufferString("Hello from "+addr+"!\n"))
			if err != nil {
				t.Logf("Error writing to connection: %v", err)
				return
			}
		}(addr)
	}

	time.Sleep(100 * time.Millisecond) // Give backends time to start

	pool, err := NewServerPool(log.New(io.Discard, "", 0), &Config{
		Addr: ":9090",
		Backends: []string{
			"http://localhost:8080",
			"http://localhost:8081",
			"http://localhost:8082",
		},
	})
	if err != nil {
		t.Fatalf("failed to create server pool: %v", err)
	}

	// Mark all backends as alive for testing
	for _, b := range pool.backends {
		b.SetHealthy(true)
	}

	pool.Start()

	time.Sleep(100 * time.Millisecond) // Give load balancer time to start

	var responses []string
	for range addrs {
		conn, err := net.Dial("tcp", pool.listener.Addr().String())
		if err != nil {
			t.Errorf("failed to connect to load balancer: %v", err)
			continue
		}

		buf := make([]byte, 64)
		n, err := conn.Read(buf)
		if err != nil {
			t.Errorf("failed to read from load balancer: %v", err)
		}
		responses = append(responses, string(buf[:n]))
		conn.Close()
	}

	if err := pool.Shutdown(t.Context()); err != nil {
		t.Errorf("error during shutdown: %v", err)
	}

	wg.Wait()

	// Check that each expected response is present
	for _, addr := range addrs {
		if !slices.Contains(responses, "Hello from "+addr+"!\n") {
			t.Errorf("expected response from %s not found", addr)
		}
	}

	if err := pool.Shutdown(t.Context()); err != nil {
		t.Errorf("error during shutdown: %v", err)
	}
}

func Test_proxy_noBackends(t *testing.T) {
	pool, err := NewServerPool(log.New(io.Discard, "", 0), &Config{
		Addr:     ":9090",
		Backends: []string{},
	})
	if err != nil {
		t.Fatalf("failed to create server pool: %v", err)
	}

	pool.Start()

	time.Sleep(100 * time.Millisecond) // Give load balancer time to start

	conn, err := net.Dial("tcp", pool.listener.Addr().String())
	if err != nil {
		t.Fatalf("failed to connect to load balancer: %v", err)
	}
	defer conn.Close()

	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil && err != io.EOF {
		t.Errorf("failed to read from load balancer: %v", err)
	}
	if n != 0 {
		t.Errorf("expected no data, got %d bytes", n)
	}

	if err := pool.Shutdown(t.Context()); err != nil {
		t.Errorf("error during shutdown: %v", err)
	}
}

func Test_proxy_tls(t *testing.T) {
	go func() {
		ln, err := net.Listen("tcp", "localhost:8080")
		if err != nil {
			t.Errorf("failed to start backend server: %v", err)
			return
		}
		defer ln.Close()

		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		conn.Write([]byte("Hello from backend!\n"))
	}()

	time.Sleep(100 * time.Millisecond) // Give backend time to start

	pool, err := NewServerPool(log.New(io.Discard, "", 0), &Config{
		Addr:        "localhost:9091",
		Backends:    []string{"http://localhost:8080"},
		TLSCertPath: "test_cert.pem",
		TLSKeyPath:  "test_key.pem",
	})
	if err != nil {
		t.Fatalf("failed to create server pool: %v", err)
	}

	// Mark backend as alive for testing
	pool.backends[0].SetHealthy(true)

	pool.Start()

	time.Sleep(100 * time.Millisecond) // Give load balancer time to start

	conn, err := tls.Dial("tcp", pool.listener.Addr().String(), &tls.Config{
		InsecureSkipVerify: true,
	})
	if err != nil {
		t.Fatalf("failed to connect to load balancer over tls: %v", err)
	}
	defer conn.Close()

	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		t.Errorf("failed to read from load balancer: %v", err)
	}
	response := string(buf[:n])
	expected := "Hello from backend!\n"
	if response != expected {
		t.Errorf("expected %q, got %q", expected, response)
	}

	if err := pool.Shutdown(t.Context()); err != nil {
		t.Errorf("error during shutdown: %v", err)
	}
}

func TestHealthCheck(t *testing.T) {
	pool, err := NewServerPool(log.New(io.Discard, "", 0), &Config{
		Addr: ":9090",
		Backends: []string{
			"http://localhost:8080", // Assume this is down
			"http://localhost:8081", // This will be started
		},
	})
	if err != nil {
		t.Fatalf("failed to create server pool: %v", err)
	}

	// Start a backend server
	ln, err := net.Listen("tcp", "localhost:8081")
	if err != nil {
		t.Errorf("failed to start backend server: %v", err)
		return
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
	}()

	pool.HealthCheck()

	time.Sleep(100 * time.Millisecond) // Wait for health checks to run

	if pool.backends[0].Healthy() {
		t.Errorf("expected backend localhost:8080 to be down")
	}
	if !pool.backends[1].Healthy() {
		t.Errorf("expected backend localhost:8081 to be up")
	}

	if err := pool.Shutdown(t.Context()); err != nil {
		t.Errorf("error during shutdown: %v", err)
	}
}

func TestNext(t *testing.T) {
	pool, err := NewServerPool(log.New(io.Discard, "", 0), &Config{
		Addr: ":9090",
		Backends: []string{
			"http://localhost:8080",
			"http://localhost:8081",
			"http://localhost:8082",
		},
	})
	if err != nil {
		t.Fatalf("failed to create server pool: %v", err)
	}

	// Mark all backends as alive
	for _, b := range pool.backends {
		b.SetHealthy(true)
	}

	for i := range 3 {
		b := pool.Next(&net.TCPConn{})
		expected := pool.backends[(i+1)%len(pool.backends)]
		if b == nil || b.URL.String() != expected.URL.String() {
			t.Errorf("expected %s, got %v", expected.URL.String(), b)
		}
	}

	if err := pool.Shutdown(t.Context()); err != nil {
		t.Errorf("error during shutdown: %v", err)
	}
}

func TestServerPoolNext_oneDown(t *testing.T) {
	pool, err := NewServerPool(log.New(io.Discard, "", 0), &Config{
		Addr: ":9090",
		Backends: []string{
			"http://localhost:8080",
			"http://localhost:8081",
			"http://localhost:8082",
		},
	})
	if err != nil {
		t.Fatalf("failed to create server pool: %v", err)
	}

	pool.backends[0].SetHealthy(false)
	pool.backends[1].SetHealthy(true)
	pool.backends[2].SetHealthy(true) // Mark one backend as down

	expected := []string{pool.backends[1].URL.String(), pool.backends[2].URL.String()}
	for range 3 {
		b := pool.Next(&net.TCPConn{})
		if b == nil || !slices.Contains(expected, b.URL.String()) {
			t.Errorf("expected next pool to be in %v, got %v", expected, b)
		}
	}

	if err := pool.Shutdown(t.Context()); err != nil {
		t.Errorf("error during shutdown: %v", err)
	}
}

func TestServerPoolNext_allDown(t *testing.T) {
	pool, err := NewServerPool(log.New(io.Discard, "", 0), &Config{
		Addr: ":9090",
		Backends: []string{
			"http://localhost:8080",
		},
	})
	if err != nil {
		t.Fatalf("failed to create server pool: %v", err)
	}

	pool.backends[0].SetHealthy(false)
	b := pool.Next(&net.TCPConn{})
	if b != nil {
		t.Errorf("expected nil, got %v", b)
	}

	if err := pool.Shutdown(t.Context()); err != nil {
		t.Errorf("error during shutdown: %v", err)
	}
}

func TestServerPoolAddBackend(t *testing.T) {
	pool, err := NewServerPool(log.New(io.Discard, "", 0), &Config{
		Addr: ":9090",
		Backends: []string{
			"http://localhost:8080",
		},
	})
	if err != nil {
		t.Fatalf("failed to create server pool: %v", err)
	}

	if len(pool.backends) != 1 {
		t.Errorf("expected 1 backend, got %d", len(pool.backends))
	}
	if pool.backends[0].URL.String() != "http://localhost:8080" {
		t.Errorf("expected backend URL to be http://localhost:8080, got %s", pool.backends[0].URL.String())
	}

	if err := pool.Shutdown(t.Context()); err != nil {
		t.Errorf("error during shutdown: %v", err)
	}
}

func TestServerPoolNext_sticky(t *testing.T) {
	pool, err := NewServerPool(log.New(io.Discard, "", 0), &Config{
		Backends: []string{
			"http://localhost:8080",
			"http://localhost:8081",
		},
		StickySessions: true,
	})
	if err != nil {
		t.Fatalf("failed to create server pool: %v", err)
	}

	for _, backend := range pool.backends {
		backend.SetHealthy(true)
	}

	remoteAddr := &net.TCPAddr{IP: net.ParseIP("192.168.1.100"), Port: 5678}
	b1 := pool.Next(&mockNetConn{remoteAddr: remoteAddr})
	b2 := pool.Next(&mockNetConn{remoteAddr: remoteAddr})

	if b1 == nil || b2 == nil || b1 != b2 {
		t.Errorf("expected same backend, got %s and %s", b1.URL.String(), b2.URL.String())
	}
}

func TestServerPoolNext_sticky_findsNextHealthy(t *testing.T) {
	pool, err := NewServerPool(log.New(io.Discard, "", 0), &Config{
		Backends: []string{
			"http://localhost:8080",
			"http://localhost:8081",
			"http://localhost:8082",
		},
		StickySessions: true,
	})
	if err != nil {
		t.Fatalf("failed to create server pool: %v", err)
	}

	pool.backends[0].SetHealthy(false)
	pool.backends[1].SetHealthy(false)
	pool.backends[2].SetHealthy(true) // Mark one backend as down

	remoteAddr := &net.TCPAddr{IP: net.ParseIP("192.168.1.100"), Port: 5678} // This IP hashes to the down backend at index 0
	b1 := pool.Next(&mockNetConn{remoteAddr: remoteAddr})
	b2 := pool.Next(&mockNetConn{remoteAddr: remoteAddr})
	b3 := pool.Next(&mockNetConn{remoteAddr: remoteAddr})

	if b1 == nil || b2 == nil || b3 == nil ||
		b1 != pool.backends[2] || b2 != pool.backends[2] || b3 != pool.backends[2] {
		t.Errorf("expected backend %q, got %q and %q and %q", pool.backends[2].URL.String(), b1.URL.String(), b2.URL.String(), b3.URL.String())
	}
}

func Test_findNextHealthyBackend(t *testing.T) {
	pool, err := NewServerPool(log.New(io.Discard, "", 0), &Config{
		Backends: []string{
			"http://localhost:8080",
			"http://localhost:8081",
			"http://localhost:8082",
		},
	})
	if err != nil {
		t.Fatalf("failed to create server pool: %v", err)
	}

	pool.backends[0].SetHealthy(false)
	pool.backends[1].SetHealthy(false)
	pool.backends[2].SetHealthy(true) // Mark backend at index 2 as healthy

	backend := pool.findNextHealthyBackend(0) // Start from index 0
	if backend == nil || backend != pool.backends[2] {
		t.Errorf("expected backend %q, got %v", pool.backends[2].URL.String(), backend)
	}
}

func Test_getIpFromConn(t *testing.T) {
	remoteAddr := &net.TCPAddr{IP: net.ParseIP("192.168.1.100"), Port: 5678}
	conn := &mockNetConn{remoteAddr: remoteAddr}
	ip := getIpfromConn(conn)
	if ip.String() != "192.168.1.100" {
		t.Errorf("expected 192.168.1.100, got %s", ip)
	}
}

func Test_hashIp(t *testing.T) {
	ip := net.ParseIP("192.168.1.100")
	hash := hashIp(ip)
	expected := int(354263838) // Precomputed hash value for this IP
	if hash != expected {
		t.Errorf("expected %d, got %d", expected, hash)
	}
}
