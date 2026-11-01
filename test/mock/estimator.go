package mock

import (
	"context"

	"oracle/internal/yahoo"
)

// Estimator answers analyst estimates from memory. A ticker missing from Estimates has no coverage.
type Estimator struct {
	Estimates map[string]yahoo.Estimate
	Err       map[string]error

	Requested []string // tickers asked for, in order
}

func (e *Estimator) Estimate(_ context.Context, ticker string) (yahoo.Estimate, error) {
	e.Requested = append(e.Requested, ticker)
	if err := e.Err[ticker]; err != nil {
		return yahoo.Estimate{}, err
	}
	estimate, ok := e.Estimates[ticker]
	if !ok {
		return yahoo.Estimate{}, yahoo.ErrNoCoverage
	}
	return estimate, nil
}
