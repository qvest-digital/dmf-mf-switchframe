.PHONY: build test lint dev ui-install ui-dev ui-build ui-test ui-e2e test-all sync-prism-ts

# Go server
build:
	cd server && go build -o ../bin/switchframe ./cmd/switchframe

test:
	cd server && go test ./... -v -race

lint:
	cd server && golangci-lint run ./...

# Frontend
ui-install:
	cd ui && npm install

ui-dev:
	cd ui && npm run dev

ui-build:
	cd ui && npm run build

ui-test:
	cd ui && npx vitest run

ui-e2e:
	cd ui && npx playwright test

# Combined
test-all: test ui-test ui-e2e

dev: build
	@echo "Start Go server: ./bin/switchframe"
	@echo "Start UI dev server: cd ui && npm run dev"
	@echo "UI proxies /api to Go server"

# Prism TS vendor sync
PRISM_TS_SRC ?= ../prism/web/src
PRISM_TS_DST := ui/src/lib/prism

sync-prism-ts:
	@if [ ! -d "$(PRISM_TS_SRC)" ]; then \
		echo "Error: Prism source not found at $(PRISM_TS_SRC)"; \
		echo "Set PRISM_TS_SRC to the Prism web/src directory"; \
		exit 1; \
	fi
	@echo "Diffing vendored Prism TS against $(PRISM_TS_SRC)..."
	@diff -rq "$(PRISM_TS_SRC)" "$(PRISM_TS_DST)" \
		--exclude="main.ts" --exclude="lib.ts" --exclude="index.ts" \
		|| echo "\nFiles differ. Review changes and copy manually if needed."
