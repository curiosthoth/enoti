package ddb

import (
	"context"
	"enoti/internal/types"
	"errors"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbTypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// DataStore implements ports.DedupStore using a TTL item per key.
type DataStore struct {
	table string
	cli   *dynamodb.Client
}

type dedupItem struct {
	PK        string `dynamodbav:"PK"`
	SK        string `dynamodbav:"SK"`
	ExpiresAt int64  `dynamodbav:"ttl"`
}

func NewDataStore(table string, cli *dynamodb.Client) *DataStore {
	createTableIfNotExists(cli, table)
	return &DataStore{table: table, cli: cli}
}

// Suppress tries to create a TTL row; if it already exists, we suppress.
func (s *DataStore) Suppress(ctx context.Context, clientID, hash string, window time.Duration) (bool, error) {
	item := dedupItem{
		PK:        pkClient(clientID),
		SK:        skDedup(hash),
		ExpiresAt: time.Now().Add(window).Unix(),
	}
	av, _ := attributevalue.MarshalMap(item)
	_, err := s.cli.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           &s.table,
		Item:                av,
		ConditionExpression: awsString("attribute_not_exists(PK) AND attribute_not_exists(SK)"),
	})
	if err != nil {
		var cc *ddbTypes.ConditionalCheckFailedException
		if ok := errorAs(err, &cc); ok {
			return true, nil // key exists within window
		}
		return false, err
	}
	return false, nil
}
func (s *DataStore) Load(ctx context.Context, clientID, scopeKey string) (*types.Edge, int64, error) {
	out, err := s.cli.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      &s.table,
		ConsistentRead: awsBool(true),
		Key: map[string]ddbTypes.AttributeValue{
			"PK": &ddbTypes.AttributeValueMemberS{Value: pkClient(clientID)},
			"SK": &ddbTypes.AttributeValueMemberS{Value: skEdge(scopeKey)},
		},
	})
	if err != nil {
		return nil, 0, err
	}
	if out.Item == nil {
		return nil, 0, nil
	}
	var st types.Edge
	if err := attributevalue.UnmarshalMap(out.Item, &st); err != nil {
		return nil, 0, err
	}
	return &st, st.Version, nil
}

// UpsertCAS creates or updates the row only if ver matches prevVersion.
// On create (prevVersion==0), the row must not exist (attribute_not_exists).
func (s *DataStore) UpsertCAS(ctx context.Context, clientID, scopeKey string, prevVersion int64, next types.Edge) (bool, error) {
	next.ScopeKey = scopeKey // safety
	if prevVersion == 0 {
		next.Version = 1
		av, _ := attributevalue.MarshalMap(map[string]any{
			"PK":             pkClient(clientID),
			"SK":             skEdge(scopeKey),
			"scope_key":      next.ScopeKey,
			"last_value":     next.LastValue,
			"last_change_ts": next.LastChangeTS,
			"window_start":   next.WindowStart,
			"flip_count":     next.FlipCount,
			"recent":         next.Recent,
			"agg_until_ts":   next.AggUntilTS,
			"ver":            next.Version,
		})
		_, err := s.cli.PutItem(ctx, &dynamodb.PutItemInput{
			TableName:           &s.table,
			Item:                av,
			ConditionExpression: awsString("attribute_not_exists(PK) AND attribute_not_exists(SK)"),
		})
		if err != nil {
			var cc *ddbTypes.ConditionalCheckFailedException
			if errorAs(err, &cc) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	}

	// Update with version bump under condition ver == prevVersion
	_, err := s.cli.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &s.table,
		Key: map[string]ddbTypes.AttributeValue{
			"PK": &ddbTypes.AttributeValueMemberS{Value: pkClient(clientID)},
			"SK": &ddbTypes.AttributeValueMemberS{Value: skEdge(scopeKey)},
		},
		UpdateExpression: awsString(
			"SET #lv=:lv, #lcts=:lcts, #ws=:ws, #fc=:fc, #rc=:rc, #aut=:aut, #ver=:newver",
		),
		ExpressionAttributeNames: map[string]string{
			"#lv":   "last_value",
			"#lcts": "last_change_ts",
			"#ws":   "window_start",
			"#fc":   "flip_count",
			"#rc":   "recent",
			"#aut":  "agg_until_ts",
			"#ver":  "ver",
		},
		ExpressionAttributeValues: map[string]ddbTypes.AttributeValue{
			":lv":     &ddbTypes.AttributeValueMemberS{Value: next.LastValue},
			":lcts":   &ddbTypes.AttributeValueMemberN{Value: itoa(next.LastChangeTS)},
			":ws":     &ddbTypes.AttributeValueMemberN{Value: itoa(next.WindowStart)},
			":fc":     &ddbTypes.AttributeValueMemberN{Value: itoa(int64(next.FlipCount))},
			":rc":     mustMarshalAttr(next.Recent),
			":aut":    &ddbTypes.AttributeValueMemberN{Value: itoa(next.AggUntilTS)},
			":newver": &ddbTypes.AttributeValueMemberN{Value: itoa(prevVersion + 1)},
			":prev":   &ddbTypes.AttributeValueMemberN{Value: itoa(prevVersion)},
		},
		ConditionExpression: awsString("#ver = :prev"),
	})
	if err != nil {
		var cc *ddbTypes.ConditionalCheckFailedException
		if errorAs(err, &cc) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *DataStore) Acquire(ctx context.Context, scope string, ratePerWindow int, window time.Duration) (bool, error) {
	if ratePerWindow <= 0 {
		return false, nil
	}
	// Window bucketing by integer minutes only (simple, predictable).
	// We use the minimum of (window, 60s) when deriving TTL — avoid long-lived keys.
	epochMin := time.Now().Unix() / 60
	ttl := time.Now().Add(window + 2*time.Minute).Unix() // grace to ensure cleanup

	// Atomic: ADD count 1, set ttl if absent, condition count < capacity
	// If item does not exist: Initialize count=0 then add 1 -> becomes 1.
	_, err := s.cli.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &s.table,
		Key: map[string]ddbTypes.AttributeValue{
			"PK": &ddbTypes.AttributeValueMemberS{Value: pkRate(scope)},
			"SK": &ddbTypes.AttributeValueMemberS{Value: skRateWin(epochMin)},
		},
		UpdateExpression: awsString(
			"SET #ttl = if_not_exists(#ttl, :ttl) " +
				"ADD #count :one",
		),
		ExpressionAttributeNames: map[string]string{
			"#count": "count",
			"#ttl":   "ttl",
		},
		ExpressionAttributeValues: map[string]ddbTypes.AttributeValue{
			":one": &ddbTypes.AttributeValueMemberN{Value: "1"},
			":ttl": &ddbTypes.AttributeValueMemberN{Value: itoa(ttl)},
			":cap": &ddbTypes.AttributeValueMemberN{Value: itoa(int64(ratePerWindow))},
		},
		ConditionExpression: awsString("attribute_not_exists(#count) OR #count < :cap"),
	})
	if err != nil {
		var cc *ddbTypes.ConditionalCheckFailedException
		if errorAs(err, &cc) {
			return false, nil // limited
		}
		return false, err
	}
	return true, nil
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }

func mustMarshalAttr(v any) ddbTypes.AttributeValue {
	av, _ := attributevalue.Marshal(v)
	return av
}
func awsString(s string) *string         { return &s }
func errorAs(err error, target any) bool { return errors.As(err, target) }
