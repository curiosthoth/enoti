package types

const HardLimitRecentItems = 128

// Edge is the persisted edge/flap state for a (clientID, scopeKey).
type Edge struct {
	ScopeKey     string `dynamodbav:"scope_key" json:"scope_key"`
	LastValue    string `dynamodbav:"last_value" json:"last_value"`
	LastChangeTS int64  `dynamodbav:"last_change_ts" json:"last_change_ts"`
	WindowStart  int64  `dynamodbav:"window_start" json:"window_start"`
	FlipCount    int    `dynamodbav:"flip_count" json:"flip_count"`
	// Recent is a list of recent flips, most recent last. It is capped in size (HardLimitRecentItems). The only use
	// if for buiding the aggregate payload.
	Recent []Flip `dynamodbav:"recent" json:"recent"`
	// AggUntilTS is the timestamp until which no new aggregate can be sent (cooldown).
	AggUntilTS int64 `dynamodbav:"agg_until_ts" json:"agg_until_ts"`
	// Version is maintained by the store; do not set in callers.
	Version int64 `dynamodbav:"ver" json:"-"`
}

type Flip struct {
	At      int64  `dynamodbav:"at" json:"at"`
	From    string `dynamodbav:"from" json:"from"`
	To      string `dynamodbav:"to" json:"to"`
	Payload string `dynamodbav:"payload" json:"payload,omitempty"`
}

// AppendRecent appends a Flip to the recent flips slice, maintaining a maximum capacity, keeping the most recent entries.
// And returns the updated slice.
func AppendRecent(rs []Flip, f Flip, cap int) []Flip {
	rs = append(rs, f)
	if len(rs) > cap {
		rs = rs[len(rs)-cap:]
	}
	return rs
}
