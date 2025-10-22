//go:build lambda

package main

import (
	"context"
	"encoding/json"
	"enoti/internal/backends"
	"enoti/internal/flow"
	"enoti/internal/ports"
	"enoti/internal/pub"
	"enoti/internal/types"
	"fmt"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/joho/godotenv"
	log "github.com/sirupsen/logrus"
)

// LambdaHandler holds the dependencies needed to process SQS messages
type LambdaHandler struct {
	ClientStore ports.ClientStore
	DataStore   ports.DataStore
	Publisher   ports.Publisher
}

// SQSMessageAttributes contains the expected attributes from FIFO queue messages
type SQSMessageAttributes struct {
	ClientID  string
	ClientKey string
	ClientIP  string // Optional, defaults to "lambda" if not provided
}

func main() {
	// Load environment variables
	envFile := os.Getenv("ENV_FILE")
	if envFile == "" {
		envFile = ".env"
	}
	err := godotenv.Load(envFile)
	if err != nil {
		log.Info("The .env file not found.")
	}

	ctx := context.Background()

	// Initialize AWS SNS client
	var snsEndpoint *string
	se := os.Getenv("SNS_ENDPOINT")
	if se != "" {
		snsEndpoint = aws.String(se)
	}

	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("Failed to load AWS config: %v", err)
	}

	snsClient := sns.NewFromConfig(awsCfg, func(o *sns.Options) {
		if snsEndpoint != nil {
			o.BaseEndpoint = snsEndpoint
			if o.Region == "" {
				o.Region = "us-east-1"
			}
			credProvider := credentials.NewStaticCredentialsProvider("test", "test", "")
			o.Credentials = credProvider
		}
	})

	publisher := pub.NewSNS(snsClient)

	// Initialize backend stores
	clientStore, err := backends.ClientBackendFromEnv()
	if err != nil {
		log.Fatalf("Failed to initialize client store: %v", err)
	}

	dataStore, err := backends.DataBackendFromEnv()
	if err != nil {
		log.Fatalf("Failed to initialize data store: %v", err)
	}

	// Create handler
	handler := &LambdaHandler{
		ClientStore: clientStore,
		DataStore:   dataStore,
		Publisher:   publisher,
	}

	// Start Lambda runtime
	lambda.Start(handler.HandleSQSEvent)
}

// HandleSQSEvent processes SQS messages from a FIFO queue
func (h *LambdaHandler) HandleSQSEvent(ctx context.Context, sqsEvent events.SQSEvent) (events.SQSEventResponse, error) {
	log.Infof("Processing batch of %d messages", len(sqsEvent.Records))

	var batchItemFailures []events.SQSBatchItemFailure

	for _, record := range sqsEvent.Records {
		if err := h.processMessage(ctx, record); err != nil {
			log.WithError(err).Errorf("Failed to process message %s", record.MessageId)
			// For FIFO queues, report failure to preserve ordering
			batchItemFailures = append(batchItemFailures, events.SQSBatchItemFailure{
				ItemIdentifier: record.MessageId,
			})
		}
	}

	return events.SQSEventResponse{
		BatchItemFailures: batchItemFailures,
	}, nil
}

