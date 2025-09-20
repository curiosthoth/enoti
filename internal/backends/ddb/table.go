package ddb

import (
	"context"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbTypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	log "github.com/sirupsen/logrus"
)

const (
	SClient = "CLIENT"
	SRate   = "RATE"
	SEdge   = "EDGE"
	SDedup  = "DEDUP"
	SWin    = "WIN"
)

func pkClient(id string) string       { return fmt.Sprintf("%s#%s", SClient, id) }
func skProfile() string               { return "PROFILE" }
func skDedup(hash string) string      { return fmt.Sprintf("%s#%s", SDedup, hash) }
func pkRate(scope string) string      { return fmt.Sprintf("%s#%s", SRate, scope) }
func skRateWin(epochMin int64) string { return fmt.Sprintf("%s#%d", SWin, epochMin) }
func skEdge(scopeKey string) string   { return fmt.Sprintf("%s#%s", SEdge, scopeKey) }

func parseClientID(pk string) (string, error) {
	var id string
	_, err := fmt.Sscanf(pk, "CLIENT#%s", &id)
	if err != nil {
		return "", err
	}
	return id, nil
}

func createTableIfNotExists(client *dynamodb.Client, table string) {
	_, err := client.CreateTable(context.Background(), &dynamodb.CreateTableInput{
		TableName: &table,
		AttributeDefinitions: []ddbTypes.AttributeDefinition{
			{AttributeName: awsString("PK"), AttributeType: ddbTypes.ScalarAttributeTypeS},
			{AttributeName: awsString("SK"), AttributeType: ddbTypes.ScalarAttributeTypeS},
		},
		KeySchema: []ddbTypes.KeySchemaElement{
			{AttributeName: awsString("PK"), KeyType: ddbTypes.KeyTypeHash},
			{AttributeName: awsString("SK"), KeyType: ddbTypes.KeyTypeRange},
		},
		BillingMode: ddbTypes.BillingModePayPerRequest,
	})
	var re *ddbTypes.ResourceInUseException
	if err != nil && !errors.As(err, &re) {
		log.Fatalf("Failed to create table %s: %v", table, err)
	}
}
