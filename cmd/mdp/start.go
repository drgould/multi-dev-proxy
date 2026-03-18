package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/derekgould/multi-dev-proxy/internal/api"
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
	startCmd.Flags().String("port-range", "10000-60000", "Range of ports for proxied services")
}

func runStart(cmd *cobra.Command, args []string) error {
	port, _ := cmd.Flags().GetInt("port")
	host, _ := cmd.Flags().GetString("host")
	tlsCert, _ := cmd.Flags().GetString("tls-cert")
	tlsKey, _ := cmd.Flags().GetString("tls-key")

	useTLS := tlsCert != "" || tlsKey != ""
	if useTLS && (tlsCert == "" || tlsKey == "") {
		return fmt.Errorf("both --tls-cert and --tls-key are required for HTTPS")
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
	srv := &http.Server{Addr: addr, Handler: nil}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /__mdp/health", api.HealthHandler(reg))
	mux.HandleFunc("GET /__mdp/servers", api.ServersHandler(reg))
	tlsUpgrade := func(certPath, keyPath string) {
		if useTLS {
			return
		}
		slog.Info("received TLS certs from upstream, upgrading to HTTPS", "cert", certPath, "key", keyPath)
		useTLS = true
		prx = proxy.NewProxy(reg, port, true)
		inj2 := inject.New()
		prx.SetModifyResponse(inj2.ModifyResponse)
		mux.Handle("/", prx)

		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			srv.Shutdown(ctx)

			newSrv := &http.Server{Addr: addr, Handler: mux}
			cert, err := tls.LoadX509KeyPair(certPath, keyPath)
			if err != nil {
				slog.Error("failed to load forwarded TLS certs", "err", err)
				return
			}
			newSrv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
			slog.Info("proxy restarted with HTTPS", "addr", fmt.Sprintf("https://%s", addr))
			srv = newSrv
			if err := newSrv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
				slog.Error("HTTPS server error", "err", err)
			}
		}()
	}
	mux.HandleFunc("POST /__mdp/register", api.RegisterHandler(reg, tlsUpgrade))
	mux.HandleFunc("DELETE /__mdp/register/{name}", api.DeregisterHandler(reg))
	mux.HandleFunc("POST /__mdp/switch/{name}", api.SwitchHandler(reg))
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
