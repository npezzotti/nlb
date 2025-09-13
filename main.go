package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatalf("error: %v", err)
	}
}

func run(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("please provide the path to the config file as the first argument")
	}
	var err error
	config, err := loadConfig(args[0])
	if err != nil {
		return fmt.Errorf("failed to load config: %v", err)
	}

	l := log.New(os.Stdout, "nlb: ", log.LstdFlags)

	var pool ServerPool
	switch config.Protocol {
	case "tcp":
		pool, err = NewTCPServerPool(l, config)
	case "udp":
		pool, err = NewUDPServerPool(l, config)
	default:
		return fmt.Errorf("unsupported protocol: %s", config.Protocol)
	}
	if err != nil {
		return fmt.Errorf("failed to create server pool: %v", err)
	}

	if len(args) < 1 {
		return fmt.Errorf("please provide path to config file as first argument")
	}

	pool.StartHealthChecks()
	pool.Start()

	// Setup HTTP handlers for the dashboard
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	mux.HandleFunc("/", pool.dashboardHandler)
	srv := &http.Server{Addr: config.ConsoleAddr, Handler: mux}

	httpErrChan := make(chan error, 1)
	go func() {
		httpErrChan <- srv.ListenAndServe()
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)

	select {
	case err := <-httpErrChan:
		return fmt.Errorf("http server error: %v", err)
	case sig := <-sigChan:
		l.Printf("received signal: %s", sig)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := pool.Shutdown(ctx); err != nil {
		l.Printf("error during shutdown: %v", err)
	}

	if err := srv.Shutdown(ctx); err != nil {
		l.Printf("error shutting down http server: %v", err)
	}

	return nil
}
