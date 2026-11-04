.PHONY: monitoring monitoring-down sync recommend backtest test test-db test-db-down test-integration coverage load-test swag lint lint-fix docker-build

sync:
	go run ./cmd/oracle sync $(if $(SINCE),-since $(SINCE))

recommend:
	go run ./cmd/oracle recommend $(if $(CAPITAL),-capital $(CAPITAL)) $(if $(TOP),-top $(TOP)) $(if $(RISK),-risk $(RISK)) $(if $(FILL),-fill)

backtest:
	go run ./cmd/oracle backtest $(if $(FROM),-from $(FROM)) $(if $(CAPITAL),-capital $(CAPITAL)) $(if $(TOP),-top $(TOP)) $(if $(RISK),-risk $(RISK)) $(if $(COST),-cost $(COST)) $(if $(RANK),-rank $(RANK)) $(if $(CSV),-csv $(CSV))

test:
	go test ./...

# Postgres and Redis for the integration tests (postgres-test and redis-test in docker-compose.yml),
# apart from the ones with your data.
test-db:
	docker compose up -d --wait postgres-test redis-test

test-db-down:
	docker compose rm -sf postgres-test redis-test

# Starts the test database if needed; the same target runs in CI.
test-integration: test-db
	go test -tags integration -race -count=1 ./test/integration/...

# Unit and integration tests, plus the compiled binary the CLI tests run, merged into coverage.out.
# Fails below COVERAGE_MIN percent; the same target runs in CI.
COVERAGE_MIN ?= 90
COVERDIR := $(CURDIR)/.cover
COVERPKG = $(shell go list ./... | grep -v /test/ | paste -sd, -)

coverage: test-db
	rm -rf $(COVERDIR) && mkdir -p $(COVERDIR)
	COVERDIR=$(COVERDIR) go test -tags integration -race -count=1 -coverpkg=$(COVERPKG) ./... -args -test.gocoverdir=$(COVERDIR)
	go tool covdata textfmt -i=$(COVERDIR) -pkg=$(COVERPKG) -o coverage.out
	@go tool cover -func=coverage.out | awk -v min=$(COVERAGE_MIN) '/^total:/ { sub("%", "", $$3); \
		printf "total coverage %s%% (minimum %s%%)\n", $$3, min; exit ($$3 + 0 < min + 0) }'

# k6 load tests against a running API (go run ./cmd/oracle api). Never run by `make test`, `make coverage` or the CI.
PROFILE ?= load

load-test:
	@command -v k6 >/dev/null || { echo "k6 not found: https://grafana.com/docs/k6/latest/set-up/install-k6/"; exit 1; }
	k6 run $(if $(BASE_URL),-e BASE_URL=$(BASE_URL)) $(if $(TICKER),-e TICKER=$(TICKER)) tests/load/$(PROFILE).js

# Prometheus, Grafana, Loki and Tempo (see docker-compose.yml). Grafana: http://localhost:3000 (admin/admin).
monitoring:
	docker compose --profile monitoring up -d --wait

monitoring-down:
	docker compose --profile monitoring stop prometheus grafana loki tempo postgres-exporter redis-exporter
	docker compose --profile monitoring rm -f prometheus grafana loki tempo postgres-exporter redis-exporter

SWAG_VERSION := v1.16.6

swag:
	go run github.com/swaggo/swag/cmd/swag@$(SWAG_VERSION) init -g api.go -d internal/command,internal/handler -o docs --outputTypes go,json,yaml
	go run github.com/swaggo/swag/cmd/swag@$(SWAG_VERSION) fmt -g api.go -d internal/command,internal/handler

lint:
	golangci-lint run ./...

lint-fix:
	golangci-lint run --fix ./...

docker-build:
	docker build --platform linux/arm64 -t oracle .
