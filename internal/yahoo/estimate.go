package yahoo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

const (
	cookieURL = "https://fc.yahoo.com"
	baseURL   = "https://query2.finance.yahoo.com"
	userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4 Safari/605.1.15"
)

var ErrNoCoverage = errors.New("yahoo: no analyst coverage")

type Estimate struct {
	Target   float64
	Rating   float64
	Analysts int
}

type Client struct {
	HTTP  *http.Client
	crumb string
}

func NewClient(ctx context.Context) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	c := &Client{HTTP: &http.Client{Timeout: 20 * time.Second, Jar: jar}}

	if _, _, err := c.get(ctx, cookieURL); err != nil {
		return nil, err
	}
	status, body, err := c.get(ctx, baseURL+"/v1/test/getcrumb")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("yahoo: crumb: unexpected status %d", status)
	}
	c.crumb = strings.TrimSpace(string(body))
	return c, nil
}

func (c *Client) Estimate(ctx context.Context, ticker string) (Estimate, error) {
	endpoint := fmt.Sprintf("%s/v10/finance/quoteSummary/%s.SA?modules=financialData&crumb=%s",
		baseURL, url.PathEscape(ticker), url.QueryEscape(c.crumb))

	status, body, err := c.get(ctx, endpoint)
	if err != nil {
		return Estimate{}, err
	}
	if status == http.StatusNotFound {
		return Estimate{}, ErrNoCoverage
	}
	if status != http.StatusOK {
		return Estimate{}, fmt.Errorf("yahoo: %s: unexpected status %d", ticker, status)
	}

	var payload struct {
		QuoteSummary struct {
			Result []struct {
				FinancialData struct {
					TargetMeanPrice         raw `json:"targetMeanPrice"`
					RecommendationMean      raw `json:"recommendationMean"`
					NumberOfAnalystOpinions raw `json:"numberOfAnalystOpinions"`
				} `json:"financialData"`
			} `json:"result"`
		} `json:"quoteSummary"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return Estimate{}, fmt.Errorf("yahoo: %s: %w", ticker, err)
	}
	if len(payload.QuoteSummary.Result) == 0 {
		return Estimate{}, ErrNoCoverage
	}

	data := payload.QuoteSummary.Result[0].FinancialData
	if data.TargetMeanPrice.Raw <= 0 {
		return Estimate{}, ErrNoCoverage
	}
	return Estimate{
		Target:   data.TargetMeanPrice.Raw,
		Rating:   data.RecommendationMean.Raw,
		Analysts: int(data.NumberOfAnalystOpinions.Raw),
	}, nil
}

func (c *Client) get(ctx context.Context, endpoint string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, body, nil
}

type raw struct {
	Raw float64 `json:"raw"`
}
