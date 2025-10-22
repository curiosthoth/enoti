package tests

import (
	"context"
	"encoding/json"
	"enoti/cmd/enoti/cmds"
	"enoti/internal/flow"
	"fmt"
	"time"
)

// TestEdgeTriggerSuppressBelow tests the suppress_below configuration.
// First 3 flips should be suppressed, 4th flip should forward.
func (s *IntegrationTestSuite) TestEdgeTriggerSuppressBelow() {
	ctx := context.Background()
	err := cmds.PutConfig(ctx, s.clientStore, "./configs/edge_trigger_suppress.yml")
	s.NoError(err)

	cnt := 0
	s.publisher.SetOnPublish(func(ctx context.Context, arn string, payload []byte) error {
		cnt += 1
		return nil
	})

	// First event - always triggers an edge
	r, err := s.notify(
		"example-client-id-edge-trigger-suppress",
		"example-api-key-1234567890",
		map[string]any{
			"message": "Test message",
			"event": map[string]any{
				"type": "e1",
			},
		},
	)
	s.NoError(err)
	s.assertSuccessStatus(r, flow.StatusTextMap[flow.EdgeTriggeredForward], nil)
	s.Equal(1, cnt)

	// Next 3 flips should be suppressed (flip count 1, 2, 3 are <= suppress_below=3)
	for i := 0; i < 3; i++ {
		r, err := s.notify(
			"example-client-id-edge-trigger-suppress",
			"example-api-key-1234567890",
			map[string]any{
				"message": "Test message",
				"event": map[string]any{
					"type": fmt.Sprintf("e%d", (i%2)+2), // Toggle between e2 and e3
				},
			},
		)
		s.NoError(err)
		s.assertSuccessStatus(r, flow.StatusTextMap[flow.SuppressFlapping], nil)
		s.Equal(1, cnt) // Still only 1 publish
	}

	// 4th flip (flip count = 4 > suppress_below=3) should forward
	r, err = s.notify(
		"example-client-id-edge-trigger-suppress",
		"example-api-key-1234567890",
		map[string]any{
			"message": "Test message",
			"event": map[string]any{
				"type": "e4",
			},
		},
	)
	s.NoError(err)
	s.assertSuccessStatus(r, flow.StatusTextMap[flow.EdgeTriggeredForward], nil)
	s.Equal(2, cnt)
}

// TestEdgeTriggerAggregateCooldown tests the aggregate_cooldown_seconds configuration.
// After an aggregate is sent, subsequent aggregates should be suppressed during cooldown.
func (s *IntegrationTestSuite) TestEdgeTriggerAggregateCooldown() {
	ctx := context.Background()
	err := cmds.PutConfig(ctx, s.clientStore, "./configs/edge_trigger_cooldown.yml")
	s.NoError(err)

	t := time.Now()
	flow.SetTimNowFn(func() time.Time {
		return t
	})

	cnt := 0
	s.publisher.SetOnPublish(func(ctx context.Context, arn string, payload []byte) error {
		cnt += 1
		return nil
	})

	// First event triggers edge
	r, err := s.notify(
		"example-client-id-edge-trigger-cooldown",
		"example-api-key-1234567890",
		map[string]any{
			"id": 0,
			"event": map[string]any{
				"type": "e0",
			},
		},
	)
	s.NoError(err)
	s.assertSuccessStatus(r, flow.StatusTextMap[flow.EdgeTriggeredForward], nil)
	s.Equal(1, cnt)

	// Send 3 flips to trigger aggregate
	for i := 1; i <= 3; i++ {
		t = t.Add(time.Second)
		r, err := s.notify(
			"example-client-id-edge-trigger-cooldown",
			"example-api-key-1234567890",
			map[string]any{
				"id": i,
				"event": map[string]any{
					"type": fmt.Sprintf("e%d", i),
				},
			},
		)
		s.NoError(err)
		if i < 3 {
			s.assertSuccessStatus(r, flow.StatusTextMap[flow.SuppressFlapping], nil)
		} else {
			// 3rd flip triggers aggregate
			s.assertSuccessStatus(r, flow.StatusTextMap[flow.AggregateSent], nil)
		}
	}
	s.Equal(2, cnt) // 1 initial edge + 1 aggregate

	// Send 3 more flips within cooldown period (5 seconds)
	for i := 4; i <= 6; i++ {
		t = t.Add(time.Second) // Only 1 second increments, still in cooldown
		r, err := s.notify(
			"example-client-id-edge-trigger-cooldown",
			"example-api-key-1234567890",
			map[string]any{
				"id": i,
				"event": map[string]any{
					"type": fmt.Sprintf("e%d", i),
				},
			},
		)
		s.NoError(err)
		// Should be suppressed due to cooldown
		s.assertSuccessStatus(r, flow.StatusTextMap[flow.SuppressFlapping], nil)
		s.Equal(2, cnt) // Still only 2 publishes
	}

	// Advance time beyond cooldown (total 6 seconds from aggregate, need 5 seconds cooldown)
	t = t.Add(2 * time.Second)
	// Send 3 more flips after cooldown expires
	for i := 7; i <= 9; i++ {
		t = t.Add(time.Second)
		r, err := s.notify(
			"example-client-id-edge-trigger-cooldown",
			"example-api-key-1234567890",
			map[string]any{
				"id": i,
				"event": map[string]any{
					"type": fmt.Sprintf("e%d", i),
				},
			},
		)
		s.NoError(err)
		if i < 9 {
			s.assertSuccessStatus(r, flow.StatusTextMap[flow.SuppressFlapping], nil)
		} else {
			// Should trigger aggregate again after cooldown
			s.assertSuccessStatus(r, flow.StatusTextMap[flow.AggregateSent], nil)
		}
	}
	s.Equal(3, cnt) // 1 initial + 1 aggregate + 1 aggregate after cooldown
}

