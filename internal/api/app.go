package api

import (
	"context"
	"enoti/internal/ports"
	"enoti/internal/types"
	"errors"
	"fmt"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	BackendDDB = "ddb"
)

// RunServer runs the HTTP server exposing the `/notify` endpoint. This is a blocking call.
func RunServer(port int,
	clientStore ports.ConfigStore,
	edgeStore ports.EdgeStore,
	rateLimiter ports.RateLimiter,
	publisher ports.Publisher,
) {
	h := NewHandler(
		clientStore,
		rateLimiter,
		edgeStore,
		publisher,
	)

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           h.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("enoti listening on %s\n", srv.Addr)
	log.Fatal(srv.ListenAndServe())
}

// RunServerInterruptible runs the server in the background in a Go routine and immediately returns a chan to
// the caller. The caller can then send a signal to the chan to gracefully shutdown the server.
// It's up to the caller to wait for in the main Go routine to keep the server running.
func RunServerInterruptible(port int,
	clientStore ports.ConfigStore,
	edgeStore ports.EdgeStore,
	rateLimiter ports.RateLimiter,
	publisher ports.Publisher,
) (stop chan<- struct{}, done <-chan error) {
	h := NewHandler(
		clientStore,
		rateLimiter,
		edgeStore,
		publisher,
	)

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           h.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// one-shot channels for control & completion
	stopCh := make(chan struct{})
	doneCh := make(chan error, 1) // buffered so goroutines can finish without blocking

	// server goroutine
	go func() {
		log.Printf("enoti listening on %s\n", srv.Addr)
		err := srv.ListenAndServe()
		// http.ErrServerClosed is returned on Shutdown; treat that as clean exit
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			doneCh <- err
			return
		}
		doneCh <- nil
	}()

	go func() {
		<-stopCh
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx) // graceful; in-flight requests get time to finish
	}()
	return stopCh, doneCh
}

// RunSNSLambdaEntryPoint is for AWS Lambda entry point. This is for receiving event from SNS notifictation
// from within an AWS Lambda. SNSInboundEvent is defined in types.go
func RunSNSLambdaEntryPoint(ctx context.Context,
	event types.SNSInboundEvent,
	clientStore ports.ConfigStore,
	edgeStore ports.EdgeStore,
	rateLimiter ports.RateLimiter,
	publisher ports.Publisher,
) error {
	return nil
}
