.PHONY: build build-server dev demo ui-install ui-build ui-test ui-e2e test test-all docker clean sync-prism-ts

EMBED_LINK := server/cmd/switchframe/ui

# UI build
ui-install:
	cd ui && npm ci

ui-build: ui-install
	cd ui && npm run build

ui-test:
	cd ui && npx vitest run

ui-e2e:
	cd ui && npx playwright test

# Symlink for go:embed
$(EMBED_LINK): ui-build
	@ln -sfn ../../../ui/build $(EMBED_LINK)

# Production build: UI + embedded Go binary
build: $(EMBED_LINK)
	cd server && go build -tags embed_ui -o ../bin/switchframe ./cmd/switchframe

# Dev build: Go only (no UI embed)
build-server:
	cd server && go build -o ../bin/switchframe ./cmd/switchframe

# Go tests
test:
	cd server && go test ./... -race

# Auto-install UI deps if missing
node_modules_check:
	@test -d ui/node_modules || (echo "Installing UI dependencies..." && cd ui && npm ci)

# Dev mode: start both servers
dev: build-server node_modules_check
	@trap 'kill 0' EXIT; \
		./bin/switchframe & \
		cd ui && npm run dev & \
		wait

# Demo mode: start with 4 simulated cameras
demo: build-server node_modules_check
	@echo ""
	@echo "  SwitchFrame Demo"
	@echo "  Open http://localhost:5173 in your browser"
	@echo "  Press Ctrl+C to stop"
	@echo ""
	@trap 'kill 0' EXIT; \
		./bin/switchframe --demo & \
		cd ui && npm run dev & \
		wait

# All tests
test-all: test ui-test ui-e2e

# Docker
docker:
	docker build -t switchframe .

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

clean:
	rm -rf bin/ $(EMBED_LINK)
