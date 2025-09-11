package main

import (
	"io"
	"log"
	"net"
	"net/url"
	"testing"
	"time"
)

func TestNewUDPServerPool(t *testing.T) {
	l := log.New(nil, "", 0)
	pool, err := NewUDPServerPool(l, &Config{
		Addr: ":9090",
		Backends: []string{
			"http://localhost:8080",
			"http://localhost:8081",
		},
		StickySessions:      true,
		HealthcheckInterval: "10s",
	})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if pool.log == nil || pool.log != l {
		t.Errorf("expected logger to be set, got %v", pool.log)
	}
	if pool.addr != ":9090" {
		t.Errorf("expected addr to be :9090, got %s", pool.addr)
	}
	if len(pool.backends) != 2 {
		t.Errorf("expected 2 backends, got %d", len(pool.backends))
	}
	if !pool.stickySessions {
		t.Errorf("expected stickySessions to be true, got false")
	}
	if pool.healthcheckInterval != 10*time.Second {
		t.Errorf("expected healthcheckInterval to be 10s, got %v", pool.healthcheckInterval)
	}
}

func Test_forwardToBackend(t *testing.T) {
	pool, err := NewUDPServerPool(nil, &Config{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	go func() {
		addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:8080")
		if err != nil {
			t.Errorf("failed to resolve UDP address: %v", err)
			return
		}
		conn, err := net.ListenUDP("udp", addr)
		if err != nil {
			t.Errorf("failed to listen on UDP address: %v", err)
			return
		}
		defer conn.Close()

		buf := make([]byte, 1024)
		n, clientAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			t.Errorf("failed to read from UDP: %v", err)
			return
		}

		// Echo back the received data
		_, err = conn.WriteToUDP(buf[:n], clientAddr)
		if err != nil {
			t.Errorf("failed to write to UDP: %v", err)
		}
	}()

	time.Sleep(100 * time.Millisecond)

	backendUrl, err := url.Parse("http://127.0.0.1:8080")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	resp, err := pool.forwardToBackend(&Backend{URL: backendUrl}, []byte("test data"))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp == nil {
		t.Fatalf("expected response to be non-nil")
	}
	if string(resp) != "test data" {
		t.Errorf("expected response to be 'test data', got %s", string(resp))
	}
}

func Test_handleConnection(t *testing.T) {
	pool, err := NewUDPServerPool(log.New(io.Discard, "", 0), &Config{
		Addr: ":9090",
		Backends: []string{
			"http://127.0.0.1:8080",
		},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	pool.Start()
	defer pool.Shutdown(t.Context())

	pool.backends[0].SetHealthy(true)

	clientAddrChan := make(chan *net.UDPAddr)
	dataChan := make(chan []byte)
	go func() {
		conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
		if err != nil {
			t.Errorf("failed to listen on udp address: %v", err)
			return
		}
		defer conn.Close()

		clientAddrChan <- conn.LocalAddr().(*net.UDPAddr)

		buf := make([]byte, 1024)
		n, err := conn.Read(buf)
		if err != nil {
			t.Errorf("failed to read from udp: %v", err)
			return
		}
		dataChan <- buf[:n]
	}()

	var clientAddr *net.UDPAddr
	select {
	case clientAddr = <-clientAddrChan:
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for clientAddr")
	}

	go func() {
		addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:8080")
		if err != nil {
			t.Errorf("failed to resolve udp address: %v", err)
			return
		}
		conn, err := net.ListenUDP("udp", addr)
		if err != nil {
			t.Errorf("failed to listen on udp address: %v", err)
			return
		}
		defer conn.Close()

		buf := make([]byte, 1024)
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			t.Errorf("failed to read from udp: %v", err)
			return
		}
		if string(buf[:n]) != "hello" {
			t.Errorf("expected data to be 'hello', got %s", string(buf[:n]))
			return
		}
		_, err = conn.WriteToUDP(buf[:n], addr)
		if err != nil {
			t.Errorf("failed to write to udp: %v", err)
		}
	}()

	time.Sleep(100 * time.Millisecond)

	pool.handleConnection(clientAddr, []byte("hello"))

	select {
	case data := <-dataChan:
		if string(data) != "hello" {
			t.Errorf("expected data to be 'hello', got %s", string(data))
		}
	case <-time.After(2 * time.Second):
		t.Errorf("timeout waiting for data")
	}

	if err := pool.Shutdown(t.Context()); err != nil {
		t.Errorf("error during shutdown: %v", err)
	}
}

func TestUDPServerPoolHealthCheck(t *testing.T) {
	pool, err := NewUDPServerPool(log.New(io.Discard, "", 0), &Config{
		Addr: ":9090",
		Backends: []string{
			"http://127.0.0.1:8080", // Assume this is down
			"http://127.0.0.1:8081", // This will be started
		},
	})
	if err != nil {
		t.Fatalf("failed to create server pool: %v", err)
	}

	go func() {
		// Start a backend server
		conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8081})
		if err != nil {
			t.Errorf("failed to start backend server: %v", err)
			return
		}
		defer conn.Close()

		buf := make([]byte, 1024)
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			return
		}
		if string(buf[:n]) == "ping" {
			_, err = conn.WriteTo([]byte("pong"), addr)
			if err != nil {
				t.Errorf("failed to write pong: %v", err)
			}
		}
	}()

	time.Sleep(100 * time.Millisecond) // Allow the backend time to start

	pool.HealthCheck()

	time.Sleep(100 * time.Millisecond) // Wait for health checks to run

	if pool.backends[0].Healthy() {
		t.Errorf("expected backend localhost:8080 to be down")
	}
	if !pool.backends[1].Healthy() {
		t.Errorf("expected backend localhost:8081 to be up")
	}
}
