package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"hash/fnv"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

var (
	tmpl = template.Must(template.New("index.html.tmpl").
		Funcs(template.FuncMap{"now": time.Now}).
		ParseFiles("static/index.html.tmpl"))
)

// ServerPool holds the collection of backends.
type ServerPool struct {
	listener       net.Listener
	wg             sync.WaitGroup
	current        uint64
	backends       []*Backend
	backendsMutex  sync.Mutex
	shutdown       chan struct{}
	log            *log.Logger
	stickySessions bool
}

// NewServerPool creates a new ServerPool with the given logger.
func NewServerPool(l *log.Logger, config *Config) (*ServerPool, error) {
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

	pool := &ServerPool{
		listener:       listener,
		shutdown:       make(chan struct{}),
		log:            l,
		stickySessions: config.StickySessions,
	}

	// Add backends from config
	for _, backend := range config.Backends {
		pool.AddBackend(backend)
	}

	return pool, nil
}

func (s *ServerPool) dashboardHandler(w http.ResponseWriter, r *http.Request) {
	if err := tmpl.Execute(w, s.backends); err != nil {
		s.log.Printf("error executing template: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
}

// Start begins accepting connections and handling them.
func (s *ServerPool) Start() {
	s.wg.Add(1)
	go s.acceptLoop()
}

// acceptLoop accepts incoming connections and handles them.
func (s *ServerPool) acceptLoop() {
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
func (s *ServerPool) Shutdown(ctx context.Context) error {
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
func (s *ServerPool) Next(conn net.Conn) *Backend {
	s.backendsMutex.Lock()
	defer s.backendsMutex.Unlock()

	if s.stickySessions {
		ip := getIpfromConn(conn)
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

// findNextHealthyBackend finds the next healthy backend starting from the given index.
func (s *ServerPool) findNextHealthyBackend(start int) *Backend {
	for i := 0; i < len(s.backends); i++ {
		idx := (start + i) % len(s.backends)
		if s.backends[idx].Healthy() {
			return s.backends[idx]
		}
	}
	return nil
}

// AddBackend adds a new backend to the server pool.
func (s *ServerPool) AddBackend(rawUrl string) {
	s.backendsMutex.Lock()
	defer s.backendsMutex.Unlock()
	parsedURL, err := url.Parse(rawUrl)
	if err != nil {
		s.log.Printf("error parsing URL %s: %v\n", rawUrl, err)
		return
	}
	backend := &Backend{
		URL:       parsedURL,
		isHealthy: false,
	}
	s.backends = append(s.backends, backend)
}

// HealthCheck pings a backend to see if it's alive.
func (s *ServerPool) HealthCheck() {
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
func proxy(conn net.Conn, pool *ServerPool, l *log.Logger) {
	defer conn.Close()
	backend := pool.Next(conn)
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

// getIpfromConn extracts the IP address from the connection.
func getIpfromConn(conn net.Conn) net.IP {
	addr := conn.RemoteAddr().String()
	ip := strings.Split(addr, ":")[0]
	return net.ParseIP(ip)
}

// hashIp hashes the IP address to a consistent integer.
func hashIp(ip net.IP) int {
	h := fnv.New32a()
	h.Write(ip)
	hash := h.Sum32()
	return int(hash)
}
