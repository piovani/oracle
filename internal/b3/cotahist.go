package b3

import (
	"archive/zip"
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const baseURL = "https://bvmf.bmfbovespa.com.br/InstDados/SerHist"

// FirstYear is the oldest year B3 publishes.
const FirstYear = 1986

// ErrNotFound means B3 has not published the requested file (holiday,
// weekend or session not closed yet).
var ErrNotFound = errors.New("b3: file not found")

const (
	recordLen      = 245
	bdiRoundLot    = "02"
	marketCash     = "010"
	recordTypeData = "01"
)

// Quote is one trading day of one stock in the cash market.
type Quote struct {
	Date     time.Time
	Ticker   string
	Name     string
	Spec     string
	ISIN     string
	Open     float64
	High     float64
	Low      float64
	Average  float64
	Close    float64
	Trades   int64
	Quantity int64
	Volume   float64
}

func AnnualFile(year int) string {
	return fmt.Sprintf("COTAHIST_A%04d.ZIP", year)
}

func MonthlyFile(year int, month time.Month) string {
	return fmt.Sprintf("COTAHIST_M%02d%04d.ZIP", month, year)
}

func DailyFile(day time.Time) string {
	return fmt.Sprintf("COTAHIST_D%s.ZIP", day.Format("02012006"))
}

type Client struct {
	HTTP    *http.Client
	BaseURL string
}

func NewClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: 15 * time.Minute}, BaseURL: baseURL}
}

// Fetch downloads the given COTAHIST file and calls fn for every stock quote
// in it. It returns ErrNotFound when the file does not exist.
func (c *Client) Fetch(ctx context.Context, file string, fn func(Quote) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/"+file, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("b3: %s: unexpected status %s", file, resp.Status)
	}

	// zip needs random access, so the archive goes to disk first.
	tmp, err := os.CreateTemp("", "cotahist-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	size, err := io.Copy(tmp, resp.Body)
	if err != nil {
		return fmt.Errorf("b3: %s: download: %w", file, err)
	}

	zr, err := zip.NewReader(tmp, size)
	if err != nil {
		return fmt.Errorf("b3: %s: %w", file, err)
	}
	for _, zf := range zr.File {
		rc, err := zf.Open()
		if err != nil {
			return fmt.Errorf("b3: %s: %w", file, err)
		}
		err = Parse(rc, fn)
		rc.Close()
		if err != nil {
			return fmt.Errorf("b3: %s: %w", file, err)
		}
	}
	return nil
}

// Parse reads a COTAHIST text stream and calls fn for every round-lot quote
// of the cash market. Options, forwards, odd lots, funds and the like are
// skipped.
func Parse(r io.Reader, fn func(Quote) error) error {
	sc := bufio.NewScanner(r)
	for line := 1; sc.Scan(); line++ {
		rec := sc.Bytes()
		if len(rec) < recordLen || string(rec[0:2]) != recordTypeData {
			continue
		}
		if string(rec[10:12]) != bdiRoundLot || string(rec[24:27]) != marketCash {
			continue
		}
		q, err := parseRecord(rec)
		if err != nil {
			return fmt.Errorf("line %d: %w", line, err)
		}
		if err := fn(q); err != nil {
			return err
		}
	}
	return sc.Err()
}

func parseRecord(rec []byte) (Quote, error) {
	var (
		q   Quote
		err error
	)
	if q.Date, err = time.Parse("20060102", string(rec[2:10])); err != nil {
		return q, err
	}
	q.Ticker = text(rec[12:24])
	q.Name = text(rec[27:39])
	q.Spec = text(rec[39:49])
	q.ISIN = text(rec[230:242])

	if q.Open, err = price(rec[56:69]); err != nil {
		return q, err
	}
	if q.High, err = price(rec[69:82]); err != nil {
		return q, err
	}
	if q.Low, err = price(rec[82:95]); err != nil {
		return q, err
	}
	if q.Average, err = price(rec[95:108]); err != nil {
		return q, err
	}
	if q.Close, err = price(rec[108:121]); err != nil {
		return q, err
	}
	if q.Trades, err = integer(rec[147:152]); err != nil {
		return q, err
	}
	if q.Quantity, err = integer(rec[152:170]); err != nil {
		return q, err
	}
	if q.Volume, err = price(rec[170:188]); err != nil {
		return q, err
	}
	return q, nil
}

// text trims the field and converts it from ISO-8859-1 to UTF-8.
func text(b []byte) string {
	runes := make([]rune, len(b))
	for i, c := range b {
		runes[i] = rune(c)
	}
	return strings.TrimSpace(string(runes))
}

func integer(b []byte) (int64, error) {
	return strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
}

// price parses a numeric field with two implied decimal places.
func price(b []byte) (float64, error) {
	n, err := integer(b)
	if err != nil {
		return 0, err
	}
	return float64(n) / 100, nil
}
