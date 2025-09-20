package api

import (
	"context"
	"encoding/json"
	"enoti/internal/flow"
	"enoti/internal/ports"
	"enoti/internal/types"
	"io"
	"net"
	"net/http"
	"strings"
)

type Handler struct {
	ConfigStore ports.ConfigStore
	Rate        ports.RateLimiter
	Edge        ports.EdgeStore
	Pub         ports.Publisher
}

type Publisher interface {
	PublishRaw(ctx context.Context, arn string, payload []byte) error
}

func NewHandler(cl ports.ConfigStore, rl ports.RateLimiter, es ports.EdgeStore, pub ports.Publisher) *Handler {
	return &Handler{
		ConfigStore: cl,
		Rate:        rl,
		Edge:        es,
		Pub:         pub,
	}
}

func (h *Handler) Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/notify", h.handleNotify)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func (h *Handler) handleNotify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	clientID := r.Header.Get(types.ClientIDHdrName)
	clientKey := r.Header.Get(types.ClientKeyHdrName)
	// Config (TTL cache → store)
	ctx := r.Context()
	cc, err := flow.LoadCachedClientConfig(ctx, h.ConfigStore, clientID)
	if err != nil {
		http.Error(w, "unknown client", http.StatusUnauthorized)
		return
	}
	err = flow.Auth(ctx, cc, clientID, clientKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	// Read body
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	defer func() {
		_ = r.Body.Close()
	}()
	if len(body) == 0 {
		http.Error(w, "empty body", http.StatusBadRequest)
		return
	}
	var payload map[string]any
	err = json.Unmarshal(body, &payload)
	if err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	action, statusCode, newPayload, err := flow.Run(
		ctx, clientID, clientIP(r), cc,
		h.Rate, h.Edge,
		payload)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
	switch action {
	case flow.NoOp, flow.SuppressFlapping, flow.SuppressDedup:
		writeJSON(w, statusCode, map[string]any{"status": flow.StatusTextMap[action]})
	case flow.AggregateSent:
		b, _ := json.Marshal(newPayload)
		_ = h.Pub.PublishRaw(ctx, cc.Trigger.Target.SNSArn, b)
		writeJSON(w, http.StatusAccepted, map[string]any{"status": flow.StatusTextMap[action]})
	case flow.EdgeTriggeredForward, flow.ForwardedAsIs:
		b, _ := json.Marshal(payload)
		_ = h.Pub.PublishRaw(ctx, cc.Trigger.Target.SNSArn, b)
		writeJSON(w, http.StatusAccepted, map[string]any{"status": flow.StatusTextMap[action]})
	}
}

// clientIP extracts the real client IP from X-Forwarded-For or RemoteAddr.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
