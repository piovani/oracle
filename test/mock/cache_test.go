package mock_test

import (
	"testing"

	"oracle/internal/cache"
	"oracle/test/contract"
	"oracle/test/mock"
)

func TestCacheContract(t *testing.T) {
	t.Parallel()
	contract.Cache(t, func(*testing.T) cache.Cache { return &mock.Cache{} })
}
