package main

import (
	"net/url"
	"sync"
)

// Backend represents a backend server with its URL and status.
type Backend struct {
	URL          *url.URL
	mux          sync.Mutex
	ReverseProxy bool // Not used in this simple TCP example, but common in HTTP LBs
	isHealthy    bool
}

// Healthy checks the status of the backend.
func (b *Backend) Healthy() bool {
	b.mux.Lock()
	defer b.mux.Unlock()
	return b.isHealthy
}

// SetHealthy sets the status of the backend.
func (b *Backend) SetHealthy(healthy bool) {
	b.mux.Lock()
	defer b.mux.Unlock()
	b.isHealthy = healthy
}
