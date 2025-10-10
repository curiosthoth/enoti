package backends

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"enoti/internal/backends/ddb"
	"enoti/internal/ports"
	"fmt"
	"os"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/redis/go-redis/v9"

	redisbackend "enoti/internal/backends/redis"
)

const (
	ClientBackendEnvKey = "CLIENT_BACKEND"
	DataBackendEnvKey   = "DATA_BACKEND"
	BackendDDB          = "ddb"
	BackendRedis        = "redis"

	DDBEndpointKey = "DDB_ENDPOINT"
	DDBTableKey    = "DDB_TABLE"

	RedisHost  = "REDIS_HOST"
	RedisPort  = "REDIS_PORT"
	RedisUser  = "REDIS_USER"
	RedisPass  = "REDIS_PASS"
	RedisTLS   = "REDIS_SSL"
	RedisDBNum = "REDIS_DB_NUM"
)
const AmazonRootCA1PEM = `-----BEGIN CERTIFICATE-----
MIIDQTCCAimgAwIBAgITBmyfz5m/jAo54vB4ikPmljZbyjANBgkqhkiG9w0BAQsF
ADA5MQswCQYDVQQGEwJVUzEPMA0GA1UEChMGQW1hem9uMRkwFwYDVQQDExBBbWF6
b24gUm9vdCBDQSAxMB4XDTE1MDUyNjAwMDAwMFoXDTM4MDExNzAwMDAwMFowOTEL
MAkGA1UEBhMCVVMxDzANBgNVBAoTBkFtYXpvbjEZMBcGA1UEAxMQQW1hem9uIFJv
b3QgQ0EgMTCCASIwDQYJKoZIhvcNAQEBBQADggEPADCCAQoCggEBALJ4gHHKeNXj
ca9HgFB0fW7Y14h29Jlo91ghYPl0hAEvrAIthtOgQ3pOsqTQNroBvo3bSMgHFzZM
9O6II8c+6zf1tRn4SWiw3te5djgdYZ6k/oI2peVKVuRF4fn9tBb6dNqcmzU5L/qw
IFAGbHrQgLKm+a/sRxmPUDgH3KKHOVj4utWp+UhnMJbulHheb4mjUcAwhmahRWa6
VOujw5H5SNz/0egwLX0tdHA114gk957EWW67c4cX8jJGKLhD+rcdqsq08p8kDi1L
93FcXmn/6pUCyziKrlA4b9v7LWIbxcceVOF34GfID5yHI9Y/QCB/IIDEgEw+OyQm
jgSubJrIqg0CAwEAAaNCMEAwDwYDVR0TAQH/BAUwAwEB/zAOBgNVHQ8BAf8EBAMC
AYYwHQYDVR0OBBYEFIQYzIU07LwMlJQuCFmcx7IQTgoIMA0GCSqGSIb3DQEBCwUA
A4IBAQCY8jdaQZChGsV2USggNiMOruYou6r4lK5IpDB/G/wkjUu0yKGX9rbxenDI
U5PMCCjjmCXPI6T53iHTfIUJrU6adTrCC2qJeHZERxhlbI1Bjjt/msv0tadQ1wUs
N+gDS63pYaACbvXy8MWy7Vu33PqUXHeeE6V/Uq2V8viTO96LXFvKWlJbYK8U90vv
o/ufQJVtMVT8QtPHRh8jrdkPSHCa2XV4cdFyQzR1bldZwgJcJmApzyMZFo6IQ6XU
5MsI+yMRQ+hDKXJioaldXgjUkK642M4UwtBV8ob2xJNDd2ZhwLnoQdeXeGADbkpy
rqXRfboQnoZsG4q5WTP468SQvvG5
-----END CERTIFICATE-----`

// ClientBackendFromEnv constructs a ClientStore based on environment variables.
// Supported backends are "ddb" (DynamoDB) and "redis" (Redis).
// If no backend is specified, defaults to "ddb". It first checks the "CLIENT_BACKEND" env var,
// to determine which backend to use. Depending on the backend, it reads additional env vars.
// Default to BackendDDB if unspecified or unrecognized.
func ClientBackendFromEnv() (clientStore ports.ClientStore, err error) {
	backend := os.Getenv(ClientBackendEnvKey)
	switch backend {
	case BackendRedis:
		var redisClient *redis.Client
		redisClient, err = redisClientFromEnv()
		if err != nil {
			return nil, err
		}
		clientStore = redisbackend.NewClientStore(redisClient)

	case BackendDDB:
		fallthrough
	case "":
		fallthrough
	default:
		var ddbClient *dynamodb.Client
		ddbClient, err = ddbClientFromEnv()
		if err != nil {
			return nil, err
		}
		table := getenv("DDB_TABLE", "notify_guard")
		clientStore = ddb.NewClientStore(table, ddbClient)
	}
	return
}

