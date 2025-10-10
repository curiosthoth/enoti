package tests

import (
	"context"
	"encoding/json"
	"enoti/cmd/enoti/cmds"
	"enoti/internal/flow"
	"fmt"
	"time"
)

// TestEdgeTrigger1 tests the edge trigger functionality:
// we only want to forward the first notification in a
// series of identical notifications on the defined edge key.
func (s *IntegrationTestSuite) TestEdgeTrigger1() {
	ctx := context.Background()
	err := cmds.PutConfig(ctx, s.clientStore, "./configs/edge_trigger_simple.yml")
	s.NoError(err)

	cnt := 0
	s.publisher.SetOnPublish(func(ctx context.Context, arn string, payload []byte) error {
		cnt += 1
		return nil
	})
	for i := 0; i < 10; i++ {
		r, err := s.notify(
			"example-client-id-edge-trigger-simple",
			"example-api-key-1234567890",
			map[string]any{
				"message": "Hello, Edge Trigger!",
				"subject": "Edge Trigger Test",
				"event": map[string]any{
					"type": "e1",
				},
			},
		)
		s.NoError(err)
		if i == 0 {
			s.assertSuccessStatus(r, flow.StatusTextMap[flow.EdgeTriggeredForward], nil)
		} else {
			s.assertSuccessStatus(r, flow.StatusTextMap[flow.NoOp], nil)
		}
	}
	s.Equal(1, cnt)
}

// TestEdgeTrigger2 tests the edge trigger functionality:
// we only want to forward the first notification in a
// series of identical notifications on the defined edge key; then
// only forward again when the edge key value changes.
func (s *IntegrationTestSuite) TestEdgeTrigger2() {
	ctx := context.Background()
	err := cmds.PutConfig(ctx, s.clientStore, "./configs/edge_trigger_simple.yml")
	s.NoError(err)

	cnt := 0
	s.publisher.SetOnPublish(func(ctx context.Context, arn string, payload []byte) error {
		cnt += 1

		var str map[string]any
		err := json.Unmarshal(payload, &str)
		s.NoError(err)
		if cnt == 1 {
			s.Equal("e1", str["event"].(map[string]any)["type"])
		}
		if cnt == 2 {
			s.Equal("e2", str["event"].(map[string]any)["type"])
		}
		if cnt > 2 {
			s.Fail("should not be called more than twice")
		}
		return nil
	})
	for i := 0; i < 3; i++ {
		r, err := s.notify(
			"example-client-id-edge-trigger-simple",
			"example-api-key-1234567890",
			map[string]any{
				"message": "Hello, Edge Trigger!",
				"subject": "Edge Trigger Test",
				"event": map[string]any{
					"type": "e1",
				},
			},
		)
		s.NoError(err)
		if i == 0 {
			s.assertSuccessStatus(r, flow.StatusTextMap[flow.EdgeTriggeredForward], nil)
		} else {
			s.assertSuccessStatus(r, flow.StatusTextMap[flow.NoOp], nil)
		}
	}
	s.Equal(1, cnt)
	for i := 0; i < 3; i++ {
		r, err := s.notify(
			"example-client-id-edge-trigger-simple",
			"example-api-key-1234567890",
			map[string]any{
				"message": "Hello, Edge Trigger!",
				"subject": "Edge Trigger Test",
				"event": map[string]any{
					"type": "e2", // changed edge key value
				},
			},
		)
		s.NoError(err)
		if i == 0 {
			s.assertSuccessStatus(r, flow.StatusTextMap[flow.EdgeTriggeredForward], nil)
		} else {
			s.assertSuccessStatus(r, flow.StatusTextMap[flow.NoOp], nil)
		}
	}
	s.Equal(2, cnt)
}

// TestEdgeTrigger3 tests the edge trigger functionality:
// Different event type values should be treated as different edges.
func (s *IntegrationTestSuite) TestEdgeTrigger3() {
	ctx := context.Background()
	err := cmds.PutConfig(ctx, s.clientStore, "./configs/edge_trigger_simple.yml")
	s.NoError(err)

	cnt := 0
	s.publisher.SetOnPublish(func(ctx context.Context, arn string, payload []byte) error {
		cnt += 1

		var str map[string]any
		err := json.Unmarshal(payload, &str)
		s.NoError(err)
		s.Equal(fmt.Sprintf("e%d", cnt), str["event"].(map[string]any)["type"])

		return nil
	})
	for i := 0; i < 4; i++ {
		r, err := s.notify(
			"example-client-id-edge-trigger-simple",
			"example-api-key-1234567890",
			map[string]any{
				"message": "Hello, Edge Trigger!",
				"subject": "Edge Trigger Test",
				"event": map[string]any{
					"type": fmt.Sprintf("e%d", i+1),
				},
			},
		)
		s.NoError(err)
		s.assertSuccessStatus(r, flow.StatusTextMap[flow.EdgeTriggeredForward], nil)
	}
	s.Equal(4, cnt)
}

