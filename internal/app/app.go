package app

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/example/ocservapi/internal/config"
	"github.com/example/ocservapi/internal/httpapi"
	"github.com/example/ocservapi/internal/store"
)

type App struct {
	cfg    config.Config
	store  *store.Store
	server *http.Server
}

func Run(ctx context.Context, configPath, version string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	if _, err := os.ReadFile(cfg.Storage.MasterKeyPath); err != nil {
		return fmt.Errorf("read master key: %w", err)
	}

	st, err := store.Open(ctx, cfg, version)
	if err != nil {
		return err
	}
	defer st.Close()

	if err := st.RunMigrations(); err != nil {
		return err
	}
	if err := st.Bootstrap(ctx); err != nil {
		return err
	}

	handler := httpapi.NewServer(httpapi.Options{
		Store:         st,
		Version:       version,
		SessionTTL:    12 * time.Hour,
		MasterKeyPath: cfg.Storage.MasterKeyPath,
	})
	server := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown error: %v", err)
		}
	}()

	log.Printf("ocservapi listening on %s", cfg.Server.Listen)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("listen: %w", err)
	}
	return nil
}
