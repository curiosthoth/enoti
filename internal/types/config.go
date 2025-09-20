package types

import "fmt"

// ClientConfig is stored per client in DynamoDB and cached in-process.
// It drives the behavior of the ingestion service for a client.
// The (ClientID, ClientKey) pair is used for authentication, if a client failed to submit the correct values in
// `X-Client-ID` and `X-API-Key` headers, the request is rejected with 401 Unauthorized.
// ClientName is for display purposes only.
// Passthrough allows filtering of events before any other processing.
// IPRPM is the max rate per minute allowed per source IP address. 0 means no limit.
// ClientRPM is the max rate per minute allowed per client. 0 means no limit.
// Dedup drives deduplication behavior.
// Trigger drives edge detection and forwarding behavior.
type ClientConfig struct {
	ClientID    string        `json:"client_id" dynamodbav:"client_id"`
	ClientName  string        `json:"client_name" dynamodbav:"client_name"`
	ClientKey   string        `json:"client_key" dynamodbav:"client_key"`
	IPRPM       int           `json:"ip_rpm" dynamodbav:"ip_rpm"`
	ClientRPM   int           `json:"client_rpm" dynamodbav:"client_rpm"`
	Passthrough Passthrough   `json:"passthrough" dynamodbav:"passthrough"`
	Trigger     TriggerConfig `json:"trigger" dynamodbav:"trigger"`
}

const (
	ClientIDMinLength  = 4
	ClientKeyMinLength = 8

	ClientIDHdrName  = "x-client-id"
	ClientKeyHdrName = "x-client-key"

	MinWindowSizeSeconds = 10 // 10 seconds
)

// Passthrough allows filtering of events before any other processing but after IP/Client rate limits.
// Anything matching the Passthrough rule is forwarded as-is to the target without applying dedup or trigger logic.
// When negate is true, the rule is inverted (i.e. events NOT matching the expression are passed through).
// To check the key existence at root level, use "contains(keys(@), '<key-name>')"; to check for existence in a map, use
// "contains(<map-field>, '<key-name>')".
type Passthrough struct {
	FieldExpr string `json:"field" dynamodbav:"field"` // JMESPath expression that yields boolean
	Negate    bool   `json:"negate" dynamodbav:"not_match"`
}

// TriggerConfig drives edge detection and forwarding behavior.
type TriggerConfig struct {
	// FieldExpr selects the value used for edge detection (string-coerced).
	FieldExpr string `json:"field" dynamodbav:"field"`
	// ScopeFields narrows edge tracking to a logical entity (default = Dedup.Fields).
	ScopeFields []string     `json:"scope_fields,omitempty" dynamodbav:"scope_fields"`
	Target      TargetConfig `json:"target" dynamodbav:"target"`
	Flapping    *FlapConfig  `json:"flapping,omitempty" dynamodbav:"flapping"`
}

type TargetConfig struct {
	SNSArn string `json:"sns_arn" dynamodbav:"sns_arn"`
	SNSRPM int    `json:"sns_rpm" dynamodbav:"rate_per_minute"`
}

// FlapConfig tolerates early flips and aggregates noisy patterns.
type FlapConfig struct {
	// WindowSeconds is the time window in seconds to count flips (edges)
	// for accumulating "flips". When the window expires, the count resets and if there are
	// flips, we trigger an edge notification.
	WindowSeconds int `json:"window_seconds" dynamodbav:"window_seconds"` // sliding window for counting flips

	// SuppressBelow is the initial seconds *within* the time window, within which, not to trigger Edge notification
	// E.g.,within [0, SuppressBelow) flips, no forwards, just ignore. 0 means no suppression, as long as there is a flip of value, do send.
	SuppressBelow int `json:"suppress_below" dynamodbav:"suppress_below"`

	// AggregateAt is the threshold: if flips >= this, send an aggregated message instead of forwarding originals; 0 means no aggregation
	// Note that, if SuppressBelow is 0, the first edge will always be forwarded, and aggregation starts from the 2nd edge.
	AggregateAt int `json:"aggregate_at" dynamodbav:"aggregate_at"`

	// AggregateMaxItems is the max number of recent flips to include in the aggregate message; 0 means all
	AggregateMaxItems int `json:"aggregate_max_items" dynamodbav:"aggregate_max_items"`

	// AggregateCooldownSeconds is the minimal seconds between aggregated sends; 0 means no cooldown
	AggregateCooldownSeconds int `json:"aggregate_cooldown_seconds" dynamodbav:"aggregate_cooldown_seconds"`
}

func (c ClientConfig) Validate() error {
	if c.ClientID == "" {
		return fmt.Errorf("client_id is required")
	}
	if c.ClientName == "" {
		return fmt.Errorf("client_name is required")
	}
	if c.ClientKey == "" {
		return fmt.Errorf("client_key is required")
	}
	if len(c.ClientKey) < ClientKeyMinLength {
		return fmt.Errorf("api_key must be at least %d characters", ClientKeyMinLength)
	}
	if c.IPRPM < 0 {
		return fmt.Errorf("ip_rpm must be non-negative. 0 for non limit")
	}
	if c.ClientRPM < 0 {
		return fmt.Errorf("client_rpm must be non-negative. 0 for non limit")
	}
	flapping := c.Trigger.Flapping
	if flapping != nil {
		if flapping.WindowSeconds < MinWindowSizeSeconds {
			return fmt.Errorf("flapping.window_seconds must be greater than or equal to %d seconds", MinWindowSizeSeconds)
		}
		if flapping.SuppressBelow < 0 || flapping.SuppressBelow > flapping.WindowSeconds {
			return fmt.Errorf("flapping.suppress_below must be non-negative and less than or equal to window_seconds")
		}
	}
	return nil
}
