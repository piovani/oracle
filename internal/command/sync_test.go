package command

import (
	"errors"
	"testing"

	"oracle/test/mock"
)

func TestFlushCacheEmptiesIt(t *testing.T) {
	c := &mock.Cache{}
	_ = c.Set(t.Context(), "/tickers?", []byte("x\n{}"), 0)

	flushCache(c)

	if c.FlushCalls() != 1 {
		t.Errorf("flushed %d times, want once", c.FlushCalls())
	}
}

func TestFlushCacheSurvivesARedisThatIsDown(t *testing.T) {
	c := &mock.Cache{FlushErr: errors.New("redis down")}
	flushCache(c) // a warning, not a failed sync
	if c.FlushCalls() != 1 {
		t.Errorf("flushed %d times, want one attempt", c.FlushCalls())
	}
}
