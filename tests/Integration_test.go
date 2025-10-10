package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"enoti/cmd/enoti/cmds"
	"enoti/internal/api"
	"enoti/internal/backends/ddb"
	redisbackend "enoti/internal/backends/redis"
	"enoti/internal/flow"
	"enoti/internal/ports"
	"enoti/internal/types"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/suite"
)

const (
	AWSMockPort    = 4566
	LocalRedisPort = 46379
	TestServerPort = 39080
	TestTableName  = "notify_guard_test-clients"
)

type IntegrationTestSuite struct {
	suite.Suite

	publisher *TestPublish

	clientStore ports.ClientStore
	dataStore   ports.DataStore
	stopChan    chan<- struct{} // Send only
	doneChan    <-chan error    // Receive only
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
	if os.Getenv("TEST_USE_REDIS_BACKEND") != "" {
		s.initRedisBackend()
	} else {
		s.initDDBBackend(context.Background())
	}
	s.publisher = &TestPublish{}
	s.publisher.SetOnPublish(func(ctx context.Context, arn string, payload []byte) error {
		fmt.Printf("PublishRaw called: [%s] %s\n", arn, string(payload))
		return nil
	})
	// Start go routine with the api.RunServer()
	s.stopChan, s.doneChan = api.RunServerInterruptible(
		TestServerPort,
		s.clientStore,
		s.dataStore,
		s.publisher,
	)
}

// initDDBBackend requires the local AWS Mock (moto) running at port `AWSMockPort`
func (s *IntegrationTestSuite) initDDBBackend(ctx context.Context) {
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
	s.clientStore = ddb.NewClientStore(TestTableName, ddbClient)
	s.dataStore = ddb.NewDataStore(TestTableName, ddbClient)
}

func (s *IntegrationTestSuite) initRedisBackend() {
	redisClient := redis.NewClient(&redis.Options{
		Addr: fmt.Sprintf("localhost:%d", LocalRedisPort),
		DB:   0, // use default DB
	})
	s.clientStore = redisbackend.NewClientStore(redisClient)
	s.dataStore = redisbackend.NewDataStore(redisClient)
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
