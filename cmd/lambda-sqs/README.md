# Enoti AWS Lambda SQS Handler

This Lambda function processes notifications from an Amazon SQS FIFO queue, applying the same edge-triggered filtering, flap detection, and aggregation logic as the HTTP endpoint.

## Architecture

```
SQS FIFO Queue → Lambda Function → Enoti Flow Processing → SNS Publish
     ↓
  Message Attributes:
  - X-Client-ID (required)
  - X-Client-Key (required)
  - ClientIP (optional)

  Message Body: JSON notification payload
```

## Why FIFO Queue?

For Enoti's edge-triggered notification system, **FIFO queues are recommended** because:

1. **Ordering guarantees**: Edge-triggered state transitions depend on the correct sequence of value changes (A→B→C)
2. **Flap detection accuracy**: Time-windowed flap counting requires proper temporal ordering
3. **Exactly-once processing**: Built-in deduplication prevents duplicate notifications
4. **Per-scope ordering**: Using `MessageGroupId = clientID:scopeKey` allows parallel processing across different scopes while maintaining ordering within each scope

### FIFO Queue Configuration

```bash
# Create FIFO queue
aws sqs create-queue \
  --queue-name enoti-notifications.fifo \
  --attributes '{
    "FifoQueue": "true",
    "ContentBasedDeduplication": "false",
    "DeduplicationScope": "messageGroup",
    "FifoThroughputLimit": "perMessageGroupId",
    "MessageRetentionPeriod": "345600",
    "VisibilityTimeout": "300"
  }'
```

**Key settings:**
- `FifoQueue`: `true` - Enables FIFO ordering
- `ContentBasedDeduplication`: `false` - Use explicit MessageDeduplicationId
- `DeduplicationScope`: `messageGroup` - Deduplication per message group
- `FifoThroughputLimit`: `perMessageGroupId` - 3,000 msg/sec per group
- `VisibilityTimeout`: `300` - 5 minutes (adjust based on Lambda timeout)

## Building the Lambda Function

```bash
# Build for Linux (Lambda runtime)
GOOS=linux GOARCH=arm64 go build -o bootstrap cmd/lambda-sqs/main.go

# Create deployment package
zip lambda-sqs.zip bootstrap

# Or use Docker for more complex builds
docker run --rm -v "$PWD":/var/task \
  -w /var/task \
  public.ecr.aws/lambda/provided:al2023 \
  sh -c "GOOS=linux GOARCH=arm64 go build -o bootstrap cmd/lambda-sqs/main.go && zip lambda-sqs.zip bootstrap"
```

## Deploying the Lambda Function

### Using AWS CLI

```bash
# Create Lambda execution role (if not exists)
aws iam create-role \
  --role-name enoti-lambda-sqs-role \
  --assume-role-policy-document '{
    "Version": "2012-10-17",
    "Statement": [{
      "Effect": "Allow",
      "Principal": {"Service": "lambda.amazonaws.com"},
      "Action": "sts:AssumeRole"
    }]
  }'

# Attach policies
aws iam attach-role-policy \
  --role-name enoti-lambda-sqs-role \
  --policy-arn arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole

aws iam attach-role-policy \
  --role-name enoti-lambda-sqs-role \
  --policy-arn arn:aws:iam::aws:policy/AmazonSQSFullAccess

aws iam attach-role-policy \
  --role-name enoti-lambda-sqs-role \
  --policy-arn arn:aws:iam::aws:policy/AmazonSNSFullAccess

aws iam attach-role-policy \
  --role-name enoti-lambda-sqs-role \
  --policy-arn arn:aws:iam::aws:policy/AmazonDynamoDBFullAccess

# Create Lambda function
aws lambda create-function \
  --function-name enoti-sqs-processor \
  --runtime provided.al2023 \
  --architectures arm64 \
  --role arn:aws:iam::YOUR_ACCOUNT_ID:role/enoti-lambda-sqs-role \
  --handler bootstrap \
  --zip-file fileb://lambda-sqs.zip \
  --timeout 300 \
  --memory-size 512 \
  --environment Variables='{
    "BACKEND_TYPE":"dynamodb",
    "DDB_TABLE_NAME":"enoti-clients",
    "DATA_BACKEND_TYPE":"dynamodb",
    "DATA_DDB_TABLE_NAME":"enoti-data"
  }'

# Configure SQS trigger with batch item failure reporting
aws lambda create-event-source-mapping \
  --function-name enoti-sqs-processor \
  --event-source-arn arn:aws:sqs:REGION:ACCOUNT_ID:enoti-notifications.fifo \
  --batch-size 10 \
  --maximum-batching-window-in-seconds 5 \
  --function-response-types ReportBatchItemFailures
```

### Environment Variables

Configure these in your Lambda function:

