package cluster

import (
	"context"
	"infra-foundation/metric"
	"infra-foundation/model"
	"net/http"
	"strings"
)

type HTTPServer struct {
	httpServer   *http.Server
	modelManager *model.ModelManager
}

func (h *HTTPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/debug/metrics" {
		metric.ServeHTTP(w, r)
		return
	}
	paths := strings.Split(r.URL.Path, "/")
	if len(paths) < 3 {
		http.NotFound(w, r)
		return
	}
	h.modelManager.DispatchHTTP(paths[1], w, r)
}

func (h *HTTPServer) Listen(addr string) {
	h.httpServer = &http.Server{Addr: addr, Handler: h}
	go func() {
		if err := h.httpServer.ListenAndServe(); err != nil {
			panic(err)
		}
	}()
}

func (h *HTTPServer) Shutdown(ctx context.Context) {
	if h.httpServer == nil {
		return
	}
	h.httpServer.Shutdown(ctx)
}
