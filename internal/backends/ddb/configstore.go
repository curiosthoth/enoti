package ddb

import (
	"context"
	"enoti/internal/types"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbTypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type ClientStore struct {
	table string
	cli   *dynamodb.Client
}

func NewClientStore(table string, cli *dynamodb.Client) *ClientStore {
	// Creates the table only if it doesn't exist.
	// We ignore the error if the table already exists.
	createTableIfNotExists(cli, table)
	return &ClientStore{table: table, cli: cli}
}

func (s *ClientStore) GetClientConfig(ctx context.Context, id string) (types.ClientConfig, error) {
	out, err := s.cli.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &s.table,
		Key: map[string]ddbTypes.AttributeValue{
			"PK": &ddbTypes.AttributeValueMemberS{Value: pkClient(id)},
			"SK": &ddbTypes.AttributeValueMemberS{Value: skProfile()},
		},
		ConsistentRead: awsBool(true),
	})
	if err != nil {
		return types.ClientConfig{}, err
	}
	if out.Item == nil {
		return types.ClientConfig{}, types.ErrNotFound
	}
	var cc types.ClientConfig
	if err := attributevalue.UnmarshalMap(out.Item, &cc); err != nil {
		return types.ClientConfig{}, err
	}
	return cc, nil
}

func (s *ClientStore) ListClients(ctx context.Context) ([]string, error) {
	// Scans the table with Pk starting with "CLIENT#"
	// and only project the pk
	out, err := s.cli.Query(ctx, &dynamodb.QueryInput{
		TableName:              &s.table,
		KeyConditionExpression: awsString("PK = :pk AND begins_with(SK, :sk)"),
		ExpressionAttributeValues: map[string]ddbTypes.AttributeValue{
			":pk": &ddbTypes.AttributeValueMemberS{Value: "CLIENT#"},
			":sk": &ddbTypes.AttributeValueMemberS{Value: "PROFILE#"},
		},
		ProjectionExpression: awsString("PK"),
	})
	if err != nil {
		return nil, err
	}
	clientIDs := make([]string, 0, len(out.Items))
	for _, item := range out.Items {
		var pk struct {
			PK string `dynamodbav:"PK"`
		}
		if err := attributevalue.UnmarshalMap(item, &pk); err != nil {
			return nil, err
		}
		id, err := parseClientID(pk.PK)
		if err != nil {
			return nil, err
		}
		if id != "" {
			clientIDs = append(clientIDs, id)
		}
	}
	return clientIDs, nil
}

func (s *ClientStore) PutClientConfig(ctx context.Context, clientID string, config types.ClientConfig) error {
	pk := pkClient(clientID)
	sk := skProfile()
	if err := config.Validate(); err != nil {
		return err
	}
	item, err := attributevalue.MarshalMap(struct {
		PK string `dynamodbav:"PK"`
		SK string `dynamodbav:"SK"`
		types.ClientConfig
	}{
		PK:           pk,
		SK:           sk,
		ClientConfig: config,
	})
	if err != nil {
		return err
	}
	_, err = s.cli.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &s.table,
		Item:      item,
	})
	return err
}

func (s *ClientStore) DeleteClientConfig(ctx context.Context, clientID string) error {
	pk := pkClient(clientID)
	sk := skProfile()
	_, err := s.cli.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: &s.table,
		Key: map[string]ddbTypes.AttributeValue{
			"PK": &ddbTypes.AttributeValueMemberS{Value: pk},
			"SK": &ddbTypes.AttributeValueMemberS{Value: sk},
		},
	})
	return err
}
func (s *ClientStore) ClearAll(ctx context.Context) error {
	// delete all items in the table
	_, err := s.cli.DeleteTable(ctx, &dynamodb.DeleteTableInput{
		TableName: &s.table,
	})
	if err != nil {
		return err
	}
	// wait until the table is deleted
	err = dynamodb.NewTableNotExistsWaiter(s.cli).Wait(ctx, &dynamodb.DescribeTableInput{
		TableName: aws.String(s.table),
	}, 30*time.Second)
	if err != nil {
		return err
	}
	// Recreate the table
	createTableIfNotExists(s.cli, s.table)
	return nil
}
func awsBool(b bool) *bool { return &b }
