package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

// TCPServerPool holds the collection of backends.
type TCPServerPool struct {
	BaseServerPool
	listener net.Listener
	wg       sync.WaitGroup
	shutdown chan struct{}
}

// NewTCPServerPool creates a new ServerPool with the given logger.
func NewTCPServerPool(l *log.Logger, config *Config) (*TCPServerPool, error) {
	listener, err := net.Listen("tcp", config.Addr)
	if err != nil {
		return nil, err
	}

	if config.TLSCertPath != "" && config.TLSKeyPath != "" {
		cert, err := tls.LoadX509KeyPair(config.TLSCertPath, config.TLSKeyPath)
		if err != nil {
			log.Fatalf("Error loading key pair: %v", err)
		}
		listener = tls.NewListener(listener, &tls.Config{
			Certificates: []tls.Certificate{cert},
		})
		if err != nil {
			return nil, err
		}
	}

	pool := &TCPServerPool{
		listener: listener,
		shutdown: make(chan struct{}),
		BaseServerPool: BaseServerPool{
			stickySessions: config.StickySessions,
			log:            l,
		},
	}

	// Add backends from config
	for _, backend := range config.Backends {
		pool.AddBackend(backend)
	}

	return pool, nil
}

// Start begins accepting connections and handling them.
func (s *TCPServerPool) Start() error {
	s.wg.Add(1)
	go s.acceptLoop()
	return nil
}

// acceptLoop accepts incoming connections and handles them.
func (s *TCPServerPool) acceptLoop() {
	defer s.wg.Done()

	for {
		select {
		case <-s.shutdown:
			return
		default:
			conn, err := s.listener.Accept()
			if err != nil {
				select {
				case <-s.shutdown:
					return // Shutdown signal received
				default:
					s.log.Printf("error accepting connection: %v\n", err)
					continue
				}
			}
			go proxy(conn, s, s.log)
		}
	}
}

// Shutdown gracefully shuts down the server pool.
func (s *TCPServerPool) Shutdown(ctx context.Context) error {
	start := time.Now()

	select {
	case <-s.shutdown:
		// Already closed
		return nil
	default:
		close(s.shutdown)
	}

	if err := s.listener.Close(); err != nil {
		s.log.Printf("error closing listener: %v\n", err)
	}

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Shutdown completed
	case <-ctx.Done():
		return fmt.Errorf("shutdown timed out: %ws", ctx.Err())
	}

	elapsed := time.Since(start)
	s.log.Printf("server pool shutdown completed in %s", elapsed)
	return nil
}

// Next returns the next available backend using round-robin.
func (s *TCPServerPool) Next(conn net.Addr) *Backend {
	s.backendsMutex.Lock()
	defer s.backendsMutex.Unlock()

	if s.stickySessions {
		ip := getIpFromAddr(conn)
		hash := hashIp(ip)
		idx := hash % len(s.backends)
		if s.backends[idx].Healthy() {
			return s.backends[idx]
		}

		// If the hashed backend is down, find the next healthy one
		backend := s.findNextHealthyBackend(idx)
		if backend != nil {
			return backend
		}
		// If no healthy backend found, return nil
		return nil
	}

	for i := 0; i < len(s.backends); i++ {
		s.current = (s.current + 1) % uint64(len(s.backends))
		if s.backends[s.current].Healthy() {
			return s.backends[s.current]
		}
	}
	return nil
}

// HealthCheck pings a backend to see if it's alive.
func (s *TCPServerPool) HealthCheck() {
	for _, b := range s.backends {
		go func(backend *Backend) {
			for {
				conn, err := net.DialTimeout("tcp", backend.URL.Host, 2*time.Second)
				if err != nil {
					backend.SetHealthy(false)
					s.log.Printf("error connecting to backend %s: %v", backend.URL.Host, err)
					s.log.Printf("backend %s is down", backend.URL.Host)
				} else {
					backend.SetHealthy(true)
					conn.Close()
				}
				time.Sleep(10 * time.Second) // Check every 10 seconds
			}
		}(b)
	}
}

// proxy handles the connection between the client and the selected backend.
func proxy(conn net.Conn, pool *TCPServerPool, l *log.Logger) {
	defer conn.Close()
	backend := pool.Next(conn.RemoteAddr())
	if backend == nil {
		l.Println("no backend available")
		return
	}

	backendConn, err := net.DialTimeout("tcp", backend.URL.Host, 2*time.Second)
	if err != nil {
		l.Println(err)
		return
	}
	defer backendConn.Close()

	go io.Copy(backendConn, conn)

	_, err = io.Copy(conn, backendConn)
	if err != nil {
		l.Println(err)
	}
}
