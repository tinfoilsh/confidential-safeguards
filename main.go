package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/openai/openai-go/v3/option"
	log "github.com/sirupsen/logrus"
	"github.com/tinfoilsh/tinfoil-go"

	"github.com/tinfoilsh/confidential-safeguards/config"
)

const (
	serverReadTimeout  = time.Minute
	serverWriteTimeout = time.Minute
	shutdownTimeout    = 10 * time.Second
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	client, err := tinfoil.NewClient(option.WithAPIKey(cfg.TinfoilAPIKey), option.WithMaxRetries(0))
	if err != nil {
		log.Fatalf("Failed to create Tinfoil client: %v", err)
	}

	service := NewService(cfg,
		NewSafeguardClassifier(client.Client, cfg.SafeguardModel, cfg.SafeguardPolicy),
		NewSafeguardReviewer(client.Client, cfg.SafeguardReviewModel, cfg.SafeguardPolicy),
		NewControlPlane(cfg.ControlPlaneURL),
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
		log.Infof("Starting on %s (model: %s, workers: %d)", cfg.ListenAddr, cfg.SafeguardModel, cfg.Workers)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	<-sigChan
	log.Info("Shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	server.Shutdown(ctx)
	stopWorkers()
	workers.Wait()
}
