package redis

import (
	"context"
	"enoti/internal/types"
	"fmt"

	"github.com/goccy/go-json"
	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
)

const (
	configKeyNameTemplate = "_enoti_cfg_%s"
)

type ClientStore struct {
	cli *redis.Client
}

func NewClientStore(cli *redis.Client) *ClientStore {
	return &ClientStore{cli: cli}
}

func (s *ClientStore) GetClientConfig(ctx context.Context, clientID string) (types.ClientConfig, error) {
	out := s.cli.Get(ctx, getClientKey(clientID))
	if out.Err() != nil {
		return types.ClientConfig{}, out.Err()
	}
	var cfg types.ClientConfig
	if err := json.Unmarshal([]byte(out.Val()), &cfg); err != nil {
		return types.ClientConfig{}, err
	}
	return cfg, nil
}

func (s *ClientStore) ListClients(ctx context.Context) ([]string, error) {
	out := s.cli.Keys(ctx, getClientKey(""))
	if out.Err() != nil {
		return nil, out.Err()
	}
	keys := out.Val()
	clients := make([]string, 0, len(keys))
	prefixLen := len(fmt.Sprintf(configKeyNameTemplate, ""))
	for _, k := range keys {
		if len(k) > prefixLen {
			clients = append(clients, k[prefixLen:])
		}
	}
	return clients, nil
}

func (s *ClientStore) PutClientConfig(ctx context.Context, clientID string, config types.ClientConfig) error {

	if err := config.Validate(); err != nil {
		return err
	}

	out, err := json.Marshal(config)
	if err != nil {
		return err
	}

	outS := s.cli.Set(
		ctx,
		getClientKey(clientID),
		string(out),
		0,
	)
	return outS.Err()
}

func (s *ClientStore) DeleteClientConfig(ctx context.Context, clientID string) error {
	out := s.cli.Del(ctx, getClientKey(clientID))
	return out.Err()
}
func (s *ClientStore) ClearAll(ctx context.Context) error {
	out := s.cli.Keys(ctx, getClientKey("*"))
	if out.Err() != nil {
		return out.Err()
	}
	keys := out.Val()
	if len(keys) == 0 {
		return nil
	}
	stubLen := len(fmt.Sprintf(configKeyNameTemplate, ""))
	for _, key := range keys {
		// Extract client ID from key
		// and delete associated data keys
		// assuming data keys are prefixed with "_enoti_data_<clientID>_"
		// Adjust the prefix as per your actual data key naming convention
		clientID := key[stubLen:]
		out = s.cli.Keys(ctx, getDataKeyName(clientID, "*"))
		if out.Err() != nil {
			log.Error(out.Err())
			continue
		}
		dataKeys := out.Val()
		if len(dataKeys) > 0 {
			outDel := s.cli.Del(ctx, dataKeys...)
			if outDel.Err() != nil {
				log.Error(outDel.Err())
			}
		}
	}
	outN := s.cli.Del(ctx, keys...)
	return outN.Err()
}

func getClientKey(id string) string {
	return fmt.Sprintf(configKeyNameTemplate, id)
}
