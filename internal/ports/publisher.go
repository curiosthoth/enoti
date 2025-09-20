package ports

import "context"

type Publisher interface {
	PublishRaw(ctx context.Context, arn string, payload []byte) error
}
