.PHONY: sync recommend

sync:
	go run ./cmd/sync $(if $(SINCE),-since $(SINCE))

recommend:
	go run ./cmd/recommend $(if $(CAPITAL),-capital $(CAPITAL)) $(if $(TOP),-top $(TOP)) $(if $(RISK),-risk $(RISK)) $(if $(STRICT),-strict)