// TestEdgeTriggerAggregateMaxItems tests that aggregates respect the max_items limit.
func (s *IntegrationTestSuite) TestEdgeTriggerAggregateMaxItems() {
	ctx := context.Background()
	err := cmds.PutConfig(ctx, s.clientStore, "./configs/edge_trigger_max_items.yml")
	s.NoError(err)

	t := time.Now()
	flow.SetTimNowFn(func() time.Time {
		t = t.Add(time.Second)
		return t
	})

	cnt := 0
	maxItemsReceived := 0
	s.publisher.SetOnPublish(func(ctx context.Context, arn string, payload []byte) error {
		cnt += 1
		var str map[string]any
		err := json.Unmarshal(payload, &str)
		s.NoError(err)

		// Check if this is an aggregate message
		if recent, ok := str["recent"].([]any); ok {
			itemCount := len(recent)
			if itemCount > maxItemsReceived {
				maxItemsReceived = itemCount
			}
			// Should never exceed aggregate_max_items=3
			s.LessOrEqual(itemCount, 3, "Aggregate should respect max_items limit")
		}
		return nil
	})

	// First event triggers edge
	r, err := s.notify(
		"example-client-id-edge-trigger-max-items",
		"example-api-key-1234567890",
		map[string]any{
			"id": 0,
			"event": map[string]any{
				"type": "e0",
			},
		},
	)
	s.NoError(err)
	s.assertSuccessStatus(r, flow.StatusTextMap[flow.EdgeTriggeredForward], nil)

	// Send 10 flips - should trigger 2 aggregates at 5th and 10th
	for i := 1; i <= 10; i++ {
		r, err := s.notify(
			"example-client-id-edge-trigger-max-items",
			"example-api-key-1234567890",
			map[string]any{
				"id": i,
				"event": map[string]any{
					"type": fmt.Sprintf("e%d", i),
				},
			},
		)
		s.NoError(err)
		if i == 5 || i == 10 {
			s.assertSuccessStatus(r, flow.StatusTextMap[flow.AggregateSent], nil)
		} else {
			s.assertSuccessStatus(r, flow.StatusTextMap[flow.SuppressFlapping], nil)
		}
	}

	s.Equal(3, cnt) // 1 initial edge + 2 aggregates
	s.Equal(3, maxItemsReceived, "Max items in aggregate should be 3")
}

// TestEdgeTriggerNestedField tests edge detection on nested field paths.
func (s *IntegrationTestSuite) TestEdgeTriggerNestedField() {
	ctx := context.Background()
	err := cmds.PutConfig(ctx, s.clientStore, "./configs/edge_trigger_nested_field.yml")
	s.NoError(err)

	cnt := 0
	s.publisher.SetOnPublish(func(ctx context.Context, arn string, payload []byte) error {
		cnt += 1
		return nil
	})

	// First event with nested field
	r, err := s.notify(
		"example-client-id-edge-trigger-nested",
		"example-api-key-1234567890",
		map[string]any{
			"message": "Test message",
			"event": map[string]any{
				"metadata": map[string]any{
					"status": "active",
				},
			},
		},
	)
	s.NoError(err)
	s.assertSuccessStatus(r, flow.StatusTextMap[flow.EdgeTriggeredForward], nil)
	s.Equal(1, cnt)

	// Same nested value - should not trigger
	r, err = s.notify(
		"example-client-id-edge-trigger-nested",
		"example-api-key-1234567890",
		map[string]any{
			"message": "Test message",
			"event": map[string]any{
				"metadata": map[string]any{
					"status": "active",
				},
			},
		},
	)
	s.NoError(err)
	s.assertSuccessStatus(r, flow.StatusTextMap[flow.NoOp], nil)
	s.Equal(1, cnt)

	// Different nested value - should trigger
	r, err = s.notify(
		"example-client-id-edge-trigger-nested",
		"example-api-key-1234567890",
		map[string]any{
			"message": "Test message",
			"event": map[string]any{
				"metadata": map[string]any{
					"status": "inactive",
				},
			},
		},
	)
	s.NoError(err)
	s.assertSuccessStatus(r, flow.StatusTextMap[flow.EdgeTriggeredForward], nil)
	s.Equal(2, cnt)
}

