package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"net/url"
	"sync"
	"text/template"
	"time"
)

type ServerPool interface {
	Next(conn net.Addr) *Backend
	AddBackend(rawUrl string)
	HealthCheck()
	Start() error
	Shutdown(ctx context.Context) error
	dashboardHandler(w http.ResponseWriter, r *http.Request)
}

var (
	tmpl = template.Must(template.New("index.html.tmpl").
		Funcs(template.FuncMap{"now": time.Now}).
		ParseFiles("static/index.html.tmpl"))
)

type BaseServerPool struct {
	backends       []*Backend
	current        uint64
	backendsMutex  sync.Mutex
	stickySessions bool
	log            *log.Logger
}

// AddBackend adds a new backend to the server pool.
func (p *BaseServerPool) AddBackend(rawUrl string) {
	p.backendsMutex.Lock()
	defer p.backendsMutex.Unlock()
	parsedURL, err := url.Parse(rawUrl)
	if err != nil {
		p.log.Printf("error parsing URL %s: %v\n", rawUrl, err)
		return
	}
	backend := &Backend{
		URL:       parsedURL,
		isHealthy: false,
	}
	p.backends = append(p.backends, backend)
}

// Next returns the next available backend using round-robin.
func (p *BaseServerPool) Next(conn net.Addr) *Backend {
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

// findNextHealthyBackend finds the next healthy backend starting from the given index.
func (p *BaseServerPool) findNextHealthyBackend(start int) *Backend {
	for i := 0; i < len(p.backends); i++ {
		idx := (start + i) % len(p.backends)
		if p.backends[idx].Healthy() {
			return p.backends[idx]
		}
	}
	return nil
}

func (p *BaseServerPool) dashboardHandler(w http.ResponseWriter, _ *http.Request) {
	if err := tmpl.Execute(w, p.backends); err != nil {
		p.log.Printf("error executing template: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
}
