package flow

import (
	"enoti/internal/types"
	"sync"
	"time"
)

// TTL is a minimal in-process TTL cache to trim backend reads on hot paths.
// Caller chooses sensible TTL (e.g., 30–60s for client config).
// Lazy expiration on Get.
type TTL[K comparable, V any] struct {
	mu   sync.RWMutex
	data map[K]entry[V]
}

type entry[V any] struct {
	val V
	exp time.Time
}

func NewTTL[K comparable, V any]() *TTL[K, V] {
	return &TTL[K, V]{data: make(map[K]entry[V])}
}

// Get returns the value and true if found and not expired; otherwise zero value and false.
func (t *TTL[K, V]) Get(k K) (V, bool) {
	t.mu.RLock()
	e, ok := t.data[k]
	t.mu.RUnlock()
	if !ok || time.Now().After(e.exp) {
		var zero V
		return zero, false
	}
	return e.val, true
}

func (t *TTL[K, V]) Set(k K, v V, ttl time.Duration) {
	t.mu.Lock()
	t.data[k] = entry[V]{val: v, exp: time.Now().Add(ttl)}
	t.mu.Unlock()
}

// cfgCache is a small TTL cache avoids a read per request on client config.
var cfgCache *TTL[string, types.ClientConfig]

func init() {
	cfgCache = NewTTL[string, types.ClientConfig]()
}