| Variable | Required | Description | Example |
|----------|----------|-------------|---------|
| `BACKEND_TYPE` | Yes | Client config backend | `dynamodb` or `redis` |
| `DDB_TABLE_NAME` | Yes (DDB) | DynamoDB table for client configs | `enoti-clients` |
| `DATA_BACKEND_TYPE` | Yes | Data storage backend | `dynamodb` or `redis` |
| `DATA_DDB_TABLE_NAME` | Yes (DDB) | DynamoDB table for state/rate limits | `enoti-data` |
| `REDIS_ADDR` | Yes (Redis) | Redis connection string | `localhost:6379` |
| `SNS_ENDPOINT` | No | Custom SNS endpoint (testing only) | `http://localhost:4566` |

## Sending Messages to SQS

### Message Format

```json
{
  "MessageBody": "{\"sensor\":\"temp-01\",\"value\":72,\"timestamp\":\"2025-01-15T10:30:00Z\"}",
  "MessageAttributes": {
    "X-Client-ID": {
      "DataType": "String",
      "StringValue": "my-app"
    },
    "X-Client-Key": {
      "DataType": "String",
      "StringValue": "secret-key-123"
    },
    "ClientIP": {
      "DataType": "String",
      "StringValue": "192.168.1.100"
    }
  },
  "MessageGroupId": "my-app:e1234567890",
  "MessageDeduplicationId": "unique-request-id-12345"
}
```

### Message Attributes (Required)

- **X-Client-ID**: Client identifier (must match config in DynamoDB/Redis)
- **X-Client-Key**: Authentication key
- **ClientIP** (Optional): Client IP for rate limiting (defaults to "lambda")

### FIFO Identifiers

#### MessageGroupId
Format: `{clientID}:{scopeKey}`

Where `scopeKey` is the hash of the trigger field expression. For consistent ordering:
- Use the **same MessageGroupId** for all messages from the same client/scope combination
- Different scopes can process in parallel
- Example: `my-app:e2718281828` (scopeKey is computed via `flow.ComputeKey()`)

To compute the scopeKey:
```go
import "enoti/internal/flow"

// If your trigger field expression is "sensor.temperature"
scopeKey := flow.ComputeKey("sensor.temperature")
messageGroupID := fmt.Sprintf("%s:%s", clientID, scopeKey)
```

Or use a simpler approach: use just `clientID` if you want all notifications for a client to be ordered:
```
MessageGroupId = clientID
```

#### MessageDeduplicationId
Use a unique identifier per message to prevent duplicates:
- Request ID
- Content hash
- UUID
- Timestamp + sequence number

### Example: Sending via AWS CLI

```bash
aws sqs send-message \
  --queue-url https://sqs.us-east-1.amazonaws.com/123456789/enoti-notifications.fifo \
  --message-body '{"sensor":"temp-01","value":72,"timestamp":"2025-01-15T10:30:00Z"}' \
  --message-attributes '{
    "X-Client-ID":{"DataType":"String","StringValue":"my-app"},
    "X-Client-Key":{"DataType":"String","StringValue":"secret-key-123"},
    "ClientIP":{"DataType":"String","StringValue":"192.168.1.100"}
  }' \
  --message-group-id "my-app:e2718281828" \
  --message-deduplication-id "$(uuidgen)"
```

### Example: Sending via AWS SDK (Go)

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/service/sqs"
    "github.com/aws/aws-sdk-go-v2/service/sqs/types"
    "github.com/google/uuid"
)

func sendNotification(ctx context.Context, queueURL, clientID, clientKey string, payload map[string]any) error {
    cfg, err := config.LoadDefaultConfig(ctx)
    if err != nil {
        return err
    }

    client := sqs.NewFromConfig(cfg)

    // Marshal payload
    body, err := json.Marshal(payload)
    if err != nil {
        return err
    }

    // Compute scope key (or use clientID for simplicity)
    messageGroupID := clientID

    // Send message
    _, err = client.SendMessage(ctx, &sqs.SendMessageInput{
        QueueUrl:    aws.String(queueURL),
        MessageBody: aws.String(string(body)),
        MessageAttributes: map[string]types.MessageAttributeValue{
            "X-Client-ID": {
                DataType:    aws.String("String"),
                StringValue: aws.String(clientID),
            },
            "X-Client-Key": {
                DataType:    aws.String("String"),
                StringValue: aws.String(clientKey),
            },
        },
        MessageGroupId:         aws.String(messageGroupID),
        MessageDeduplicationId: aws.String(uuid.New().String()),
    })

    return err
}
```

## Error Handling

### Batch Item Failures

The Lambda handler reports individual message failures back to SQS using `ReportBatchItemFailures`. For FIFO queues:
- Failed messages **block subsequent messages in the same MessageGroup**
- Other MessageGroups continue processing
- Failed messages are retried based on queue's redrive policy

### Retry Strategy

Configure a dead-letter queue (DLQ) for messages that fail repeatedly:

```bash
# Create DLQ
aws sqs create-queue \
  --queue-name enoti-notifications-dlq.fifo \
  --attributes FifoQueue=true

