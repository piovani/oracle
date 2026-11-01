package mock_test

import (
	"testing"

	"oracle/internal/quote"
	"oracle/test/contract"
	"oracle/test/mock"
)

// The in-memory store must behave like the Postgres one; the same suite runs against Postgres in test/integration.
func TestStoreContract(t *testing.T) {
	contract.Store(t, func(*testing.T) quote.Store { return &mock.Store{} })
}
