package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

type UDPServerPool struct {
	BaseServerPool
	conn                *net.UDPConn
	wg                  sync.WaitGroup
	shutdown            chan struct{}
	healthcheckInterval time.Duration
	addr                string
}

func NewUDPServerPool(l *log.Logger, config *Config) (*UDPServerPool, error) {
	if config.HealthcheckInterval == "" {
		config.HealthcheckInterval = "10s" // Default to 10 seconds if not set
	}

	healthcheckInterval, err := time.ParseDuration(config.HealthcheckInterval)
	if err != nil {
		return nil, fmt.Errorf("invalid healthcheck interval: %w", err)
	}

	pool := &UDPServerPool{
		shutdown:            make(chan struct{}),
		addr:                config.Addr,
		healthcheckInterval: healthcheckInterval,
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

func (p *UDPServerPool) HealthCheck() {
	for _, b := range p.backends {
		go func(backend *Backend) {
			for {
				addr, err := net.ResolveUDPAddr("udp", backend.URL.Host)
				if err != nil {
					p.log.Printf("error resolving backend address %s: %v", backend.URL.Host, err)
					backend.SetHealthy(false)
					time.Sleep(p.healthcheckInterval)
					continue
				}
				conn, err := net.DialUDP("udp", nil, addr)
				if err != nil {
					backend.SetHealthy(false)
					p.log.Printf("error connecting to backend %s: %v", backend.URL.Host, err)
					p.log.Printf("backend %s is down", backend.URL.Host)
				}

				// Send health check ping
				conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
				if _, err := conn.Write([]byte("ping")); err != nil {
					backend.SetHealthy(false)
					p.log.Printf("error writing to backend %s: %v", backend.URL.Host, err)
					p.log.Printf("backend %s is down", backend.URL.Host)
				} else {
					backend.SetHealthy(true)
				}

				buf := make([]byte, 1024)
				conn.SetReadDeadline(time.Now().Add(2 * time.Second))
				n, backendAddr, err := conn.ReadFrom(buf)
				if err != nil {
					backend.SetHealthy(false)
					p.log.Printf("error reading from backend %s: %v", backend.URL.Host, err)
				} else {
					if backendAddr.String() == backend.URL.Host && string(buf[:n]) == "pong" {
						backend.SetHealthy(true)
					} else {
						backend.SetHealthy(false)
						p.log.Printf("unexpected response from backend %s: %s", backend.URL.Host, string(buf[:n]))
					}
				}
				conn.Close()
				time.Sleep(p.healthcheckInterval)
			}
		}(b)
	}
}

func (p *UDPServerPool) Start() error {
	var err error
	p.conn, err = net.ListenUDP("udp", &net.UDPAddr{
		Port: 9090,
	})
	if err != nil {
		return fmt.Errorf("error starting udp server: %w", err)
	}
	p.log.Printf("UDP server started on %s", p.conn.LocalAddr().String())

	p.wg.Add(1)
	go p.acceptUDPConnections()
	return nil
}

func (p *UDPServerPool) Shutdown(ctx context.Context) error {
	start := time.Now()

	select {
	case <-p.shutdown:
		// Already closed
		return nil
	default:
		close(p.shutdown)
	}

	var err error
	if p.conn != nil {
		err = p.conn.Close()
	}
	if err != nil {
		return fmt.Errorf("error closing UDP connection: %w", err)
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

func (p *UDPServerPool) acceptUDPConnections() {
	defer p.wg.Done()

	buf := make([]byte, 65507) // Max UDP payload size
	for {
		select {
		case <-p.shutdown:
			return
		default:
			n, addr, err := p.conn.ReadFromUDP(buf)
			if err != nil {
				select {
				case <-p.shutdown:
					return // Shutdown signal received
				default:
					p.log.Printf("error accepting connection: %v\n", err)
					continue
				}
			}
			go p.handleConnection(addr, buf[:n])
		}
	}
}

func (p *UDPServerPool) handleConnection(clientAddr *net.UDPAddr, data []byte) {
	backend := p.Next(clientAddr)
	if backend == nil {
		p.log.Printf("No healthy backend available")
		return
	}
	resp, err := p.forwardToBackend(backend, data)
	if err != nil {
		p.log.Printf("Error forwarding to backend: %v", err)
		return
	}
	if _, err := p.conn.WriteToUDP(resp, clientAddr); err != nil {
		p.log.Printf("Error writing response to client: %v", err)
	}
}

func (p *UDPServerPool) forwardToBackend(backend *Backend, data []byte) ([]byte, error) {
	remoteAddr, err := net.ResolveUDPAddr("udp", backend.URL.Host)
	if err != nil {
		return nil, fmt.Errorf("error resolving backend address %s: %w", backend.URL.Host, err)
	}
	conn, err := net.DialUDP("udp", nil, remoteAddr)
	if err != nil {
		return nil, fmt.Errorf("error dialing backend %s: %w", backend.URL.Host, err)
	}
	defer conn.Close()

	if _, err := conn.Write(data); err != nil {
		return nil, fmt.Errorf("error writing to backend %s: %w", backend.URL.Host, err)
	}

	buf := make([]byte, 65507)
	n, addr, err := conn.ReadFromUDP(buf)
	if err != nil {
		return nil, fmt.Errorf("error reading from backend %s: %w", backend.URL.Host, err)
	}

	if addr.String() != backend.URL.Host {
		return nil, fmt.Errorf("received response from unexpected address %s", addr.String())
	}

	return buf[:n], nil
}
