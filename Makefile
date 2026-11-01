.PHONY: sync recommend backtest test test-db test-db-down test-integration lint lint-fix docker-build

sync:
	go run ./cmd/oracle sync $(if $(SINCE),-since $(SINCE))

recommend:
	go run ./cmd/oracle recommend $(if $(CAPITAL),-capital $(CAPITAL)) $(if $(TOP),-top $(TOP)) $(if $(RISK),-risk $(RISK)) $(if $(FILL),-fill)

backtest:
	go run ./cmd/oracle backtest $(if $(FROM),-from $(FROM)) $(if $(CAPITAL),-capital $(CAPITAL)) $(if $(TOP),-top $(TOP)) $(if $(RISK),-risk $(RISK)) $(if $(COST),-cost $(COST)) $(if $(RANK),-rank $(RANK)) $(if $(CSV),-csv $(CSV))

test:
	go test ./...

# Postgres for the integration tests (postgres-test in docker-compose.yml), apart from the one with your data.
test-db:
	docker compose up -d --wait postgres-test

test-db-down:
	docker compose rm -sf postgres-test

# Starts the test database if needed; the same target runs in CI.
test-integration: test-db
	go test -tags integration -race -count=1 ./test/integration/...

lint:
	golangci-lint run ./...

lint-fix:
	golangci-lint run --fix ./...

docker-build:
	docker build --platform linux/arm64 -t oracle .
