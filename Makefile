.PHONY: run swagger verify

CONFIG ?= $(CURDIR)/config.yaml

run:
	cd backend && GOCACHE=$(CURDIR)/.gocache go run ./cmd/grok2api --config "$(abspath $(CONFIG))" $(RUN_ARGS)

swagger:
	cd backend && GOCACHE=$(CURDIR)/.gocache go run github.com/swaggo/swag/cmd/swag@v1.16.6 init \
		-g main.go \
		-d cmd/grok2api,internal/transport/http \
		--parseInternal \
		--output docs \
		--outputTypes go,json,yaml

# Verify local patches survived a merge/deploy (markers, config drift, live limits, persona).
# Requires PowerShell (Windows) - for the Docker-based Go test suite see UPDATE.md.
verify:
	powershell -ExecutionPolicy Bypass -File tools/verify-patches.ps1 $(VERIFY_ARGS)