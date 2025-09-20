package tests

import (
	"context"
	"enoti/cmd/enoti/cmds"
	"enoti/internal/flow"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
)

func (s *IntegrationTestSuite) TestAuthCorrect() {
	ctx := context.Background()
	err := cmds.PutConfig(ctx, s.clientStore, "./configs/bare_minimum.yml")
	s.NoError(err)

	cfg, err := s.clientStore.GetClientConfig(ctx, "example-client-id-bare-minimum")
	s.NoError(err)

	resp, err := s.notify(cfg.ClientID, cfg.ClientKey,
		`{}
			`)

	s.assertSuccessStatus(resp, flow.StatusTextMap[flow.ForwardedAsIs], nil)
}

func (s *IntegrationTestSuite) TestAuthInvalidConfig() {
	ctx := context.Background()
	err := cmds.PutConfig(ctx, s.clientStore, "./configs/invalid.yml")
	s.Error(err)

	err = cmds.GetConfig(ctx, s.clientStore, "example-client-id")
	s.Error(err)
}

func (s *IntegrationTestSuite) TestAuthInvalidPayloadClientKey() {
	ctx := context.Background()
	err := cmds.PutConfig(ctx, s.clientStore, "./configs/bare_minimum.yml")
	s.NoError(err)

	cfg, err := s.clientStore.GetClientConfig(ctx, "example-client-id-bare-minimum")
	s.NoError(err)

	resp, err := s.notify(cfg.ClientID, "bad-client-key",
		`{}
			`)

	s.assertFailureStatus(resp, http.StatusUnauthorized, err, aws.String("invalid credentials"))
}
