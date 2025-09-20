package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"enoti/cmd/enoti/cmds"
	"enoti/internal/api"
	"enoti/internal/backends/ddb"
	"enoti/internal/flow"
	"enoti/internal/ports"
	"enoti/internal/types"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/stretchr/testify/suite"
)

const (
	AWSMockPort    = 4566
	TestServerPort = 39080
	TestTableName  = "notify_guard_test-clients"
)

var wg sync.WaitGroup

type IntegrationTestSuite struct {
	suite.Suite

	publisher *TestPublish

	clientStore      ports.ConfigStore
	edgeStore        ports.EdgeStore
	rateLimiterStore ports.RateLimiter
	stopChan         chan<- struct{} // Send only
	doneChan         <-chan error    // Receive only
}

type TestPublish struct {
	callback func(ctx context.Context, arn string, payload []byte) error
}

func (s *TestPublish) PublishRaw(ctx context.Context, arn string, payload []byte) error {
	return s.callback(ctx, arn, payload)
}

func (s *TestPublish) SetOnPublish(fn func(ctx context.Context, arn string, payload []byte) error) {
	s.callback = fn
}

func (s *IntegrationTestSuite) SetupSuite() {
	// Start the server in a Goroutine
	// Makes sure the aws mock is running at port AWSMockPort
	ctx := context.Background()
	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		s.FailNow("Failed to load AWS config", err)
	}
	ddbClient := dynamodb.NewFromConfig(awsCfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(fmt.Sprintf("http://localhost:%d", AWSMockPort))
		if o.Region == "" {
			o.Region = "us-east-1"
		}
		credProvider := credentials.NewStaticCredentialsProvider("test", "test", "")
		o.Credentials = credProvider
	})

	clientStore := ddb.NewClientStore(TestTableName, ddbClient)
	s.rateLimiterStore = ddb.NewDataStore(TestTableName, ddbClient)
	s.edgeStore = ddb.NewDataStore(TestTableName, ddbClient)
	s.publisher = &TestPublish{}
	s.publisher.SetOnPublish(func(ctx context.Context, arn string, payload []byte) error {
		fmt.Printf("PublishRaw called: [%s] %s\n", arn, string(payload))
		return nil
	})
	s.NoError(err)
	s.clientStore = clientStore
	// Start go routine with the api.RunServer()
	s.stopChan, s.doneChan = api.RunServerInterruptible(
		TestServerPort,
		clientStore, s.edgeStore, s.rateLimiterStore, s.publisher,
	)
}

func (s *IntegrationTestSuite) TearDownSuite() {
	// Stop the server
	s.stopChan <- struct{}{}
	err := <-s.doneChan
	if err != nil {
		fmt.Println(err)
	}
}

func (s *IntegrationTestSuite) SetupTest() {
	// Runs before each test in the suite
	flow.RestoreTimeNow()
	err := s.clientStore.ClearAll(context.Background())
	s.NoError(err)
}

func (s *IntegrationTestSuite) TestHealthCheck() {
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/health", TestServerPort))
	s.NoError(err)
	s.Equal(200, resp.StatusCode)
}

func (s *IntegrationTestSuite) TestLoadConfig() {
	ctx := context.Background()
	err := cmds.PutConfig(ctx, s.clientStore, "./configs/bare_minimum.yml")
	s.NoError(err)
}

func (s *IntegrationTestSuite) TestLoadConfigInvalid() {
	ctx := context.Background()
	err := cmds.PutConfig(ctx, s.clientStore, "./configs/invalid.yml")
	s.Error(err)
}

// notify sends a test notification to the test server with the given payload.
// client ID and client Key
func (s *IntegrationTestSuite) notify(clientID, clientKey string, payload any) (*http.Response, error) {
	// Http request
	var body []byte
	var err error
	switch payload.(type) {
	case []byte:
		body = payload.([]byte)
	case string:
		body = []byte(payload.(string))
	case map[string]interface{}:
		body, err = json.Marshal(payload)
	}
	if err != nil {
		s.FailNow("Failed to marshal payload", err)
	}

	bodyReader := bytes.NewReader(body)
	req, err := http.NewRequest(
		"POST",
		fmt.Sprintf("http://localhost:%d/notify", TestServerPort),
		bodyReader,
	)
	if err != nil {
		s.FailNow("Failed to create request", err)
	}
	req.Header.Add(types.ClientIDHdrName, clientID)
	req.Header.Add(types.ClientKeyHdrName, clientKey)
	req.Header.Add("Content-Type", "application/json")

	return http.DefaultClient.Do(req)
}

func (s *IntegrationTestSuite) assertSuccessStatus(resp *http.Response, statusText string, err error) {
	s.NoError(err)
	s.Equal(http.StatusAccepted, resp.StatusCode)
	defer func() {
		_ = resp.Body.Close()
	}()
	content, err := io.ReadAll(resp.Body)
	s.NoError(err)
	var m struct {
		Status string `json:"status"`
	}
	err = json.Unmarshal(content, &m)
	s.NoError(err)
	s.Equal(statusText, m.Status)
	fmt.Printf("%s\n", m.Status)
}

func (s *IntegrationTestSuite) assertFailureStatus(resp *http.Response, statusCode int, err error, text *string) {
	s.NoError(err)
	s.Equal(statusCode, resp.StatusCode)
	defer func() {
		_ = resp.Body.Close()
	}()
	content, err := io.ReadAll(resp.Body)
	s.NoError(err)
	if text != nil {
		s.Contains(string(content), *text)
	}
}

func TestIntegrationTestSuite(t *testing.T) {
	suite.Run(t, new(IntegrationTestSuite))
}
