package server

import (
	"context"
	"net"
	"net/http"
	"time"
)

// WorkerHealthHandler checks the database used by the running document worker.
// It exposes only readiness; the worker does not serve application API routes.
func WorkerHealthHandler(ping func(context.Context) error) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "application/json")
		if err := ping(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"unavailable"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	return mux
}

// RunWorkerHealth serves readiness until shutdown or a listener failure.
func RunWorkerHealth(ctx context.Context, port string, ping func(context.Context) error) error {
	listener, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return err
	}
	server := &http.Server{
		Handler: WorkerHealthHandler(ping), ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second,
	}
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	select {
	case err := <-finished:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			return err
		}
		<-finished
		return nil
	}
}
