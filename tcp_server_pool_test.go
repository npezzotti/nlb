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

	pool, err := NewTCPServerPool(log.New(io.Discard, "", 0), &Config{
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
	pool, err := NewTCPServerPool(log.New(io.Discard, "", 0), &Config{
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

		if _, err := conn.Write([]byte("Hello from backend!\n")); err != nil {
			t.Errorf("error writing to connection: %v", err)
		}
	}()

	time.Sleep(100 * time.Millisecond) // Give backend time to start

	pool, err := NewTCPServerPool(log.New(io.Discard, "", 0), &Config{
		Addr:        "localhost:9091",
		Backends:    []string{"http://localhost:8080"},
		TLSCertPath: "testdata/test_cert.pem",
		TLSKeyPath:  "testdata/test_key.pem",
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
	pool, err := NewTCPServerPool(log.New(io.Discard, "", 0), &Config{
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
