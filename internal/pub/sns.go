package pub

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sns/types"
)

type snsPub struct{ cli *sns.Client }

func NewSNS(c *sns.Client) *snsPub { return &snsPub{cli: c} }

func (s *snsPub) PublishRaw(ctx context.Context, arn string, payload []byte) error {
	_, err := s.cli.Publish(ctx, &sns.PublishInput{
		TopicArn: &arn,
		Message:  aws.String(string(payload)),
		MessageAttributes: map[string]types.MessageAttributeValue{
			"content-type": {DataType: aws.String("String"), StringValue: aws.String("application/json")},
		},
	})
	return err
}
