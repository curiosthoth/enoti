package flow

import (
	"context"
	"encoding/base64"

	"enoti/internal/ports"
	"enoti/internal/types"

	json "github.com/goccy/go-json"
	"github.com/klauspost/compress/zstd"
)

// Action indicates what to do after evaluating the new value against state.
type Action int

var enc, _ = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest))
var dec, _ = zstd.NewReader(nil)

// EvaluateEdgeAndFlap applies edge detection + flapping logic and persists state via CAS.
// Callers SHOULD retry once on CAS collision (see handler below).
func EvaluateEdgeAndFlap(
	ctx context.Context,
	store ports.EdgeStore,
	clientID,
	scopeKey string,
	newVal string,
	f *types.FlapConfig,
	payload map[string]any,
) (Action, map[string]any, error) {
	now := EpochTime()

	edgeInfo, ver, err := store.Load(ctx, clientID, scopeKey)
	if err != nil {
		return NoOp, nil, err
	}
	if edgeInfo == nil {
		ns := types.Edge{
			LastValue:    newVal,
			LastChangeTS: now,
			WindowStart:  now,
			FlipCount:    0,
		}
		ok, err := store.UpsertCAS(ctx, clientID, scopeKey, 0, ns)
		if err != nil {
			return NoOp, nil, err
		}
		if ok {
			return EdgeTriggeredForward, nil, nil // first observation counts as an "edge"
		}
		// CAS raced — ask caller to retry whole evaluation path once.
		return SuppressFlapping, nil, nil
	}

	// Stable -- no change
	if edgeInfo.LastValue == newVal {
		return NoOp, nil, nil
	}

	// Flip observed
	encoded, err := EncodePayload(payload)
	if err != nil {
		return NoOp, nil, err
	}
	edgeInfo.Recent = types.AppendRecent(
		edgeInfo.Recent,
		types.Flip{
			At: now, From: edgeInfo.LastValue, To: newVal,
			// Saves payload
			Payload: encoded,
		},
		types.HardLimitRecentItems,
	)
	edgeInfo.LastValue = newVal
	edgeInfo.LastChangeTS = now

	// Flapping control
	if f != nil {
		// Check the window
		newWindow := false
		if f.WindowSeconds > 0 && now-edgeInfo.WindowStart > int64(f.WindowSeconds) {
			// At this point, we know we saw a new Value that is different from LastValue already.
			// So the first flip in the new window is this one.
			edgeInfo.WindowStart = now
			edgeInfo.FlipCount = 1
			if len(edgeInfo.Recent) > 0 {
				// Keep only the latest flip info for the new window
				// We should also do an edge trigger if just out for the new window
				edgeInfo.Recent = edgeInfo.Recent[len(edgeInfo.Recent)-1:]
			}
			newWindow = true
		} else {
			edgeInfo.FlipCount++
		}

		// Suppress initial flips under tolerance
		if edgeInfo.FlipCount <= f.SuppressBelow {
			_, _ = store.UpsertCAS(ctx, clientID, scopeKey, ver, *edgeInfo)
			return SuppressFlapping, nil, nil
		}

		// Aggregate path
		if f.AggregateAt > 0 && !newWindow {
			var agg map[string]any
			action := SuppressFlapping
			if edgeInfo.FlipCount >= f.AggregateAt && now > edgeInfo.AggUntilTS && len(edgeInfo.Recent) >= f.AggregateAt {
				edgeInfo.AggUntilTS = now + int64(f.AggregateCooldownSeconds)
				agg = BuildAggregate(edgeInfo, f.AggregateMaxItems)
				// Trim the edgeInfo.Recent
				edgeInfo.Recent = nil
				action = AggregateSent
			}
			if ok, err := store.UpsertCAS(ctx, clientID, scopeKey, ver, *edgeInfo); err != nil {
				return SuppressFlapping, nil, err
			} else if ok {
				return action, agg, nil
			} else {
				return NoOp, nil, nil // CAS raced, suppress this time
			}
		}
	}
	if ok, err := store.UpsertCAS(ctx, clientID, scopeKey, ver, *edgeInfo); err != nil {
		return NoOp, nil, err
	} else if ok {
		return EdgeTriggeredForward, nil, nil
	} else {
		return NoOp, nil, nil // CAS raced, suppress this time
	}

}

// EncodePayload encodes the payload as JSON, compresses and base64-url encodes it.
func EncodePayload(d map[string]any) (string, error) {
	s, err := json.Marshal(d)
	if err != nil {
		return "", err
	}
	b := enc.EncodeAll(s, make([]byte, 0, len(s)))
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// DecodePayload decodes the base64-url encoded, compressed payload and decompresses and JSON-decodes it.
func DecodePayload(in string) ([]byte, error) {
	b, err := base64.RawURLEncoding.DecodeString(in)
	if err != nil {
		return []byte{}, err
	}
	out, err := dec.DecodeAll(b, nil)
	if err != nil {
		return []byte{}, err
	}
	return out, nil
}

// BuildAggregate builds the aggregate payload to send.
func BuildAggregate(edgeInfo *types.Edge, k int) map[string]any {
	items := make([]map[string]any, 0, len(edgeInfo.Recent))
	num := len(edgeInfo.Recent)
	if k > 0 && num > 0 {
		if k > num {
			k = num
		}
		for i := num - 1; i > num-k-1; i-- {
			it := edgeInfo.Recent[i]
			var pl map[string]any
			if it.Payload != "" {
				b, err := DecodePayload(it.Payload)
				if err == nil {
					_ = json.Unmarshal(b, &pl)
				}
			}
			items = append(items, map[string]any{
				"at":      it.At,
				"from":    it.From,
				"to":      it.To,
				"payload": pl,
			})
		}
	}
	return map[string]any{
		"type":         "flap_aggregate",
		"scope":        edgeInfo.ScopeKey,
		"last_value":   edgeInfo.LastValue,
		"window_start": edgeInfo.WindowStart,
		"flip_count":   edgeInfo.FlipCount,
		"recent":       items,
	}
}
