package cluster

import (
	"context"
	"fmt"
	"infra-foundation/logx"
	"infra-foundation/metric"
	"net/http"
	"strings"
)

type HTTPServer struct {
	httpServer *http.Server
	dispatcher ModelDispatcher
	errCh      chan<- error
}

func (h *HTTPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/debug/metrics" {
		metric.ServeHTTP(w, r)
		return
	}
	paths := strings.Split(r.URL.Path, "/")
	if len(paths) < 3 || paths[1] == "" {
		http.NotFound(w, r)
		return
	}
	h.dispatcher.DispatchHTTP(paths[1], w, r)
}

func (h *HTTPServer) Listen(addr string) {
	h.httpServer = &http.Server{Addr: addr, Handler: h}
	go func() {
		if err := h.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logx.Err.Printf("[HTTPServer] ListenAndServe: %v", err)
			select {
			case h.errCh <- fmt.Errorf("http server: %w", err):
			default:
			}
		}
	}()
}

func (h *HTTPServer) Shutdown(ctx context.Context) error {
	if h.httpServer == nil {
		return nil
	}
	if err := h.httpServer.Shutdown(ctx); err != nil {
		return err
	}
	return nil
}
