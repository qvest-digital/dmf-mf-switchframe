.PHONY: build build-server build-server-mxl dev demo mxl-demo ui-install ui-build ui-test ui-e2e test test-mxl test-all docker clean sync-prism-ts lint format setup-mkcert

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

# Demo mode: 4 H.264 cameras + 2 raw MXL sources (exercises full pipeline)
demo: build-server node_modules_check
	@echo ""
	@echo "  SwitchFrame Demo"
	@echo "  Open http://localhost:5173 in your browser"
	@echo "  Sources: cam1-cam4 (H.264), mxl:raw1-raw2 (raw YUV), SRT push (if test clips exist)"
	@echo "  Keying: mxl:raw2 is a green screen with chroma key pre-configured (Keys tab)"
	@echo "  SCTE-35: Enabled (SCTE-35 tab in bottom panel)"
	@echo "  Captions: Enabled (Captions tab in bottom panel)"
	@echo "  Press Ctrl+C to stop"
	@echo ""
	@trap 'kill 0' EXIT; \
		CERT_FLAGS="--http-fallback"; \
		echo "  Self-signed cert (WebTransport draft-02 + TCP fallback for Vite proxy)"; \
		echo ""; \
		if [ -d test/clips ]; then \
			./bin/switchframe --demo --demo-video test/clips --srt-listen :6464 --format 1080p24 --frame-sync --frc-quality mcfi --scte35 --captions $$CERT_FLAGS & \
		else \
			./bin/switchframe --demo --srt-listen :6464 --format 1080p24 --frame-sync --frc-quality mcfi --scte35 --captions $$CERT_FLAGS & \
		fi; \
		cd ui && npm run dev & \
		wait

# Generate mkcert certificate for trusted HTTP/3 dev mode
setup-mkcert:
	@command -v mkcert >/dev/null 2>&1 || { echo "Install mkcert first: brew install mkcert"; exit 1; }
	mkcert -install
	mkdir -p $(HOME)/.switchframe
	mkcert -cert-file $(HOME)/.switchframe/cert.pem -key-file $(HOME)/.switchframe/key.pem localhost 127.0.0.1 ::1
	@echo "Done! mkcert certificate generated at ~/.switchframe/"
	@echo "Run 'make demo' to use HTTP/3 dev mode"

# Build with MXL SDK support (requires MXL_ROOT env var)
build-server-mxl:
	@test -n "$${MXL_ROOT}" || { echo "ERROR: MXL_ROOT not set. Export it to your MXL SDK install directory."; exit 1; }
	cd server && PKG_CONFIG_PATH="$${MXL_ROOT}/lib/pkgconfig$${PKG_CONFIG_PATH:+:$$PKG_CONFIG_PATH}" \
		go build -tags "cgo mxl" -o ../bin/switchframe ./cmd/switchframe

# MXL demo: real MXL SDK + GStreamer test sources through shared memory
mxl-demo: node_modules_check
	@bash scripts/mxl-demo.sh

# MXL pipeline integration tests
test-mxl:
	cd server && go test ./mxl/ -v -race -run "TestPipeline|TestV210RoundTrip"

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

# Lint
lint: node_modules_check
	cd server && go vet ./...
	cd ui && npx svelte-check --tsconfig ./tsconfig.json

# Format
format:
	cd server && gofmt -w .
	cd ui && npx prettier --write 'src/**/*.{ts,svelte,css}'

clean:
	rm -rf bin/ $(EMBED_LINK)
