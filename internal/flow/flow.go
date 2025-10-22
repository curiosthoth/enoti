package flow

import (
	"context"
	"enoti/internal/ports"
	"enoti/internal/types"
	"fmt"
	"hash/fnv"
	"net/http"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

// Auth checks the clientID and clientKey against the config store.
// Returns nil if authenticated, error otherwise.
func Auth(ctx context.Context, cc types.ClientConfig, clientID, clientKey string) error {
	if clientID == "" || clientKey == "" {
		return fmt.Errorf("missing headers")
	}
	// Later we can have more complex auth schemes.
	if strings.Compare(clientKey, cc.ClientKey) != 0 {
		return fmt.Errorf("invalid credentials")
	}
	return nil
}

// Run is the core logic to process a notification payload. It returns the action to take for the next publishing step.
// Note that rate limiting are not deemed as errors, instead they are indicated in the return values and proper statusCode
// to pass back to the caller.
func Run(ctx context.Context, clientID, clientIP string,
	cc types.ClientConfig,
	dataStore ports.DataStore,
	payload map[string]any) (action Action, statusCode int, newPayload map[string]any, err error) {

	action = NoOp
	statusCode = http.StatusAccepted
	newPayload = payload

	// Rate limits: IP + client
	if cc.IPRPM > 0 {
		ip := clientIP
		ok, acquireErr := dataStore.Acquire(ctx, "IP:"+ip, cc.IPRPM, time.Minute)
		if acquireErr != nil {
			log.WithError(acquireErr).Error("failed to acquire IP rate limit")
			err = fmt.Errorf("rate limit check failed")
			return
		}
		if !ok {
			err = fmt.Errorf("rate limit (ip)")
			return
		}
	}
	if cc.ClientRPM > 0 {
		ok, acquireErr := dataStore.Acquire(ctx, "CLIENT:"+clientID, cc.ClientRPM, time.Minute)
		if acquireErr != nil {
			log.WithError(acquireErr).Error("failed to acquire client rate limit")
			err = fmt.Errorf("rate limit check failed")
			return
		}
		if !ok {
			err = fmt.Errorf("rate limit (client)")
			return
		}
	}

	// If pass through mode matched, just acknowledge
	if CheckPassthrough(cc.Passthrough, payload) {
		action = ForwardedAsIs
		return
	}
	// Edge scope
	// If the trigger field is empty, always forward (no edge/flap/aggregate)
	// coz there is no field to watch.
	if cc.Trigger.FieldExpr == "" {
		action = ForwardedAsIs
		return
	}
	newVal, err := EvalString(cc.Trigger.FieldExpr, payload)
	if err != nil {
		statusCode = http.StatusBadRequest
		err = fmt.Errorf("trigger field eval error")
		return
	}

	if newVal != nil {
		scopeKey := ComputeKey(cc.Trigger.FieldExpr)
		// Edge + flapping; one retry on CAS race
		action, newPayload, err = EvaluateEdgeAndFlap(
			ctx, dataStore, clientID, scopeKey, *newVal, cc.Trigger.Flapping,
			payload,
		)
		if err != nil {
			err = fmt.Errorf("edge evaluation error")
			statusCode = http.StatusInternalServerError
			return
		}
	}

	// Target limit
	if (action == EdgeTriggeredForward || action == AggregateSent) && cc.Trigger.Target.SNSRPM > 0 {
		targetScope := "TARGET:" + clientID + ":" + cc.Trigger.Target.SNSArn
		ok, acquireErr := dataStore.Acquire(ctx, targetScope, cc.Trigger.Target.SNSRPM, time.Minute)
		if acquireErr != nil {
			log.WithError(acquireErr).Error("failed to acquire target rate limit")
			statusCode = http.StatusInternalServerError
			err = fmt.Errorf("rate limit check failed")
			return
		}
		if !ok {
			action = NoOp
			statusCode = http.StatusTooManyRequests
		}
	}
	return
}

// ComputeKey generates a quick hash of the given string with fixed length.
func ComputeKey(s string) string {
	h := fnv.New32a()
	// hash.Hash.Write never returns an error according to the interface contract
	_, _ = h.Write([]byte(s))
	return fmt.Sprintf("e%d", h.Sum32())
}

// LoadCachedClientConfig loads client config from cache or store.
func LoadCachedClientConfig(ctx context.Context, cs ports.ClientStore, id string) (types.ClientConfig, error) {
	if v, ok := cfgCache.Get(id); ok {
		return v, nil
	}
	cc, err := cs.GetClientConfig(ctx, id)
	if err != nil {
		return types.ClientConfig{}, err
	}
	// Caches the client config info for 5 minutes
	cfgCache.Set(id, cc, 300*time.Second)
	return cc, nil
}
