package sdkbridgeconsumer

import (
	"context"

	consumer "example.com/sdkbridgeconsumer/gen/consumer"
)

type consumerService struct{}

func (consumerService) Lookup(_ context.Context, payload *consumer.LookupPayload) (*consumer.LookupResult, error) {
	answer := "answer:"
	if payload != nil && payload.Query != nil {
		answer += *payload.Query
	}
	return &consumer.LookupResult{Answer: answer}, nil
}
