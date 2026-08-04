// Command waxgrove is the single binary that serves the API and, later, the PWA.
//
// One artifact, no external services required to boot (docs/requirements.md N2).
package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/johnzastrow/waxgrove/internal/config"
	"github.com/johnzastrow/waxgrove/internal/crypto"
	"github.com/johnzastrow/waxgrove/internal/httpapi"
	"github.com/johnzastrow/waxgrove/internal/musicbrainz"
	"github.com/johnzastrow/waxgrove/internal/repository/sqlite"
	"github.com/johnzastrow/waxgrove/internal/resolve"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "genkey" {
		if err := genkey(); err != nil {
			fmt.Fprintln(os.Stderr, "genkey:", err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// genkey prints a fresh AES-256 key for the operator to put in the
// environment. Printed to stdout only, never written to disk by us.
func genkey() error {
	key, err := crypto.GenerateKey()
	if err != nil {
		return err
	}
	fmt.Println(base64.StdEncoding.EncodeToString(key))
	return nil
}

// remoteOrNil avoids handing the resolver a non-nil interface wrapping a nil
// pointer, which would look enabled and then panic on first use.
func remoteOrNil(c *musicbrainz.Client) resolve.Remote {
	if c == nil {
		return nil
	}
	return c
}

func searchOrNil(c *musicbrainz.Client) httpapi.RemoteSearch {
	if c == nil {
		return nil
	}
	return c
}

func run() error {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	slog.Info("configuration loaded", "config", cfg.Redacted())

	// Fail at startup if the key is unusable, rather than at first token write.
	if _, err := crypto.NewSealerFromBase64(cfg.SecretKeyB64); err != nil {
		return fmt.Errorf("secret key: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := sqlite.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = store.Close() }()
	slog.Info("database ready", "path", cfg.DatabaseURL)

	// N6: with no metadata source the ladder runs local-only and every
	// remote-optional path is exercised in production, not just in tests.
	var remote *musicbrainz.Client
	if cfg.RemoteEnabled() {
		remote = musicbrainz.New(cfg.Contact, httpapi.Version)
		slog.Info("metadata source enabled", "source", cfg.MetadataSource)
	} else {
		slog.Info("running with no metadata source; local catalogue and JSPF only")
	}

	api := &httpapi.API{
		Store:    store,
		Resolver: resolve.New(store.Records(), remoteOrNil(remote)),
		Remote:   searchOrNil(remote),
		Env:      cfg.Environment,
		Secure:   cfg.Environment == "production",
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           httpapi.New(store, cfg.Environment).WithAPI(api).Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.Addr, "version", httpapi.Version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
