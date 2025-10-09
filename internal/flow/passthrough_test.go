package flow

import "enoti/internal/types"

// TestCheckPassthrough1 tests the CheckPassthrough function with a simple expression
func (s *UnitTestSuite) TestCheckPassthrough1() {
	passthroughCfg := types.Passthrough{
		FieldExpr: "contains(keys(@), 'data')",
		Negate:    false,
	}
	v := CheckPassthrough(
		passthroughCfg,
		map[string]any{"data": map[string]any{"value": 15}},
	)
	s.True(v)
	v = CheckPassthrough(
		passthroughCfg,
		map[string]any{"DATA": map[string]any{"value": 15}},
	)
	s.False(v)

	passthroughCfg.FieldExpr = "data.value == 'abc'"
	v = CheckPassthrough(
		passthroughCfg,
		map[string]any{"data": map[string]any{"value": "abc"}},
	)
	s.True(v)

	passthroughCfg.FieldExpr = "data.value != 'abc'"
	v = CheckPassthrough(
		passthroughCfg,
		map[string]any{"data": map[string]any{"value": "abcd"}},
	)
	s.True(v)

	passthroughCfg.FieldExpr = "data.value == 'abc'"
	passthroughCfg.Negate = true
	v = CheckPassthrough(
		passthroughCfg,
		map[string]any{"data": map[string]any{"value": "abdc"}},
	)
	s.True(v)
}
