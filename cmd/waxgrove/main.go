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
	"strings"
	"syscall"
	"time"

	"github.com/johnzastrow/waxgrove/internal/config"
	"github.com/johnzastrow/waxgrove/internal/connector"
	"github.com/johnzastrow/waxgrove/internal/crypto"
	"github.com/johnzastrow/waxgrove/internal/httpapi"
	"github.com/johnzastrow/waxgrove/internal/jobs"
	"github.com/johnzastrow/waxgrove/internal/memlimit"
	"github.com/johnzastrow/waxgrove/internal/musicbrainz"
	"github.com/johnzastrow/waxgrove/internal/repository/sqlite"
	"github.com/johnzastrow/waxgrove/internal/resolve"
	"github.com/johnzastrow/waxgrove/internal/spotify"
	"github.com/johnzastrow/waxgrove/internal/webui"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "genkey":
			if err := genkey(); err != nil {
				fmt.Fprintln(os.Stderr, "genkey:", err)
				os.Exit(1)
			}
			return
		case "healthcheck":
			if err := healthcheck(); err != nil {
				fmt.Fprintln(os.Stderr, "healthcheck:", err)
				os.Exit(1)
			}
			return
		}
	}
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// healthcheck probes the running server from inside its own container.
//
// The production image is distroless: there is no curl, no wget, and no shell,
// so the binary has to be able to check itself. It probes 127.0.0.1 explicitly
// rather than "localhost" — that name can resolve to ::1 first, and a server
// bound only to IPv4 then fails a health check while serving traffic perfectly.
func healthcheck() error {
	addr := os.Getenv("WAXGROVE_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	// A listen address of 0.0.0.0:8080 or :8080 is not a dial address.
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		addr = "127.0.0.1" + addr[i:]
	}

	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get("http://" + addr + "/health")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
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

	// Before anything allocates: the collector needs to know it is in a box.
	// Without this it sizes the heap against GOGC alone and grows until the
	// kernel kills the process (see internal/memlimit).
	if msg, err := memlimit.Apply(); err == nil {
		slog.Info("memory limit", "detail", msg)
	} else if !errors.Is(err, memlimit.ErrNoLimit) {
		slog.Warn("could not read the cgroup memory limit", "err", err)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	slog.Info("configuration loaded", "config", cfg.Redacted())

	// Fail at startup if the key is unusable, rather than at first token write.
	sealer, err := crypto.NewSealerFromBase64(cfg.SecretKeyB64)
	if err != nil {
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

	resolver := resolve.New(store.Records(), remoteOrNil(remote))

	// The Spotify connector, and the job runner that drives it. Both are
	// optional: N6 makes an instance with no connector a supported
	// configuration, and the routes are simply not registered without one.
	var (
		spotifyConn *connector.Spotify
		runner      *jobs.Runner
	)
	if cfg.SpotifyEnabled {
		spotifyConn = connector.NewSpotify(
			spotify.New(), store.Credentials(sealer), store.ProviderRefs(), cfg.BaseURL)
		runner = jobs.NewRunner(store, spotifyConn, resolver)
		go runner.Start(ctx)
		slog.Info("spotify connector enabled",
			"redirect_uri", spotifyConn.RedirectURI())
	} else {
		slog.Info("running with no streaming connector; JSPF import and export only")
	}

	api := &httpapi.API{
		Store:    store,
		Resolver: resolver,
		Remote:   searchOrNil(remote),
		Env:      cfg.Environment,
		Secure:   cfg.Environment == "production",
		Spotify:  spotifyConn,
		Jobs:     runner,
		Sealer:   sealer,
	}

	// The PWA is served from the same binary and the same origin, which is what
	// lets the session cookie be HttpOnly + SameSite=Lax with no CORS at all.
	web, err := webui.Handler()
	if err != nil {
		return fmt.Errorf("web ui: %w", err)
	}
	if !webui.Built() {
		slog.Warn("serving a placeholder web ui; run `make web` to build the app")
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           httpapi.New(store, cfg.Environment).WithAPI(api).WithWebUI(web).Routes(),
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
