package api

import (
	"context"
	"enoti/internal/flow"
	"enoti/internal/ports"
	"enoti/internal/types"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/goccy/go-json"
)

type Handler struct {
	ClientStore ports.ClientStore
	DataStore   ports.DataStore
	Pub         ports.Publisher
}

type Publisher interface {
	PublishRaw(ctx context.Context, arn string, payload []byte) error
}

func NewHandler(cl ports.ClientStore, es ports.DataStore, pub ports.Publisher) *Handler {
	return &Handler{
		ClientStore: cl,
		DataStore:   es,
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
	cc, err := flow.LoadCachedClientConfig(ctx, h.ClientStore, clientID)
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
		h.DataStore,
		payload)
	if err != nil {
		http.Error(w, err.Error(), statusCode)
		return
	}
	switch action {
	case flow.NoOp, flow.SuppressFlapping, flow.SuppressDedup:
		if err := writeJSON(w, statusCode, map[string]any{"status": flow.StatusTextMap[action]}); err != nil {
			http.Error(w, "failed to write response", http.StatusInternalServerError)
		}
	case flow.AggregateSent:
		b, err := json.Marshal(newPayload)
		if err != nil {
			http.Error(w, "failed to marshal payload", http.StatusInternalServerError)
			return
		}
		if err := h.Pub.PublishRaw(ctx, cc.Trigger.Target.SNSArn, b); err != nil {
			http.Error(w, "failed to publish", http.StatusInternalServerError)
			return
		}
		if err := writeJSON(w, http.StatusAccepted, map[string]any{"status": flow.StatusTextMap[action]}); err != nil {
			http.Error(w, "failed to write response", http.StatusInternalServerError)
		}
	case flow.EdgeTriggeredForward, flow.ForwardedAsIs:
		b, err := json.Marshal(payload)
		if err != nil {
			http.Error(w, "failed to marshal payload", http.StatusInternalServerError)
			return
		}
		if err := h.Pub.PublishRaw(ctx, cc.Trigger.Target.SNSArn, b); err != nil {
			http.Error(w, "failed to publish", http.StatusInternalServerError)
			return
		}
		if err := writeJSON(w, http.StatusAccepted, map[string]any{"status": flow.StatusTextMap[action]}); err != nil {
			http.Error(w, "failed to write response", http.StatusInternalServerError)
		}
	}
}

// clientIP extracts the real client IP from X-Forwarded-For or RemoteAddr.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// If SplitHostPort fails, return the RemoteAddr as-is
		return r.RemoteAddr
	}
	return host
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, code int, v any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	return json.NewEncoder(w).Encode(v)
}
