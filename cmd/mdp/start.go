package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/derekgould/multi-dev-proxy/internal/api"
	"github.com/derekgould/multi-dev-proxy/internal/certs"
	"github.com/derekgould/multi-dev-proxy/internal/inject"
	"github.com/derekgould/multi-dev-proxy/internal/process"
	"github.com/derekgould/multi-dev-proxy/internal/proxy"
	"github.com/derekgould/multi-dev-proxy/internal/registry"
	"github.com/derekgould/multi-dev-proxy/internal/ui"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the multi-dev proxy server",
	RunE:  runStart,
}

func init() {
	rootCmd.AddCommand(startCmd)
	startCmd.Flags().IntP("port", "p", 3000, "Port to listen on")
	startCmd.Flags().String("host", "0.0.0.0", "Host to listen on")
	startCmd.Flags().String("tls-cert", "", "Path to TLS certificate file")
	startCmd.Flags().String("tls-key", "", "Path to TLS key file")
	startCmd.Flags().Bool("no-tls", false, "Disable HTTPS and run plain HTTP")
	startCmd.Flags().String("port-range", "10000-60000", "Range of ports for proxied services")
}

func runStart(cmd *cobra.Command, args []string) error {
	port, _ := cmd.Flags().GetInt("port")
	host, _ := cmd.Flags().GetString("host")
	tlsCert, _ := cmd.Flags().GetString("tls-cert")
	tlsKey, _ := cmd.Flags().GetString("tls-key")
	noTLS, _ := cmd.Flags().GetBool("no-tls")

	if (tlsCert != "") != (tlsKey != "") {
		return fmt.Errorf("both --tls-cert and --tls-key are required")
	}

	useTLS := !noTLS
	if useTLS && tlsCert == "" {
		certDir := certs.DefaultDir()
		var err error
		tlsCert, tlsKey, err = certs.EnsureCert(certDir)
		if err != nil {
			return fmt.Errorf("auto-generate TLS cert: %w", err)
		}
		slog.Info("using auto-generated TLS cert", "dir", certDir)
	}
	if useTLS {
		if _, err := os.Stat(tlsCert); err != nil {
			return fmt.Errorf("tls-cert: %w", err)
		}
		if _, err := os.Stat(tlsKey); err != nil {
			return fmt.Errorf("tls-key: %w", err)
		}
	}

	reg := registry.New()
	prx := proxy.NewProxy(reg, port, useTLS)
	inj := inject.New()
	prx.SetModifyResponse(inj.ModifyResponse)

	addr := fmt.Sprintf("%s:%d", host, port)
	srv := &http.Server{
		Addr:     addr,
		ErrorLog: log.New(io.Discard, "", 0),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /__mdp/health", api.HealthHandler(reg))
	mux.HandleFunc("GET /__mdp/servers", api.ServersHandler(reg))
	mux.HandleFunc("POST /__mdp/register", api.RegisterHandler(reg))
	mux.HandleFunc("DELETE /__mdp/register/{name...}", api.DeregisterHandler(reg))
	mux.HandleFunc("POST /__mdp/switch/{name...}", api.SwitchHandler(reg))
	mux.HandleFunc("GET /__mdp/switch", ui.SwitchPageHandler(reg))
	mux.HandleFunc("GET /__mdp/widget.js", ui.WidgetHandler())
	mux.Handle("/", prx)

	prunerCtx, prunerCancel := context.WithCancel(context.Background())
	defer prunerCancel()
	registry.StartPruner(prunerCtx, reg, 10*time.Second, process.IsProcessAlive)

	srv.Handler = mux

	proto := "http"
	if useTLS {
		proto = "https"
	}
	slog.Info("mdp proxy started",
		"addr", fmt.Sprintf("%s://%s", proto, addr),
		"switch", fmt.Sprintf("%s://localhost:%d/__mdp/switch", proto, port),
	)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() {
		if useTLS {
			cert, err := tls.LoadX509KeyPair(tlsCert, tlsKey)
			if err != nil {
				errCh <- fmt.Errorf("load TLS keypair: %w", err)
				return
			}
			srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
			errCh <- srv.ListenAndServeTLS("", "")
		} else {
			errCh <- srv.ListenAndServe()
		}
	}()

	select {
	case <-sigCh:
		slog.Info("shutting down")
		prunerCancel()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(ctx)
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	}
}
