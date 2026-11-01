package quote_test

import (
	"testing"

	"oracle/internal/quote"
	"oracle/test/fixture"
)

func TestLastSession(t *testing.T) {
	last := fixture.Day(2026, 10, 30)
	history := map[string][]quote.Quote{
		"NEW3": fixture.Trend("NEW3", 3, last, nil),
		"OLD3": fixture.Trend("OLD3", 3, last.AddDate(0, 0, -10), nil),
		"NIL3": nil,
	}
	if got := quote.LastSession(history); !got.Equal(last) {
		t.Errorf("got %v, want %v", got, last)
	}
	if got := quote.LastSession(nil); !got.IsZero() {
		t.Errorf("empty history: got %v", got)
	}
}

func TestIsStock(t *testing.T) {
	for spec, want := range map[string]bool{
		"ON      NM": true, "PN      N2": true, "PNA     N1": true, "UNT     N2": true,
		"CI": false, "CI  ER": false, "DRN": false, "": false,
	} {
		if got := quote.IsStock(spec); got != want {
			t.Errorf("IsStock(%q) = %v, want %v", spec, got, want)
		}
	}
}
