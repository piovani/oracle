.PHONY: sync recommend backtest test test-db test-db-down test-integration coverage swagger lint lint-fix docker-build

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

SWAG_VERSION := v1.16.6

swagger:
	go run github.com/swaggo/swag/cmd/swag@$(SWAG_VERSION) init -g api.go -d internal/command,internal/handler -o docs --outputTypes go,json,yaml
	go run github.com/swaggo/swag/cmd/swag@$(SWAG_VERSION) fmt -g api.go -d internal/command,internal/handler

lint:
	golangci-lint run ./...

lint-fix:
	golangci-lint run --fix ./...

docker-build:
	docker build --platform linux/arm64 -t oracle .
