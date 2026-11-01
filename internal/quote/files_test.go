package quote

import (
	"slices"
	"testing"
	"time"

	"oracle/internal/b3"
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

func TestFilesUntilCalendar(t *testing.T) {
	date := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
	tests := []struct {
		name  string
		since int
		day   time.Time
		want  []string
	}{
		{"before the first year B3 publishes", 1900, date(1986, 1, 1), []string{"COTAHIST_D01011986.ZIP"}},
		{"january has no monthly files", 2026, date(2026, 1, 5), []string{"COTAHIST_D01012026.ZIP", "COTAHIST_D02012026.ZIP", "COTAHIST_D05012026.ZIP"}},
		{"month starting on a weekend", 2026, date(2026, 11, 2), append(months(2026, 10), "COTAHIST_D02112026.ZIP")},
		{"today on a weekend", 2026, date(2026, 10, 31), append(months(2026, 9), days(2026, time.October, 1, 2, 5, 6, 7, 8, 9, 12, 13, 14, 15, 16, 19, 20, 21, 22, 23, 26, 27, 28, 29, 30)...)},
		{"first day of the year on a weekend", 2028, date(2028, 1, 1), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := filesUntil(tt.since, tt.day); !slices.Equal(got, tt.want) {
				t.Errorf("got  %v\nwant %v", got, tt.want)
			}
		})
	}
}

func TestFilesUntilStartsAtFirstYear(t *testing.T) {
	got := filesUntil(0, time.Date(1988, 1, 1, 0, 0, 0, 0, time.UTC))
	if len(got) < 2 || got[0] != "COTAHIST_A1986.ZIP" || got[1] != "COTAHIST_A1987.ZIP" {
		t.Errorf("got %v", got)
	}
}

// months lists the monthly files from January to the given month.
func months(year int, until time.Month) []string {
	var files []string
	for m := time.January; m <= until; m++ {
		files = append(files, b3.MonthlyFile(year, m))
	}
	return files
}

func days(year int, month time.Month, list ...int) []string {
	files := make([]string, len(list))
	for i, d := range list {
		files[i] = b3.DailyFile(time.Date(year, month, d, 0, 0, 0, 0, time.UTC))
	}
	return files
}

func TestToday(t *testing.T) {
	got := today()
	if got.Location() != time.UTC || got.Hour() != 0 || got.Minute() != 0 || got.Second() != 0 || got.Nanosecond() != 0 {
		t.Errorf("got %v, want midnight UTC", got)
	}
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Skip("time zone database not available")
	}
	now := time.Now().In(loc)
	if y, m, d := now.Date(); got.Year() != y || got.Month() != m || got.Day() != d {
		t.Errorf("got %v, want the date in São Paulo %v", got, now)
	}
}

func TestTodayUsesSaoPaulo(t *testing.T) {
	orig := now
	t.Cleanup(func() { now = orig })
	// 01:00 UTC is still the previous day in São Paulo (UTC-3).
	now = func() time.Time { return time.Date(2026, 3, 3, 1, 0, 0, 0, time.UTC) }
	if got, want := today(), time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
