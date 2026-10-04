.PHONY: sync

sync:
	go run ./cmd/sync $(if $(SINCE),-since $(SINCE))
