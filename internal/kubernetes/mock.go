package kubernetes

type MockClient struct{}

func NewMockClient() *MockClient { return &MockClient{} }
