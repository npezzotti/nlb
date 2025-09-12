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
	listener            net.Listener
	wg                  sync.WaitGroup
	shutdown            chan struct{}
	healthcheckInterval time.Duration
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

	if config.HealthcheckInterval == "" {
		config.HealthcheckInterval = "10s"
	}

	healthcheckInterval, err := time.ParseDuration(config.HealthcheckInterval)
	if err != nil {
		return nil, fmt.Errorf("invalid healthcheck interval: %w", err)
	}

	pool := &TCPServerPool{
		listener: listener,
		shutdown: make(chan struct{}),
		BaseServerPool: BaseServerPool{
			stickySessions: config.StickySessions,
			log:            l,
		},
		healthcheckInterval: healthcheckInterval,
	}

	// Add backends from config
	for _, backend := range config.Backends {
		pool.AddBackend(backend)
	}

	return pool, nil
}

// Start begins accepting connections and handling them.
func (p *TCPServerPool) Start() error {
	p.wg.Add(1)
	go p.acceptLoop()
	return nil
}

// acceptLoop accepts incoming connections and handles them.
func (p *TCPServerPool) acceptLoop() {
	defer p.wg.Done()

	for {
		select {
		case <-p.shutdown:
			return
		default:
			conn, err := p.listener.Accept()
			if err != nil {
				select {
				case <-p.shutdown:
					return // Shutdown signal received
				default:
					p.log.Printf("error accepting connection: %v\n", err)
					continue
				}
			}
			go proxy(conn, p, p.log)
		}
	}
}

// Shutdown gracefully shuts down the server pool.
func (p *TCPServerPool) Shutdown(ctx context.Context) error {
	start := time.Now()

	select {
	case <-p.shutdown:
		// Already closed
		return nil
	default:
		close(p.shutdown)
	}

	if err := p.listener.Close(); err != nil {
		p.log.Printf("error closing listener: %v\n", err)
	}

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Shutdown completed
	case <-ctx.Done():
		return fmt.Errorf("shutdown timed out: %ws", ctx.Err())
	}

	elapsed := time.Since(start)
	p.log.Printf("server pool shutdown completed in %s", elapsed)
	return nil
}

// Next returns the next available backend using round-robin.
func (p *TCPServerPool) Next(conn net.Addr) *Backend {
	p.backendsMutex.Lock()
	defer p.backendsMutex.Unlock()

	if p.stickySessions {
		ip := getIpFromAddr(conn)
		hash := hashIp(ip)
		idx := hash % len(p.backends)
		if p.backends[idx].Healthy() {
			return p.backends[idx]
		}

		// If the hashed backend is down, find the next healthy one
		backend := p.findNextHealthyBackend(idx)
		if backend != nil {
			return backend
		}
		// If no healthy backend found, return nil
		return nil
	}

	for i := 0; i < len(p.backends); i++ {
		p.current = (p.current + 1) % uint64(len(p.backends))
		if p.backends[p.current].Healthy() {
			return p.backends[p.current]
		}
	}
	return nil
}

// StartHealthChecks pings a backend to see if it's alive.
func (p *TCPServerPool) StartHealthChecks() {
	for _, b := range p.backends {
		go func(backend *Backend) {
			for {
				conn, err := net.DialTimeout("tcp", backend.URL.Host, 2*time.Second)
				if err != nil {
					backend.SetHealthy(false)
					p.log.Printf("error connecting to backend %s: %v", backend.URL.Host, err)
					p.log.Printf("backend %s is down", backend.URL.Host)
				} else {
					backend.SetHealthy(true)
					conn.Close()
				}

				select {
				case <-time.After(p.healthcheckInterval):
				case <-p.shutdown:
					return
				}
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
