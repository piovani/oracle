package handler_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"oracle/internal/handler"
	"oracle/internal/quote"
	"oracle/test/fixture"
	"oracle/test/mock"
)

var day = fixture.Day(2026, 10, 30)

func clock() time.Time { return fixture.Day(2026, 11, 2) }

type slowStore struct {
	*mock.Store
	release chan struct{}
	loads   atomic.Int32
	since   []time.Time
	mu      sync.Mutex
}

func (s *slowStore) History(ctx context.Context, since time.Time) (map[string][]quote.Quote, error) {
	s.loads.Add(1)
	s.mu.Lock()
	s.since = append(s.since, since)
	s.mu.Unlock()
	<-s.release
	return s.Store.History(ctx, since)
}

func newSlowStore(quotes ...quote.Quote) *slowStore {
	release := make(chan struct{})
	close(release)
	return &slowStore{Store: &mock.Store{Quotes: quotes}, release: release}
}

func bars() []quote.Quote { return fixture.Trend("WIN3", 30, day, nil) }

func TestHistoriesLoadOnceForRequestsAtTheSameTime(t *testing.T) {
	store := newSlowStore(bars()...)
	store.release = make(chan struct{})
	histories := handler.NewHistories(store, clock)

	var wg sync.WaitGroup
	results := make([]int, 6)
	for i := range results {
		wg.Go(func() {
			history, latest, err := histories.Load(t.Context(), 3)
			if err != nil || !latest.Equal(day) {
				t.Errorf("load %d: %v, latest %v", i, err, latest)
			}
			results[i] = len(history["WIN3"])
		})
	}
	time.Sleep(50 * time.Millisecond) // let every request reach the cache before the load ends
	close(store.release)
	wg.Wait()

	if n := store.loads.Load(); n != 1 {
		t.Errorf("loaded %d times, want once for 6 requests", n)
	}
	if !slices.Equal(results, []int{30, 30, 30, 30, 30, 30}) {
		t.Errorf("got %v bars", results)
	}
	if want := fixture.Day(2023, 11, 2); !store.since[0].Equal(want) {
		t.Errorf("loaded since %v, want %v: 3 years before the clock, from midnight", store.since[0], want)
	}
}

func TestHistoriesReloadForANewSession(t *testing.T) {
	store := newSlowStore(bars()...)
	histories := handler.NewHistories(store, clock)

	for range 3 {
		if _, _, err := histories.Load(t.Context(), 3); err != nil {
			t.Fatal(err)
		}
	}
	store.Quotes = append(store.Quotes, fixture.Trend("WIN3", 1, day.AddDate(0, 0, 3), nil)...)
	_, latest, err := histories.Load(t.Context(), 3)
	if err != nil || !latest.Equal(day.AddDate(0, 0, 3)) || store.loads.Load() != 2 {
		t.Errorf("got latest %v, %v after %d loads, want a reload for the new session", latest, err, store.loads.Load())
	}
}

func TestHistoriesKeepTheLastThree(t *testing.T) {
	store := newSlowStore(bars()...)
	histories := handler.NewHistories(store, clock)
	for _, years := range []int{1, 2, 3, 1, 4} { // 1 is used again, so 2 is the oldest when 4 comes
		if _, _, err := histories.Load(t.Context(), years); err != nil {
			t.Fatal(err)
		}
	}
	if store.loads.Load() != 4 {
		t.Fatalf("loaded %d times, want 4: the repeated 1 year is cached", store.loads.Load())
	}
	for _, years := range []int{2, 4} {
		if _, _, err := histories.Load(t.Context(), years); err != nil {
			t.Fatal(err)
		}
	}
	if store.loads.Load() != 5 {
		t.Errorf("loaded %d times, want 5: 2 years was dropped, 4 years is still there", store.loads.Load())
	}
}

func TestHistoriesTryAgainAfterAFailure(t *testing.T) {
	store := newSlowStore(bars()...)
	store.HistoryErr = errors.New("connection reset")
	histories := handler.NewHistories(store, clock)

	if _, _, err := histories.Load(t.Context(), 3); err == nil {
		t.Fatal("want the load error")
	}
	store.HistoryErr = nil
	if _, latest, err := histories.Load(t.Context(), 3); err != nil || !latest.Equal(day) || store.loads.Load() != 2 {
		t.Errorf("got %v, %v after %d loads, want the failed load tried again", latest, err, store.loads.Load())
	}
}

func TestHistoriesClientGivingUp(t *testing.T) {
	store := newSlowStore(bars()...)
	store.release = make(chan struct{})
	histories := handler.NewHistories(store, clock)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := histories.Load(ctx, 3); !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want the client's own error", err)
	}
	close(store.release)
	if _, latest, err := histories.Load(t.Context(), 3); err != nil || !latest.Equal(day) || store.loads.Load() != 1 {
		t.Errorf("got %v, %v after %d loads, want the first load reused", latest, err, store.loads.Load())
	}
}

func TestHistoriesFailWhenTheLatestSessionFails(t *testing.T) {
	store := newSlowStore(bars()...)
	store.ReadErr = errors.New("connection refused")
	if _, _, err := handler.NewHistories(store, clock).Load(t.Context(), 3); err == nil || store.loads.Load() != 0 {
		t.Errorf("got %v after %d loads, want the error before loading", err, store.loads.Load())
	}
}
