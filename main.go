package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/openai/openai-go/v3/option"
	"github.com/tinfoilsh/tinfoil-go"
)

const (
	serverReadTimeout  = time.Minute
	serverWriteTimeout = time.Minute
	shutdownTimeout    = 10 * time.Second
)

func main() {
	cfg, err := LoadConfig()
	if err != nil {
		fatal("invalid configuration", err)
	}

	client, err := tinfoil.NewClient(option.WithAPIKey(cfg.TinfoilAPIKey))
	if err != nil {
		fatal("failed to create Tinfoil client", err)
	}

	service := NewService(cfg,
		NewSafeguardClassifier(client.Client, cfg.SafeguardModel, cfg.SafeguardPolicy),
		NewSafeguardReviewer(client.Client, cfg.SafeguardReviewModel, cfg.SafeguardPolicy),
		NewControlPlaneReporter(cfg.ControlPlaneURL),
	)

	workerCtx, stopWorkers := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	for i := 0; i < cfg.Workers; i++ {
		workers.Go(func() { service.RunWorker(workerCtx) })
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ingest", service.HandleIngest)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	server := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  serverReadTimeout,
		WriteTimeout: serverWriteTimeout,
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		slog.Info("starting", "addr", cfg.ListenAddr, "model", cfg.SafeguardModel, "review_model", cfg.SafeguardReviewModel, "workers", cfg.Workers)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fatal("server failed", err)
		}
	}()

	<-sigChan
	slog.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	server.Shutdown(ctx)
	stopWorkers()
	workers.Wait()
}

func fatal(msg string, err error) {
	slog.Error(msg, "error", err)
	os.Exit(1)
}
