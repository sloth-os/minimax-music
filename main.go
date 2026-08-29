// Command minimax-music is an HTTP API service that generates and downloads
// music via the MiniMaxi (Hailuo Audio) web API, with optional HTTP/SOCKS5
// proxy support for all outbound traffic.
//
// Configuration is loaded from a YAML file (path via -config flag, default
// config.yaml) and overridden by MINIMAX_* environment variables. See
// config.example.yaml.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"minimax-music/internal/api"
	"minimax-music/internal/config"
	"minimax-music/internal/minimax"
	"minimax-music/internal/proxy"
)

func main() {
	var (
		configPath string
		addr       string
	)
	flag.StringVar(&configPath, "config", "config.yaml", "path to YAML config file")
	flag.StringVar(&addr, "addr", "", "HTTP listen address (overrides config/env)")
	flag.Parse()

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if addr != "" {
		cfg.Addr = addr
	}
	if cfg.Minimax.Token == "" {
		log.Fatalf("minimax token is required (set minimax.token in %s or MINIMAX_TOKEN env)", configPath)
	}
	if cfg.Minimax.UUID == "" {
		log.Printf("warning: minimax.uuid is empty; the server may reject requests. Set it to the browser uuid.")
	}
	log.Printf("config: %s", cfg)

	// Build the proxy-routed HTTP client and WebSocket dialer.
	httpClient, err := proxy.HTTPClient(cfg.Proxy)
	if err != nil {
		log.Fatalf("proxy http client: %v", err)
	}
	wsDialer, err := proxy.WSDialer(cfg.Proxy)
	if err != nil {
		log.Fatalf("proxy ws dialer: %v", err)
	}

	// Build the MiniMaxi client and inject the WS dialer. *websocket.Dialer
	// satisfies minimax.WSDialer.
	client := minimax.New(cfg.Minimax, httpClient)
	client.SetWSDialer(wsDialer)

	// Build the API server.
	srv := api.New(client)
	srv.SetAuthKey(cfg.Auth.APIKey)
	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	done := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		log.Printf("shutdown signal received")
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("shutdown: %v", err)
		}
		close(done)
	}()

	log.Printf("listening on %s", cfg.Addr)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("listen: %v", err)
	}
	<-done
	log.Printf("stopped")
}
