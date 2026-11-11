// Command server runs a Shopify app service: a small HTTP service that holds
// the Admin API access token of one store (or of each store of a Plus
// organisation sharing the install) and exposes a few endpoints that use it.
// Storefronts call it through the App Proxy (/proxy), servers such as Shopify
// Flow with an API key (/api). See README.md.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"

	"github.com/ChristiantangCC/Shopify-Go-Service-App-Template/internal/apikey"
	"github.com/ChristiantangCC/Shopify-Go-Service-App-Template/internal/appproxy"
	"github.com/ChristiantangCC/Shopify-Go-Service-App-Template/internal/config"
	"github.com/ChristiantangCC/Shopify-Go-Service-App-Template/internal/handlers"
	"github.com/ChristiantangCC/Shopify-Go-Service-App-Template/internal/logging"
	"github.com/ChristiantangCC/Shopify-Go-Service-App-Template/internal/shopify"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	logger, shutdownLogs, err := logging.New(ctx, logging.Options{Level: cfg.LogLevel, Format: cfg.LogFormat, ServiceNamespace: cfg.ServiceNamespace, OTLPEndpoint: cfg.OTLPEndpoint})
	if err != nil {
		return fmt.Errorf("logging: %w", err)
	}
	slog.SetDefault(logger)

	tokens, err := shopify.NewTokenStore(cfg.DataDir, cfg.ClientSecret)
	if err != nil {
		return err
	}
	for _, shop := range cfg.Shops {
		if !tokens.Connected(shop) {
			logger.Warn("[Server] Not connected to Shopify yet: open the app in the store's admin once", "shop", shop)
		}
	}
	admin := shopify.NewClient(cfg.APIVersion, tokens)

	router := chi.NewRouter()
	router.Use(middleware.RequestID, middleware.Recoverer, middleware.Timeout(30*time.Second))
	router.Get("/health", handlers.Health)
	// The App URL: opened in the admin, it connects the store (token exchange).
	router.Method(http.MethodGet, "/", handlers.Install{Shops: cfg.Shops, ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret, Tokens: tokens, Logger: logger})

	// The service's endpoints, mounted under both /proxy and /api below.
	// Example endpoints (read_products, write_products): replace with the
	// service's own, and delete internal/handlers/example_products.go.
	endpoints := func(r chi.Router) {
		r.Method(http.MethodGet, "/example/products", handlers.ListProducts{Admin: admin, Logger: logger})
		r.Method(http.MethodPost, "/example/products", handlers.CreateProduct{Admin: admin, Logger: logger})
	}

	// Storefront and browser callers: only reachable through Shopify's App
	// Proxy, which signs every request, the store it came from included. Rate
	// limited per customer, else per IP.
	router.Route("/proxy", func(r chi.Router) {
		r.Use(appproxy.Verify(appproxy.VerifyOptions{Secret: cfg.ClientSecret, Shops: cfg.Shops, MaxAge: cfg.ProxyMaxAge, Logger: logger}))
		r.Use(httprate.Limit(cfg.RateLimitPerMinute, time.Minute, httprate.WithKeyFuncs(appproxy.RateLimitKey)))
		r.Use(handlers.ProxyShop)
		endpoints(r)
	})

	// Server-to-server callers (Shopify Flow, backends): a bearer API key,
	// and with several stores an X-Shopify-Shop-Domain header naming the one
	// to work on. Off unless API_KEY or API_KEYS is set.
	if len(cfg.Keys) > 0 {
		router.Route("/api", func(r chi.Router) {
			r.Use(apikey.Verify(cfg.Keys, logger))
			r.Use(handlers.APIShop(cfg.Shops))
			endpoints(r)
		})
	}

	server := &http.Server{Addr: fmt.Sprintf(":%d", cfg.Port), Handler: router, ReadHeaderTimeout: 10 * time.Second}
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("[Server] Listening", "port", cfg.Port, "shops", cfg.Shops)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		logger.Info("[Server] Shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("[Server] Shutdown failed", "error", err)
	}
	_ = shutdownLogs(shutdownCtx)
	return nil
}
