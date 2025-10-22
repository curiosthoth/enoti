package tests

import (
	"context"
	"enoti/cmd/enoti/cmds"
	"enoti/internal/flow"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// TestRateLimitIP tests IP-based rate limiting.
// The config allows 5 requests per minute per IP.
func (s *IntegrationTestSuite) TestRateLimitIP() {
	ctx := context.Background()
	err := cmds.PutConfig(ctx, s.clientStore, "./configs/rate_limit_ip.yml")
	s.NoError(err)

	// First 5 requests should succeed
	for i := 0; i < 5; i++ {
		r, err := s.notify(
			"example-client-id-rate-limit-ip",
			"example-api-key-1234567890",
			map[string]any{
				"message": "Test message",
			},
		)
		s.NoError(err)
		s.assertSuccessStatus(r, flow.StatusTextMap[flow.ForwardedAsIs], nil)
	}

	// 6th request should fail with rate limit error
	r, err := s.notify(
		"example-client-id-rate-limit-ip",
		"example-api-key-1234567890",
		map[string]any{
			"message": "Test message",
		},
	)
	s.assertFailureStatus(r, http.StatusAccepted, err, aws.String("rate limit (ip)"))
}

// TestRateLimitClient tests client-based rate limiting.
// The config allows 3 requests per minute per client.
func (s *IntegrationTestSuite) TestRateLimitClient() {
	ctx := context.Background()
	err := cmds.PutConfig(ctx, s.clientStore, "./configs/rate_limit_client.yml")
	s.NoError(err)

	// First 3 requests should succeed
	for i := 0; i < 3; i++ {
		r, err := s.notify(
			"example-client-id-rate-limit-client",
			"example-api-key-1234567890",
			map[string]any{
				"message": "Test message",
			},
		)
		s.NoError(err)
		s.assertSuccessStatus(r, flow.StatusTextMap[flow.ForwardedAsIs], nil)
	}

	// 4th request should fail with rate limit error
	r, err := s.notify(
		"example-client-id-rate-limit-client",
		"example-api-key-1234567890",
		map[string]any{
			"message": "Test message",
		},
	)
	s.assertFailureStatus(r, http.StatusAccepted, err, aws.String("rate limit (client)"))
}

// TestRateLimitSNS tests SNS target rate limiting.
// The config allows 2 SNS publishes per minute.
func (s *IntegrationTestSuite) TestRateLimitSNS() {
	ctx := context.Background()
	err := cmds.PutConfig(ctx, s.clientStore, "./configs/rate_limit_sns.yml")
	s.NoError(err)

	cnt := 0
	s.publisher.SetOnPublish(func(ctx context.Context, arn string, payload []byte) error {
		cnt += 1
		return nil
	})

	// First 2 edge triggers should result in SNS publishes
	for i := 0; i < 2; i++ {
		r, err := s.notify(
			"example-client-id-rate-limit-sns",
			"example-api-key-1234567890",
			map[string]any{
				"message": "Test message",
				"event": map[string]any{
					"type": i, // Different values to trigger edges
				},
			},
		)
		s.NoError(err)
		s.assertSuccessStatus(r, flow.StatusTextMap[flow.EdgeTriggeredForward], nil)
	}
	s.Equal(2, cnt)

	// 3rd edge trigger should be rate limited
	r, err := s.notify(
		"example-client-id-rate-limit-sns",
		"example-api-key-1234567890",
		map[string]any{
			"message": "Test message",
			"event": map[string]any{
				"type": 2, // Different value to trigger edge
			},
		},
	)
	s.NoError(err)
	s.Equal(http.StatusTooManyRequests, r.StatusCode)
	s.Equal(2, cnt) // Still only 2 publishes
}

// TestRateLimitCombined tests all three rate limits together.
// The most restrictive limit should apply.
func (s *IntegrationTestSuite) TestRateLimitCombined() {
	ctx := context.Background()
	err := cmds.PutConfig(ctx, s.clientStore, "./configs/rate_limit_combined.yml")
	s.NoError(err)

	cnt := 0
	s.publisher.SetOnPublish(func(ctx context.Context, arn string, payload []byte) error {
		cnt += 1
		return nil
	})

	// Send 5 edge-triggering requests (client_rpm limit is 5)
	for i := 0; i < 5; i++ {
		r, err := s.notify(
			"example-client-id-rate-limit-combined",
			"example-api-key-1234567890",
			map[string]any{
				"message": "Test message",
				"event": map[string]any{
					"type": i, // Different values to trigger edges
				},
			},
		)
		s.NoError(err)
		if i < 3 {
			// First 3 should succeed and publish (sns_rpm limit is 3)
			s.assertSuccessStatus(r, flow.StatusTextMap[flow.EdgeTriggeredForward], nil)
		} else {
			// 4th and 5th should be accepted but not published due to SNS rate limit
			s.Equal(http.StatusTooManyRequests, r.StatusCode)
		}
	}
	s.Equal(3, cnt) // Only 3 SNS publishes due to sns_rpm limit

	// 6th request should fail client rate limit
	r, err := s.notify(
		"example-client-id-rate-limit-combined",
		"example-api-key-1234567890",
		map[string]any{
			"message": "Test message",
			"event": map[string]any{
				"type": 5,
			},
		},
	)
	s.assertFailureStatus(r, http.StatusAccepted, err, aws.String("rate limit (client)"))
	s.Equal(3, cnt) // Still only 3 publishes
}

// TestRateLimitIPMultipleClients tests that IP rate limiting is
// independent of client ID.
func (s *IntegrationTestSuite) TestRateLimitIPIndependent() {
	ctx := context.Background()
	// Load both IP rate limited config and another config
	err := cmds.PutConfig(ctx, s.clientStore, "./configs/rate_limit_ip.yml")
	s.NoError(err)
	err = cmds.PutConfig(ctx, s.clientStore, "./configs/bare_minimum.yml")
	s.NoError(err)

	// Use up the IP rate limit with first client
	for i := 0; i < 5; i++ {
		r, err := s.notify(
			"example-client-id-rate-limit-ip",
			"example-api-key-1234567890",
			map[string]any{
				"message": "Test message",
			},
		)
		s.NoError(err)
		s.assertSuccessStatus(r, flow.StatusTextMap[flow.ForwardedAsIs], nil)
	}

	// Second client should also be rate limited by IP
	r, err := s.notify(
		"example-client-id-bare-minimum",
		"example-api-key-1234567890",
		map[string]any{
			"message": "Test message",
		},
	)
	// This should succeed since bare_minimum has no IP rate limiting
	s.NoError(err)
	s.assertSuccessStatus(r, flow.StatusTextMap[flow.ForwardedAsIs], nil)
}

// TestRateLimitClientIndependent tests that client rate limiting is
// independent across different clients.
func (s *IntegrationTestSuite) TestRateLimitClientIndependent() {
	ctx := context.Background()
	err := cmds.PutConfig(ctx, s.clientStore, "./configs/rate_limit_client.yml")
	s.NoError(err)
	err = cmds.PutConfig(ctx, s.clientStore, "./configs/bare_minimum.yml")
	s.NoError(err)

	// Use up client rate limit for first client
	for i := 0; i < 3; i++ {
		r, err := s.notify(
			"example-client-id-rate-limit-client",
			"example-api-key-1234567890",
			map[string]any{
				"message": "Test message",
			},
		)
		s.NoError(err)
		s.assertSuccessStatus(r, flow.StatusTextMap[flow.ForwardedAsIs], nil)
	}

	// 4th request should fail
	r, err := s.notify(
		"example-client-id-rate-limit-client",
		"example-api-key-1234567890",
		map[string]any{
			"message": "Test message",
		},
	)
	s.assertFailureStatus(r, http.StatusAccepted, err, aws.String("rate limit (client)"))

	// Second client should not be affected
	r, err = s.notify(
		"example-client-id-bare-minimum",
		"example-api-key-1234567890",
		map[string]any{
			"message": "Test message",
		},
	)
	s.NoError(err)
	s.assertSuccessStatus(r, flow.StatusTextMap[flow.ForwardedAsIs], nil)
}
