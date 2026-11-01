// Command seed recreates the oracle_postman database in postgres-test with a few quotes,
// so the API answers realistic examples for the Postman collection. It is run by api.py.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"oracle/config"
	"oracle/internal/quote"
	"oracle/pkg/db"
	"oracle/test/fixture"
)

const name = "oracle_postman"

func main() {
	server := config.DBConfig{Host: "localhost", Port: "5433", User: "oracle_test", Password: "oracle_test", Name: "oracle_test", SSLMode: "disable"}
	admin, err := db.Connect(server)
	if err != nil {
		log.Fatal(err)
	}
	for _, stmt := range []string{"DROP DATABASE IF EXISTS " + name + " WITH (FORCE)", "CREATE DATABASE " + name} {
		if err := admin.Exec(stmt).Error; err != nil {
			log.Fatal(err)
		}
	}

	server.Name = name
	conn, err := db.Connect(server)
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	store := quote.NewPostgres(conn)
	if err := store.Migrate(ctx); err != nil {
		log.Fatal(err)
	}
	n, err := store.SaveFile(ctx, "POSTMAN_SAMPLE", func(save func(quote.Quote) error) error {
		for _, q := range sample() {
			if err := save(q); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("seeded %s with %d quotes\n", name, n)
}

func sample() []quote.Quote {
	day := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }
	bar := func(ticker, name, spec, isin string, d int, o, h, l, avg, c float64, trades, qty int64) quote.Quote {
		return quote.Quote{
			Ticker: ticker, Name: name, Spec: spec, ISIN: isin, Date: day(d),
			Open: o, High: h, Low: l, Average: avg, Close: c,
			Trades: trades, Quantity: qty, Volume: float64(qty) * avg,
		}
	}
	// SWNG3 is synthetic: 400 sessions of uptrend with 5 winning pullbacks and a "forte" signal on the last day.
	return append(fixture.Trend("SWNG3", 400, day(30), fixture.Dips(5), fixture.StrongPullback...), []quote.Quote{
		bar("PETR4", "PETROBRAS", "PN      N2", "BRPETRACNPR6", 28, 37.40, 37.95, 37.10, 37.58, 37.80, 48120, 38904100),
		bar("PETR4", "PETROBRAS", "PN      N2", "BRPETRACNPR6", 29, 37.80, 38.30, 37.55, 37.96, 38.05, 50233, 40112800),
		bar("PETR4", "PETROBRAS", "PN      N2", "BRPETRACNPR6", 30, 37.90, 38.45, 37.62, 38.10, 38.20, 52310, 41250300),
		bar("VALE3", "VALE", "ON      NM", "BRVALEACNOR0", 28, 61.20, 61.85, 60.70, 61.30, 61.55, 39870, 21004500),
		bar("VALE3", "VALE", "ON      NM", "BRVALEACNOR0", 29, 61.50, 62.10, 61.05, 61.62, 61.90, 41022, 22310700),
		bar("VALE3", "VALE", "ON      NM", "BRVALEACNOR0", 30, 61.95, 62.40, 61.30, 61.88, 62.00, 40115, 20987600),
		bar("XPML11", "XP MALLS", "CI", "BRXPMLCTF000", 30, 104.10, 104.80, 103.70, 104.25, 104.50, 6210, 98340),
	}...)
}
