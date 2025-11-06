package queue

import (
	"context"
	"encoding/json"
)

type Publisher interface {
	Publish(ctx context.Context, queueName string, message interface{}) error
}

type MockPublisher struct{}

func (m *MockPublisher) Publish(ctx context.Context, queueName string, message interface{}) error {
	return nil
}

type WithdrawalMessage struct {
	WithdrawalID string `json:"withdrawal_id"`
	Step         string `json:"step"`
}

func NewMockPublisher() Publisher {
	return &MockPublisher{}
}

func MarshalMessage(msg interface{}) ([]byte, error) {
	return json.Marshal(msg)
}
