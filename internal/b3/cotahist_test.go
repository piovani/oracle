package b3

import (
	"strings"
	"testing"
	"time"
)

const sample = "00COTAHIST.2026BOVESPA 20261002\n" +
	"012026100202COGN3       010COGNA ON    ON      NM   R$  000000000023800000000002450000000000235000000000024000000000002430000000000243000000000024429896000000000044274400000000010626183600000000000000009999123100000010000000000000BRCOGNACNOR2112\n" +
	"012026100296COGN3F      020COGNA ON    ON      NM   R$  000000000023800000000002450000000000235000000000024000000000002430000000000243000000000024429896000000000044274400000000010626183600000000000000009999123100000010000000000000BRCOGNACNOR2112\n" +
	"99COTAHIST.2026BOVESPA 2026100200000000004\n"

func TestParse(t *testing.T) {
	var got []Quote
	err := Parse(strings.NewReader(sample), func(q Quote) error {
		got = append(got, q)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d quotes, want 1", len(got))
	}

	want := Quote{
		Date:     time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC),
		Ticker:   "COGN3",
		Name:     "COGNA ON",
		Spec:     "ON      NM",
		ISIN:     "BRCOGNACNOR2",
		Open:     2.38,
		High:     2.45,
		Low:      2.35,
		Average:  2.40,
		Close:    2.43,
		Trades:   29896,
		Quantity: 44274400,
		Volume:   106261836.00,
	}
	if got[0] != want {
		t.Errorf("got  %+v\nwant %+v", got[0], want)
	}
}

func TestFileNames(t *testing.T) {
	day := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	if got := AnnualFile(2025); got != "COTAHIST_A2025.ZIP" {
		t.Error(got)
	}
	if got := MonthlyFile(2026, time.September); got != "COTAHIST_M092026.ZIP" {
		t.Error(got)
	}
	if got := DailyFile(day); got != "COTAHIST_D02102026.ZIP" {
		t.Error(got)
	}
}