// DataBackendFromEnv constructs a DataStore based on environment variables.
// Supported backends are "ddb" (DynamoDB) and "redis" (Redis).
// If no backend is specified, defaults to "ddb". It first checks the "DATA_BACKEND" env var,
// to determine which backend to use. Depending on the backend, it reads additional env vars.
// Default to BackendDDB if unspecified or unrecognized.
func DataBackendFromEnv() (dataStore ports.DataStore, err error) {
	backend := os.Getenv(DataBackendEnvKey)
	switch backend {
	case BackendRedis:
		var redisClient *redis.Client
		redisClient, err = redisClientFromEnv()
		if err != nil {
			return nil, err
		}
		dataStore = redisbackend.NewDataStore(redisClient)

	case BackendDDB:
		fallthrough
	case "":
		fallthrough
	default:
		var ddbClient *dynamodb.Client
		ddbClient, err = ddbClientFromEnv()
		if err != nil {
			return nil, err
		}
		table := getenv(DDBTableKey, "notify_guard")
		dataStore = ddb.NewDataStore(table, ddbClient)
	}
	return
}

// ddbClientFromEnv creates a DynamoDB client from environment variables, if any.
func ddbClientFromEnv() (*dynamodb.Client, error) {
	var ddbEndpoint *string
	de := os.Getenv("DDB_ENDPOINT")
	if de != "" {
		ddbEndpoint = aws.String(de)
	}

	awsCfg, err := config.LoadDefaultConfig(context.Background())

	if err != nil {
		return nil, err
	}

	ddbClient := dynamodb.NewFromConfig(awsCfg, func(o *dynamodb.Options) {
		if ddbEndpoint != nil {
			// This is used for testing only locally
			o.BaseEndpoint = ddbEndpoint
			o.Region = getenv("AWS_REGION", "us-east-1")
			credProvider := credentials.NewStaticCredentialsProvider(
				getenv("AWS_ACCESS_KEY_ID", "x"),
				getenv("AWS_SECRET_ACCESS_KEY", "x"),
				"",
			)
			o.Credentials = credProvider
		}
	})
	return ddbClient, nil
}

// redisClientFromEnv creates a Redis client from environment variables, if any.
func redisClientFromEnv() (*redis.Client, error) {
	host := getenv(RedisHost, "localhost")
	port := getenv(RedisPort, "6379")
	user := os.Getenv(RedisUser)
	pass := os.Getenv(RedisPass)
	tlsEnabled := parseBoolean(getenv(RedisTLS, "false"))
	dbNumStr := getenv(RedisDBNum, "0")
	dbNum, err := strconv.Atoi(dbNumStr)
	if err != nil {
		return nil, fmt.Errorf("invalid Redis DB number: %w", err)
	}

	var tlsConfig *tls.Config
	if tlsEnabled {
		// Create a CA certificate pool and add our CA certificate
		caCerts := x509.NewCertPool()
		if !caCerts.AppendCertsFromPEM([]byte(AmazonRootCA1PEM)) {
			return nil, fmt.Errorf("failed to retrieve CA certificate")
		}
		tlsConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			RootCAs:    caCerts,
		}
	}

	redisConfig := redis.Options{
		Addr:      fmt.Sprintf("%s:%s", host, port),
		Username:  user,
		Password:  pass,
		DB:        dbNum,
		TLSConfig: tlsConfig,
	}
	redisClient := redis.NewClient(&redisConfig)
	_, err = redisClient.Ping(context.Background()).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}
	return redisClient, nil
}

// getenv retrieves the value of the environment variable named by the key.
func getenv(key, def string) string {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	return v
}

func parseBoolean(s string) bool {
	b, err := strconv.ParseBool(s)
	if err != nil {
		return false
	}
	return b
}
