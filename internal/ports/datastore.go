package ports

import (
	"context"
	"enoti/internal/types"
	"time"
)

// DataStore persists edge-detection state + flapping counters. It also provides
// a simple rate-limiter for the Acquire() method.
// Implementations MUST support compare-and-set (CAS) semantics to avoid races.
type DataStore interface {
	// Acquire attempts a slot in the given scope for the provided window.
	// ratePerWindow is the maximum allowed **successful** acquires in the window.
	// Returns (true,nil) if granted; (false,nil) if rate-limited.
	Acquire(ctx context.Context, scope string, ratePerWindow int, window time.Duration) (bool, error)

	// Load returns the edge state and a monotonic version suitable for CAS.
	// If no state exists, (nil,0,nil) MUST be returned.
	Load(ctx context.Context, clientID, scopeKey string) (*types.Edge, int64, error)

	// UpsertCAS creates or updates the edge state only if the version matches.
	// If prevVersion==0, the item MUST NOT already exist.
	// Returns true on success (committed), false if precondition failed, error for I/O.
	UpsertCAS(ctx context.Context, clientID, scopeKey string, prevVersion int64, next types.Edge) (bool, error)
}