// processMessage handles a single SQS message
func (h *LambdaHandler) processMessage(ctx context.Context, record events.SQSMessage) error {
	// Extract message attributes
	attrs, err := h.extractMessageAttributes(record)
	if err != nil {
		return fmt.Errorf("extract attributes: %w", err)
	}

	log.WithFields(log.Fields{
		"clientID":  attrs.ClientID,
		"messageID": record.MessageId,
		"groupID":   record.Attributes["MessageGroupId"],
	}).Debug("Processing message")

	// Load and cache client config
	cc, err := flow.LoadCachedClientConfig(ctx, h.ClientStore, attrs.ClientID)
	if err != nil {
		return fmt.Errorf("load client config: %w", err)
	}

	// Authenticate
	if err := flow.Auth(ctx, cc, attrs.ClientID, attrs.ClientKey); err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}

	// Parse message body as JSON payload
	var payload map[string]any
	if err := json.Unmarshal([]byte(record.Body), &payload); err != nil {
		return fmt.Errorf("parse message body: %w", err)
	}

	// Run the flow processing (same as HTTP handler)
	action, statusCode, newPayload, err := flow.Run(
		ctx,
		attrs.ClientID,
		attrs.ClientIP,
		cc,
		h.DataStore,
		payload,
	)

	if err != nil {
		log.WithError(err).WithFields(log.Fields{
			"clientID":   attrs.ClientID,
			"statusCode": statusCode,
			"messageID":  record.MessageId,
		}).Error("Flow processing failed")
		return fmt.Errorf("flow.Run: %w", err)
	}

	// Handle actions
	switch action {
	case flow.NoOp, flow.SuppressFlapping, flow.SuppressDedup:
		log.WithFields(log.Fields{
			"action":    flow.StatusTextMap[action],
			"clientID":  attrs.ClientID,
			"messageID": record.MessageId,
		}).Debug("Message suppressed")
		return nil

	case flow.AggregateSent:
		b, err := json.Marshal(newPayload)
		if err != nil {
			return fmt.Errorf("marshal aggregate payload: %w", err)
		}
		if err := h.Publisher.PublishRaw(ctx, cc.Trigger.Target.SNSArn, b); err != nil {
			return fmt.Errorf("publish aggregate to SNS: %w", err)
		}
		log.WithFields(log.Fields{
			"action":    flow.StatusTextMap[action],
			"clientID":  attrs.ClientID,
			"snsArn":    cc.Trigger.Target.SNSArn,
			"messageID": record.MessageId,
		}).Info("Aggregate sent to SNS")
		return nil

	case flow.EdgeTriggeredForward, flow.ForwardedAsIs:
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal payload: %w", err)
		}
		if err := h.Publisher.PublishRaw(ctx, cc.Trigger.Target.SNSArn, b); err != nil {
			return fmt.Errorf("publish to SNS: %w", err)
		}
		log.WithFields(log.Fields{
			"action":    flow.StatusTextMap[action],
			"clientID":  attrs.ClientID,
			"snsArn":    cc.Trigger.Target.SNSArn,
			"messageID": record.MessageId,
		}).Info("Message forwarded to SNS")
		return nil

	default:
		log.WithFields(log.Fields{
			"action":    action,
			"clientID":  attrs.ClientID,
			"messageID": record.MessageId,
		}).Warn("Unknown action")
		return nil
	}
}

// extractMessageAttributes parses SQS message attributes
func (h *LambdaHandler) extractMessageAttributes(record events.SQSMessage) (*SQSMessageAttributes, error) {
	attrs := &SQSMessageAttributes{
		ClientIP: "lambda", // Default value for Lambda context
	}

	// Extract ClientID
	if clientIDAttr, ok := record.MessageAttributes[types.ClientIDHdrName]; ok {
		if clientIDAttr.StringValue != nil {
			attrs.ClientID = *clientIDAttr.StringValue
		}
	}
	if attrs.ClientID == "" {
		return nil, fmt.Errorf("missing required attribute: %s", types.ClientIDHdrName)
	}

	// Extract ClientKey
	if clientKeyAttr, ok := record.MessageAttributes[types.ClientKeyHdrName]; ok {
		if clientKeyAttr.StringValue != nil {
			attrs.ClientKey = *clientKeyAttr.StringValue
		}
	}
	if attrs.ClientKey == "" {
		return nil, fmt.Errorf("missing required attribute: %s", types.ClientKeyHdrName)
	}

	// Optional: Extract ClientIP if provided
	if clientIPAttr, ok := record.MessageAttributes["ClientIP"]; ok {
		if clientIPAttr.StringValue != nil && *clientIPAttr.StringValue != "" {
			attrs.ClientIP = *clientIPAttr.StringValue
		}
	}

	return attrs, nil
}
