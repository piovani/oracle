.PHONY: sync recommend backtest test lint lint-fix

sync:
	go run ./cmd/sync $(if $(SINCE),-since $(SINCE))

recommend:
	go run ./cmd/recommend $(if $(CAPITAL),-capital $(CAPITAL)) $(if $(TOP),-top $(TOP)) $(if $(RISK),-risk $(RISK)) $(if $(FILL),-fill)

backtest:
	go run ./cmd/backtest $(if $(FROM),-from $(FROM)) $(if $(CAPITAL),-capital $(CAPITAL)) $(if $(TOP),-top $(TOP)) $(if $(RISK),-risk $(RISK)) $(if $(COST),-cost $(COST)) $(if $(RANK),-rank $(RANK)) $(if $(CSV),-csv $(CSV))

test:
	go test ./...

lint:
	golangci-lint run ./...

lint-fix:
	golangci-lint run --fix ./...