// TestEdgeTriggerAggregate1 tests the edge trigger functionality:
// Test out sending aggregation instead of single events.
func (s *IntegrationTestSuite) TestEdgeTriggerAggregate1() {
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
		fmt.Println("Published:", string(payload))
		return nil
	})
	// Make 3 flips
	for i := 0; i < 5; i++ {
		r, err := s.notify(
			"example-client-id-edge-trigger-agg",
			"example-api-key-1234567890",
			map[string]any{
				"message": "Hello, Edge Trigger!",
				"subject": fmt.Sprintf("Edge Trigger Test %d", i),
				"event": map[string]any{
					"type": fmt.Sprintf("e%d", i%2),
				},
			},
		)
		s.NoError(err)
		fmt.Printf("%d : ", i)
		if i == 0 {
			// First edge always forwarded
			s.assertSuccessStatus(r, flow.StatusTextMap[flow.EdgeTriggeredForward], nil)
			continue
		} else if i > 0 && i < 3 {
			s.assertSuccessStatus(r, flow.StatusTextMap[flow.SuppressFlapping], nil)
		} else if i == 3 {
			s.assertSuccessStatus(r, flow.StatusTextMap[flow.AggregateSent], nil)
		} else {
			s.assertSuccessStatus(r, flow.StatusTextMap[flow.SuppressFlapping], nil)
		}
	}
	// Hit twice: 1 initial edge, 1 aggregate at the end
	s.Equal(2, cnt)
	fmt.Println()
}

// TestEdgeTriggerAggregate2 tests the edge trigger functionality:
// Test more flips
func (s *IntegrationTestSuite) TestEdgeTriggerAggregate2() {
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
		fmt.Println("Published:", string(payload))
		return nil
	})
	// 5 different type of events
	for i := 0; i < 100; i++ {
		fmt.Printf("%d : ", i)

		r, err := s.notify(
			"example-client-id-edge-trigger-agg",
			"example-api-key-1234567890",
			map[string]any{
				"message": "Hello, Edge Trigger!",
				"subject": fmt.Sprintf("Edge Trigger Test %d", i),
				"event": map[string]any{
					"type": fmt.Sprintf("e%d", i%5),
				},
			},
		)
		s.NoError(err)
		if i == 0 {
			// First edge always forwarded
			s.assertSuccessStatus(r, flow.StatusTextMap[flow.EdgeTriggeredForward], nil)
			continue
		}
	}
	// Hits: 1 initial edge, 33 aggregates
	s.Equal(34, cnt)
}

// TestEdgeTriggerAggregateCrossWindows tests the edge trigger functionality:
// Test across multiple windows.
// We run the test for 25 iterations, with 1 seconds gap.
// 0 - triggers a forward
// 1 -- 5: triggers an aggregate at 5th
// 6 -- 10: triggers an aggregate at 10th
// 11 ->: Cross window, send an edge
// 11 -- 15: triggers an aggregate at 15th
// 16 -- 20: triggers an aggregate at 20th
// 22 ->: Cross window, send an edge;
// 21 -- 25: triggers an aggregate at 25th
// That will make the test go over [ -- 10 -- 10 -- 10 -- ] windows
func (s *IntegrationTestSuite) TestEdgeTriggerAggregateCrossWindows() {
	ctx := context.Background()
	err := cmds.PutConfig(ctx, s.clientStore, "./configs/edge_trigger_agg_short_window.yml")
	s.NoError(err)

	t := time.Now()
	flow.SetTimNowFn(func() time.Time {
		t = t.Add(time.Second)
		return t
	})
	cnt := 0
	maxID := 0
	s.publisher.SetOnPublish(func(ctx context.Context, arn string, payload []byte) error {
		cnt += 1
		fmt.Println("Published:", string(payload))
		// Parse the payload, extracting the `recent` field, for each one's `payload`'s `id`
		var str map[string]any
		err := json.Unmarshal(payload, &str)
		s.NoError(err)
		recent, ok := str["recent"].([]any)
		if !ok {
			return nil
		}
		for _, item := range recent {
			it := item.(map[string]any)
			pl := it["payload"].(map[string]any)
			id := int(pl["id"].(float64))
			if id > maxID {
				maxID = id
			}
		}
		return nil
	})
	// 5 different type of events
	for i := 0; i < 25; i++ {
		et := fmt.Sprintf("e%d", i%5)
		fmt.Printf("%d : %s\t", i, et)
		if i == 5 {
			fmt.Println()
		}
		r, err := s.notify(
			"example-client-id-edge-trigger-agg-short-window",
			"example-api-key-1234567890",
			map[string]any{
				"id":      i,
				"message": "Hello, Edge Trigger!",
				"subject": fmt.Sprintf("Edge Trigger Test %d", i),
				"event": map[string]any{
					"type": et,
				},
			},
		)
		s.NoError(err)
		// Why 11 and 22? Because with 1 second interval, at 11th second, it crosses the 10 seconds window
		// and at 22nd second, it crosses the next 10 seconds window
		// We used > to test the window crossing logic *not >=*
		if i == 0 || i == 11 || i == 22 {
			// First edge always forwarded
			s.assertSuccessStatus(r, flow.StatusTextMap[flow.EdgeTriggeredForward], nil)
			continue
		} else if i%5 == 0 {
			// Other than every 5th, should be suppressed
			s.assertSuccessStatus(r, flow.StatusTextMap[flow.AggregateSent], nil)
			continue
		} else {
			s.assertSuccessStatus(r, flow.StatusTextMap[flow.SuppressFlapping], nil)
		}
	}
	s.Equal(7, cnt)
	s.Equal(20, maxID) // The last 5 events are not sent out
	//
	//// Add a push to flush the last 4
	//r, err := s.notify(
	//	"example-client-id-edge-trigger-agg-short-window",
	//	"example-api-key-1234567890",
	//	map[string]any{
	//		"id":      10000,
	//		"message": "Hello, Edge Trigger!",
	//		"subject": "Edge Trigger Test Last!",
	//		"event": map[string]any{
	//			"type": "last push",
	//		},
	//	},
	//)
	//s.NoError(err)
	//s.assertSuccessStatus(r, flow.StatusTextMap[flow.AggregateSent], nil)
	//s.Equal(8, cnt)
	//s.Equal(10000, maxID) // The last 4 events plus this one are sent out
}