# Configure redrive policy
aws sqs set-queue-attributes \
  --queue-url https://sqs.REGION.amazonaws.com/ACCOUNT/enoti-notifications.fifo \
  --attributes '{
    "RedrivePolicy": "{\"deadLetterTargetArn\":\"arn:aws:sqs:REGION:ACCOUNT:enoti-notifications-dlq.fifo\",\"maxReceiveCount\":\"3\"}"
  }'
```

## Monitoring

### CloudWatch Metrics

Monitor these Lambda metrics:
- `Invocations`: Total invocations
- `Errors`: Failed invocations
- `Duration`: Processing time
- `ConcurrentExecutions`: Concurrent Lambda instances

Monitor these SQS metrics:
- `ApproximateNumberOfMessagesVisible`: Messages in queue
- `ApproximateAgeOfOldestMessage`: Oldest message age
- `NumberOfMessagesSent`: Message throughput

### CloudWatch Logs

Lambda automatically logs to CloudWatch Logs. Key log fields:
- `clientID`: Client identifier
- `messageID`: SQS message ID
- `groupID`: Message group ID
- `action`: Flow action (NoOp, EdgeTriggeredForward, etc.)
- `snsArn`: Target SNS topic

Example query:
```
fields @timestamp, clientID, action, messageID
| filter action = "EdgeTriggeredForward"
| sort @timestamp desc
| limit 100
```

## Performance Tuning

### Batch Size
- Default: `10` messages per batch
- Max: `10` for FIFO (lower than standard queues)
- Increase for higher throughput (trade-off: longer processing time)

### Reserved Concurrency
For FIFO queues, limit concurrency to avoid overwhelming downstream systems:
```bash
aws lambda put-function-concurrency \
  --function-name enoti-sqs-processor \
  --reserved-concurrent-executions 10
```

### Memory Allocation
- Start with 512 MB
- Monitor CloudWatch metrics
- Increase if you see high duration or throttling

## Testing Locally

Use LocalStack or AWS SAM for local testing:

```bash
# Start LocalStack
docker run -d -p 4566:4566 localstack/localstack

# Set environment variable
export SNS_ENDPOINT=http://localhost:4566

# Invoke locally with SAM
sam local invoke -e test-event.json
```

Example `test-event.json`:
```json
{
  "Records": [
    {
      "messageId": "test-message-1",
      "receiptHandle": "test-receipt-handle",
      "body": "{\"sensor\":\"temp-01\",\"value\":72}",
      "attributes": {
        "MessageGroupId": "my-app:e1234"
      },
      "messageAttributes": {
        "X-Client-ID": {
          "stringValue": "my-app",
          "dataType": "String"
        },
        "X-Client-Key": {
          "stringValue": "test-key",
          "dataType": "String"
        }
      }
    }
  ]
}
```

## Migration from HTTP to SQS

If you're migrating from the HTTP endpoint:

1. **Dual-mode operation**: Run both HTTP and Lambda simultaneously
2. **Client migration**: Update clients to send to SQS instead of HTTP
3. **Message attributes**: Map HTTP headers to SQS message attributes:
   - `X-Client-ID` header → `X-Client-ID` message attribute
   - `X-Client-Key` header → `X-Client-Key` message attribute
   - Client IP → `ClientIP` message attribute (or omit)
4. **MessageGroupId**: Use `clientID:scopeKey` for proper ordering
5. **Monitoring**: Compare CloudWatch metrics between HTTP and SQS

## Troubleshooting

### Messages stuck in queue
- Check CloudWatch Logs for errors
- Verify DynamoDB/Redis connectivity
- Check IAM permissions
- Review message format and attributes

### Out-of-order processing
- Verify FIFO queue configuration
- Check MessageGroupId format
- Ensure messages for same scope use same group ID

### High latency
- Increase Lambda memory
- Reduce batch size
- Check DynamoDB/Redis performance
- Review network connectivity

### Authentication failures
- Verify client config exists in DynamoDB/Redis
- Check X-Client-Key matches stored value
- Ensure message attributes are set correctly

## Security Best Practices

1. **Encrypt messages**: Enable SQS encryption at rest
2. **IAM policies**: Use least-privilege IAM roles
3. **VPC**: Deploy Lambda in VPC if accessing private resources
4. **Secrets**: Store client keys in AWS Secrets Manager, not environment variables
5. **Monitoring**: Enable CloudTrail for API audit logs

## Cost Optimization

- Use ARM64 architecture (20% cost savings)
- Right-size memory allocation
- Configure appropriate batch size
- Use reserved concurrency to prevent runaway costs
- Monitor and set billing alarms
