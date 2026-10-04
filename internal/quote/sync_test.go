package quote

import (
	"slices"
	"testing"
	"time"
)

func TestFilesUntil(t *testing.T) {
	day := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)

	got := filesUntil(2024, day)
	want := []string{
		"COTAHIST_A2024.ZIP",
		"COTAHIST_A2025.ZIP",
		"COTAHIST_M012026.ZIP",
		"COTAHIST_M022026.ZIP",
		"COTAHIST_D02032026.ZIP",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}

	if got := filesUntil(2027, day); len(got) != 0 {
		t.Errorf("future year: got %v", got)
	}
}
