.PHONY: sync recommend backtest test test-integration lint lint-fix docker-build

sync:
	go run ./cmd/oracle sync $(if $(SINCE),-since $(SINCE))

recommend:
	go run ./cmd/oracle recommend $(if $(CAPITAL),-capital $(CAPITAL)) $(if $(TOP),-top $(TOP)) $(if $(RISK),-risk $(RISK)) $(if $(FILL),-fill)

backtest:
	go run ./cmd/oracle backtest $(if $(FROM),-from $(FROM)) $(if $(CAPITAL),-capital $(CAPITAL)) $(if $(TOP),-top $(TOP)) $(if $(RISK),-risk $(RISK)) $(if $(COST),-cost $(COST)) $(if $(RANK),-rank $(RANK)) $(if $(CSV),-csv $(CSV))

test:
	go test ./...

# Needs Postgres from docker-compose.yml; every test creates and drops its own database.
test-integration:
	go test -tags integration -count=1 ./test/integration/...

lint:
	golangci-lint run ./...

lint-fix:
	golangci-lint run --fix ./...

docker-build:
	docker build --platform linux/arm64 -t oracle .
