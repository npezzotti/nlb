package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/url"
	"sync"
	"time"
)

type UDPServerPool struct {
	BaseServerPool
	conn     *net.UDPConn
	wg       sync.WaitGroup
	shutdown chan struct{}
}

func NewUDPServerPool(l *log.Logger, config *Config) *UDPServerPool {
	pool := &UDPServerPool{
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
	return pool
}

func (p *UDPServerPool) HealthCheck() {
	for _, b := range p.backends {
		go func(backend *Backend) {
			for {
				conn, err := net.DialTimeout("udp", backend.URL.Host, 2*time.Second)
				if err != nil {
					backend.SetHealthy(false)
					p.log.Printf("error connecting to backend %s: %v", backend.URL.Host, err)
					p.log.Printf("backend %s is down", backend.URL.Host)
				} else {
					backend.SetHealthy(true)
					conn.Close()
				}
				time.Sleep(10 * time.Second) // Check every 10 seconds
			}
		}(b)
	}
}

func (u *UDPServerPool) AddBackend(rawUrl string) {
	u.backendsMutex.Lock()
	defer u.backendsMutex.Unlock()
	parsedURL, err := url.Parse(rawUrl)
	if err != nil {
		u.log.Printf("error parsing URL %s: %v\n", rawUrl, err)
		return
	}
	backend := &Backend{
		URL:       parsedURL,
		isHealthy: true,
	}
	u.backends = append(u.backends, backend)
}

func (u *UDPServerPool) Start() error {
	var err error
	u.conn, err = net.ListenUDP("udp", &net.UDPAddr{
		Port: 9090,
	})
	if err != nil {
		return fmt.Errorf("error starting udp server: %w", err)
	}
	u.log.Printf("UDP server started on %s", u.conn.LocalAddr().String())

	u.wg.Add(1)
	go u.acceptUDPConnections()
	return nil
}

func (u *UDPServerPool) Shutdown(ctx context.Context) error {
	start := time.Now()

	select {
	case <-u.shutdown:
		// Already closed
		return nil
	default:
		close(u.shutdown)
	}

	var err error
	if u.conn != nil {
		err = u.conn.Close()
	}
	if err != nil {
		return fmt.Errorf("error closing UDP connection: %w", err)
	}

	done := make(chan struct{})
	go func() {
		u.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Shutdown completed
	case <-ctx.Done():
		return fmt.Errorf("shutdown timed out: %ws", ctx.Err())
	}

	elapsed := time.Since(start)
	u.log.Printf("server pool shutdown completed in %s", elapsed)
	return nil
}

func (u *UDPServerPool) acceptUDPConnections() {
	defer u.wg.Done()

	buf := make([]byte, 65507) // Max UDP payload size
	for {
		select {
		case <-u.shutdown:
			return
		default:
			n, addr, err := u.conn.ReadFromUDP(buf)
			if err != nil {
				select {
				case <-u.shutdown:
					return // Shutdown signal received
				default:
					u.log.Printf("error accepting connection: %v\n", err)
					continue
				}
			}
			go u.handleConnection(addr, buf[:n])
		}
	}
}

func (u *UDPServerPool) handleConnection(clientAddr *net.UDPAddr, data []byte) {
	backend := u.Next(clientAddr)
	if backend == nil {
		u.log.Printf("No healthy backend available")
		return
	}
	resp, err := u.forwardToBackend(backend, data)
	if err != nil {
		u.log.Printf("Error forwarding to backend: %v", err)
		return
	}
	if _, err := u.conn.WriteToUDP(resp, clientAddr); err != nil {
		u.log.Printf("Error writing response to client: %v", err)
	}
}

func (u *UDPServerPool) forwardToBackend(backend *Backend, data []byte) ([]byte, error) {
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