// TestEdgeTriggerAggregateCrossWindows tests the edge trigger functionality:
// Test across multiple windows.
// We run the test for 20 iterations, with 1 seconds gap.
// 0 - triggers a forward
// Then all the rest should be suppressed
// That will make the test go over [ -- 10 -- 10 --] windows
func (s *IntegrationTestSuite) TestEdgeTriggerAggregateCrossWindowsSuppress() {
	ctx := context.Background()
	err := cmds.PutConfig(ctx, s.clientStore, "./configs/edge_trigger_agg_short_window.yml")
	s.NoError(err)
	t := time.Now()
	flow.SetTimNowFn(func() time.Time {
		t = t.Add(time.Second)
		return t
	})
	cnt := 0
	maxID := 0
	s.publisher.SetOnPublish(func(ctx context.Context, arn string, payload []byte) error {
		cnt += 1
		fmt.Println("Published:", string(payload))
		// Parse the payload, extracting the `recent` field, for each one's `payload`'s `id`
		var str map[string]any
		err := json.Unmarshal(payload, &str)
		s.NoError(err)
		recent, ok := str["recent"].([]any)
		if !ok {
			// Not an aggregated message, parse the `id` directly
			id := int(str["id"].(float64))
			if id > maxID {
				maxID = id
			}
			return nil
		}
		for _, item := range recent {
			it := item.(map[string]any)
			pl := it["payload"].(map[string]any)
			id := int(pl["id"].(float64))
			if id > maxID {
				maxID = id
			}
		}
		return nil
	})
	// 5 different type of events
	for i := 0; i < 20; i++ {
		fmt.Printf("%d : ", i)
		r, err := s.notify(
			"example-client-id-edge-trigger-agg-short-window",
			"example-api-key-1234567890",
			map[string]any{
				"id":      i,
				"message": "Hello, Edge Trigger!",
				"subject": fmt.Sprintf("Edge Trigger Test %d", i),
				"event": map[string]any{
					"type": "fixed-type",
				},
			},
		)
		s.NoError(err)
		if i == 0 {
			// First edge always forwarded
			s.assertSuccessStatus(r, flow.StatusTextMap[flow.EdgeTriggeredForward], nil)
			continue
		} else {
			s.assertSuccessStatus(r, flow.StatusTextMap[flow.NoOp], nil)
		}
	}
	// Hits: 1 initial edge only
	s.Equal(1, cnt)
	s.Equal(0, maxID) // The last 5 events are not sent out

	// Add a push to flush
	r, err := s.notify(
		"example-client-id-edge-trigger-agg-short-window",
		"example-api-key-1234567890",
		map[string]any{
			"id":      10000,
			"message": "Hello, Edge Trigger!",
			"subject": "Edge Trigger Test Last!",
			"event": map[string]any{
				"type": "new push",
			},
		},
	)
	s.NoError(err)
	s.assertSuccessStatus(r, flow.StatusTextMap[flow.EdgeTriggeredForward], nil)
	s.Equal(2, cnt)
	s.Equal(10000, maxID) // The last 4 events plus this one are sent out
}