// TestEdgeTriggerRapidFlips tests behavior with many rapid value changes.
func (s *IntegrationTestSuite) TestEdgeTriggerRapidFlips() {
	ctx := context.Background()
	err := cmds.PutConfig(ctx, s.clientStore, "./configs/edge_trigger_agg.yml")
	s.NoError(err)

	t := time.Now()
	flow.SetTimNowFn(func() time.Time {
		t = t.Add(time.Millisecond * 100) // Fast flips
		return t
	})

	cnt := 0
	s.publisher.SetOnPublish(func(ctx context.Context, arn string, payload []byte) error {
		cnt += 1
		return nil
	})

	// First event
	r, err := s.notify(
		"example-client-id-edge-trigger-agg",
		"example-api-key-1234567890",
		map[string]any{
			"event": map[string]any{
				"type": "e0",
			},
		},
	)
	s.NoError(err)
	s.assertSuccessStatus(r, flow.StatusTextMap[flow.EdgeTriggeredForward], nil)

	// Send 20 rapid flips between two values
	for i := 1; i <= 20; i++ {
		r, err := s.notify(
			"example-client-id-edge-trigger-agg",
			"example-api-key-1234567890",
			map[string]any{
				"id": i,
				"event": map[string]any{
					"type": fmt.Sprintf("e%d", i%2), // Alternate between e0 and e1
				},
			},
		)
		s.NoError(err)
		// Should see aggregates at flip count 3, 6, 9, 12, 15, 18
		if i%3 == 0 {
			s.assertSuccessStatus(r, flow.StatusTextMap[flow.AggregateSent], nil)
		} else {
			s.assertSuccessStatus(r, flow.StatusTextMap[flow.SuppressFlapping], nil)
		}
	}

	// 1 initial edge + 6 aggregates (at positions 3, 6, 9, 12, 15, 18)
	s.Equal(7, cnt)
}

// TestEdgeTriggerStableAfterFlapping tests that after flapping stops,
// the system remains stable and doesn't send duplicate notifications.
func (s *IntegrationTestSuite) TestEdgeTriggerStableAfterFlapping() {
	ctx := context.Background()
	err := cmds.PutConfig(ctx, s.clientStore, "./configs/edge_trigger_agg.yml")
	s.NoError(err)

	t := time.Now()
	flow.SetTimNowFn(func() time.Time {
		t = t.Add(time.Second)
		return t
	})

	cnt := 0
	s.publisher.SetOnPublish(func(ctx context.Context, arn string, payload []byte) error {
		cnt += 1
		return nil
	})

	// Initial edge
	r, err := s.notify(
		"example-client-id-edge-trigger-agg",
		"example-api-key-1234567890",
		map[string]any{
			"event": map[string]any{
				"type": "e0",
			},
		},
	)
	s.NoError(err)
	s.assertSuccessStatus(r, flow.StatusTextMap[flow.EdgeTriggeredForward], nil)
	s.Equal(1, cnt)

	// Create flapping (3 flips to trigger aggregate)
	for i := 1; i <= 3; i++ {
		r, err := s.notify(
			"example-client-id-edge-trigger-agg",
			"example-api-key-1234567890",
			map[string]any{
				"event": map[string]any{
					"type": fmt.Sprintf("e%d", i),
				},
			},
		)
		s.NoError(err)
		if i == 3 {
			s.assertSuccessStatus(r, flow.StatusTextMap[flow.AggregateSent], nil)
		}
	}
	s.Equal(2, cnt) // 1 edge + 1 aggregate

	// Now send same value multiple times (stable)
	for i := 0; i < 5; i++ {
		r, err := s.notify(
			"example-client-id-edge-trigger-agg",
			"example-api-key-1234567890",
			map[string]any{
				"event": map[string]any{
					"type": "e3", // Same value
				},
			},
		)
		s.NoError(err)
		s.assertSuccessStatus(r, flow.StatusTextMap[flow.NoOp], nil)
		s.Equal(2, cnt) // Should not increase
	}
}

// TestEdgeTriggerAlternatingPattern tests a specific pattern of value changes.
func (s *IntegrationTestSuite) TestEdgeTriggerAlternatingPattern() {
	ctx := context.Background()
	err := cmds.PutConfig(ctx, s.clientStore, "./configs/edge_trigger_simple.yml")
	s.NoError(err)

	cnt := 0
	values := []string{}
	s.publisher.SetOnPublish(func(ctx context.Context, arn string, payload []byte) error {
		cnt += 1
		var str map[string]any
		err := json.Unmarshal(payload, &str)
		s.NoError(err)
		values = append(values, str["event"].(map[string]any)["type"].(string))
		return nil
	})

	// Pattern: A -> B -> A -> B -> C
	pattern := []string{"A", "B", "A", "B", "C"}
	for i, val := range pattern {
		r, err := s.notify(
			"example-client-id-edge-trigger-simple",
			"example-api-key-1234567890",
			map[string]any{
				"event": map[string]any{
					"type": val,
				},
			},
		)
		s.NoError(err)
		// Every change should trigger (no aggregation in simple config)
		s.assertSuccessStatus(r, flow.StatusTextMap[flow.EdgeTriggeredForward], nil)
		s.Equal(i+1, cnt)
	}

	// Verify all values were published
	s.Equal(pattern, values)
}
