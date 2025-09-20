package flow

import "time"

const (
	NoOp Action = iota // NoOp means do nothing. The request is good and accepted but it won't be forwarded due to the logic.
	SuppressFlapping
	SuppressDedup
	EdgeTriggeredForward
	ForwardedAsIs // No Edge trigger logic applied. Just forward as is.
	AggregateSent // Send aggregated notification, this is different from EdgeTriggeredForward.
)

var StatusTextMap = map[Action]string{
	NoOp:                 "no_op",
	SuppressFlapping:     "suppress_flap",
	SuppressDedup:        "suppress_dedup",
	EdgeTriggeredForward: "edge_triggered_forward",
	ForwardedAsIs:        "forwarded_as_is",
	AggregateSent:        "aggregate_sent",
}

var timeNow = time.Now

func EpochTime() int64 {
	return timeNow().Unix()
}

func SetTimNowFn(f func() time.Time) {
	timeNow = f
}

func RestoreTimeNow() {
	timeNow = time.Now
}
