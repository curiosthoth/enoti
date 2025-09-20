package flow

import "enoti/internal/types"

func CheckPassthrough(passthroughCfg types.Passthrough, payload map[string]any) bool {
	if passthroughCfg.FieldExpr == "" {
		return false
	}
	match, err := EvalAny(passthroughCfg.FieldExpr, payload)
	if err != nil {
		return false
	}
	// Match should be a boolean
	matched, ok := match.(bool)
	if !ok {
		return false
	}
	if passthroughCfg.Negate {
		return !matched
	} else {
		return matched
	}
}
