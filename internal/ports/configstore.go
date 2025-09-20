package ports

import (
	"context"
	"enoti/internal/types"
)

// ConfigStore retrieves immutable-ish client configuration used to guard requests.
// Implementations SHOULD cache upstream reads where possible; callers MAY add an
// in-process TTL cache to avoid hot-path lookups.
type ConfigStore interface {
	// GetClientConfig returns the configuration for a clientID.
	// MUST return errors.ErrNotFound if the client does not exist.
	GetClientConfig(ctx context.Context, clientID string) (types.ClientConfig, error)

	ListClients(ctx context.Context) ([]string, error)

	PutClientConfig(ctx context.Context, clientID string, config types.ClientConfig) error

	DeleteClientConfig(ctx context.Context, clientID string) error

	// ClearAll purges all client configurations and data. Used in tests only.
	ClearAll(ctx context.Context) error
}
