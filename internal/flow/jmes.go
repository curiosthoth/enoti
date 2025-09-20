package flow

import (
	"encoding/json"
	"fmt"

	"github.com/jmespath/go-jmespath"
)

// EvalAny returns the raw value selected by the JMESPath expression.
// It is safe to pass any decoded JSON (map[string]any, []any, etc.)
// It will return nil and no error if the expression does not match anything.
// That is the same effect as having the expression evaluate to `null`.
func EvalAny(expression string, payload map[string]any) (any, error) {
	v, err := jmespath.Search(expression, payload)
	if err != nil {
		return nil, fmt.Errorf("jmespath: %w", err)
	}
	return v, nil
}

// EvalString coerces the selection to string; primitives are JSON-encoded if needed.
func EvalString(expression string, payload map[string]any) (*string, error) {
	v, err := EvalAny(expression, payload)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, nil
	}
	switch t := v.(type) {
	case string:
		return &t, nil
	default:
		b, _ := json.Marshal(t)
		bs := string(b)
		return &bs, nil
	}
}
