package redis

import (
	"context"
	"enoti/internal/types"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/goccy/go-json"
	"github.com/redis/go-redis/v9"
)

const (
	dataKeyNameTemplate   = "_enoti_data_%s_s%s"
	windowKeyNameTemplate = "_enoti_rwin_%s_%d" // for rate limiting
)

// DataStore implements ports.DedupStore using a TTL item per key.
type DataStore struct {
	cli *redis.Client
}

func NewDataStore(cli *redis.Client) *DataStore {
	return &DataStore{cli: cli}
}

// Load returns the edge state and a monotonic version suitable for CAS.
// If no state exists, (nil,0,nil) MUST be returned.
func (s *DataStore) Load(ctx context.Context, clientID, scopeKey string) (*types.Edge, int64, error) {
	out := s.cli.HGetAll(ctx, getDataKeyName(clientID, scopeKey))
	if out.Err() != nil {
		if errors.Is(out.Err(), redis.Nil) {
			return nil, 0, nil
		}
		return nil, 0, out.Err()
	}

	m := out.Val()
	if len(m) == 0 {
		return nil, 0, nil
	}
	ver, err := strconv.ParseInt(m["ver"], 10, 64)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid ver: %w", err)
	}
	lastChangeTS, err := strconv.ParseInt(m["last_change_ts"], 10, 64)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid last_change_ts: %w", err)
	}
	windowStart, err := strconv.ParseInt(m["window_start"], 10, 64)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid window_start: %w", err)
	}
	flipCount, err := strconv.Atoi(m["flip_count"])
	if err != nil {
		return nil, 0, fmt.Errorf("invalid flip_count: %w", err)
	}
	aggUntilTS, err := strconv.ParseInt(m["agg_until_ts"], 10, 64)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid agg_until_ts: %w", err)
	}
	var recent []types.Flip
	if err := json.Unmarshal([]byte(m["recent"]), &recent); err != nil {
		return nil, 0, fmt.Errorf("invalid recent: %w", err)
	}

	edge := &types.Edge{
		ScopeKey:     scopeKey,
		LastValue:    m["last_value"],
		LastChangeTS: lastChangeTS,
		WindowStart:  windowStart,
		FlipCount:    flipCount,
		Recent:       recent,
		AggUntilTS:   aggUntilTS,
	}
	return edge, ver, nil
}

// UpsertCAS creates or updates the row only if ver matches prevVersion.
// On create (prevVersion==0), the row must not exist (attribute_not_exists).
func (s *DataStore) UpsertCAS(ctx context.Context, clientID, scopeKey string, prevVersion int64, next types.Edge) (bool, error) {
	next.ScopeKey = scopeKey // safety
	if prevVersion == 0 {
		next.Version = 1
		recentMarshaled, err := json.Marshal(next.Recent)
		if err != nil {
			return false, err
		}
		av := map[string]any{
			"scope_key":      next.ScopeKey,
			"last_value":     next.LastValue,
			"last_change_ts": next.LastChangeTS,
			"window_start":   next.WindowStart,
			"flip_count":     next.FlipCount,
			"recent":         recentMarshaled,
			"agg_until_ts":   next.AggUntilTS,
			"ver":            next.Version,
		}
		// Set all fields
		out := s.cli.HMSet(ctx, getDataKeyName(clientID, scopeKey), av)
		if out.Err() != nil {
			return false, out.Err()
		}
		return true, nil
	}

	// Update with version bump under condition ver == prevVersion
	// with Redis
	currentVerObj, err := s.cli.HMGet(ctx, getDataKeyName(clientID, scopeKey), "ver").Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return false, nil // key does not exist
		}
		return false, err
	}

	currentVerStr, ok := currentVerObj[0].(string)
	if !ok || currentVerStr == "" {
		return false, nil // key does not exist
	}
	currenVersion, err := strconv.ParseInt(currentVerStr, 10, 64)
	if err != nil {
		return false, fmt.Errorf("invalid ver: %w", err)
	}
	if currenVersion != prevVersion {
		return false, nil // version mismatch
	}

	recentMarshaled, err := json.Marshal(next.Recent)
	if err != nil {
		return false, err
	}

	outN := s.cli.HMSet(ctx, getDataKeyName(clientID, scopeKey), map[string]interface{}{
		"last_value":     next.LastValue,
		"last_change_ts": next.LastChangeTS,
		"window_start":   next.WindowStart,
		"flip_count":     next.FlipCount,
		"recent":         string(recentMarshaled),
		"agg_until_ts":   next.AggUntilTS,
		"ver":            currenVersion + 1,
	})
	return true, outN.Err()
}

func (s *DataStore) Acquire(ctx context.Context, key string, ratePerWindow int, window time.Duration) (bool, error) {
	if ratePerWindow <= 0 {
		return false, nil
	}
	// Window bucketing by integer minutes only (simple, predictable).
	// We use the minimum of (window, 60s) when deriving TTL — avoid long-lived keys.
	epochMin := time.Now().Unix() / 60

	// Atomic: ADD count 1, set ttl if absent, condition count < capacity
	// Check capacity first
	cacheKey := getWindowKeyName(key, epochMin)
	outC := s.cli.HGet(ctx, cacheKey, "count")
	if outC.Err() != nil {
		if errors.Is(outC.Err(), redis.Nil) {
			// does not exist yet, proceed
			out := s.cli.HIncrBy(ctx, cacheKey, "count", 1)
			e1 := out.Err()
			if e1 != nil {
				return false, e1
			}
			outb := s.cli.Expire(ctx, cacheKey, 2*window)
			e2 := outb.Err()
			return e2 == nil, e2
		}
		return false, outC.Err()
	}
	if outC.Val() != "" {
		count, err := strconv.Atoi(outC.Val())
		if err != nil {
			return false, fmt.Errorf("invalid count: %w", err)
		}
		if count >= ratePerWindow {
			return false, nil // at capacity
		}
	}
	// Item exits path
	out := s.cli.HIncrBy(ctx, cacheKey, "count", 1)
	if out.Err() != nil {
		return false, out.Err()
	}

	return true, nil
}

func getDataKeyName(clientID, scopeKey string) string {
	return fmt.Sprintf(dataKeyNameTemplate, clientID, scopeKey)
}
func getWindowKeyName(key string, epochMin int64) string {
	return fmt.Sprintf(windowKeyNameTemplate, key, epochMin)
}
