.PHONY: build build-server build-server-mxl build-asr-sidecar build-asr-sidecar-whisper dev demo demo-captions demo-cef demo-advanced cef-sidecar mxl-demo ui-install ui-build ui-test ui-e2e test test-mxl test-all docker docker-cuda clean sync-prism-ts lint format setup-mkcert metal-shaders asr-eval-fixtures asr-eval

EMBED_LINK := server/cmd/switchframe/ui

# Suppress macOS linker warnings about duplicate FFmpeg libraries
# (multiple cgo packages link the same libs; harmless but noisy)
UNAME_S := $(shell uname -s)
ifeq ($(UNAME_S),Darwin)
  export CGO_LDFLAGS += -Wl,-no_warn_duplicate_libraries
endif

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

# Metal shader compilation (macOS only, no-op on Linux)
metal-shaders:
	@if [ "$$(uname)" = "Darwin" ] && command -v xcrun >/dev/null 2>&1; then \
		cd server/gpu/metal && $(MAKE) -s 2>/dev/null || true; \
	fi

# Production build: UI + embedded Go binary
build: $(EMBED_LINK) metal-shaders
	cd server && go build -tags embed_ui -o ../bin/switchframe ./cmd/switchframe

# Dev build: Go only (no UI embed)
build-server: metal-shaders
	cd server && go build -o ../bin/switchframe ./cmd/switchframe

# Load test harness (in-process; no UI embed, no GPU tags by default)
loadtest:
	cd server && go build -o ../bin/loadtest ./cmd/loadtest

# ASR sidecar binary (stub backends only -- no GPU/cgo)
build-asr-sidecar:
	cd server && go build -o ../bin/switchframe-asr ./cmd/switchframe-asr

# ASR sidecar with whisper.cpp linked.
# Requires whisper.cpp built and installed at /opt/whisper.cpp.
# Platform-specific tags: Linux pulls in CUDA + TensorRT; Darwin uses Metal only.
build-asr-sidecar-whisper:
ifeq ($(UNAME_S),Darwin)
	cd server && go build -tags whisper -o ../bin/switchframe-asr ./cmd/switchframe-asr
else
	cd server && go build -tags cuda,tensorrt,whisper -o ../bin/switchframe-asr ./cmd/switchframe-asr
endif

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
# Override color format: COLOR_FORMAT=professional make demo  (HEVC 10-bit 4:2:2)
#                        COLOR_FORMAT=standard-hevc make demo (HEVC 8-bit)
demo: build-server node_modules_check
	@echo ""
	@echo "  SwitchFrame Demo (color-format: $${COLOR_FORMAT:-standard})"
	@echo "  Open http://localhost:5173 in your browser"
	@echo "  Sources: cam1-cam4 (H.264), mxl:raw1-raw2 (raw YUV), SRT push (if test clips exist)"
	@echo "  Keying: mxl:raw2 is a green screen with chroma key pre-configured (Keys tab)"
	@echo "  SCTE-35: Enabled (SCTE-35 tab in bottom panel)"
	@echo "  Captions: Enabled (Captions tab in bottom panel)"
	@echo "  Press Ctrl+C to stop"
	@echo ""
	@trap 'kill 0' EXIT; \
		CERT_FLAGS="--http-fallback"; \
		CF_FLAG=""; \
		if [ -n "$${COLOR_FORMAT}" ]; then CF_FLAG="--color-format $${COLOR_FORMAT}"; fi; \
		echo "  Self-signed cert (WebTransport draft-02 + TCP fallback for Vite proxy)"; \
		echo ""; \
		if [ -d test/clips ]; then \
			./bin/switchframe --demo --demo-video test/clips --srt-listen :6464 --srt-output-ports 7464-7467 --format 1080p24 --frame-sync --frc-quality mcfi --scte35 --captions --preview-proxy $$CF_FLAG $$CERT_FLAGS & \
		else \
			./bin/switchframe --demo --srt-listen :6464 --srt-output-ports 7464-7467 --format 1080p24 --frame-sync --frc-quality mcfi --scte35 --captions --preview-proxy $$CF_FLAG $$CERT_FLAGS & \
		fi; \
		cd ui && npm run dev & \
		wait

# CEF sidecar (HTML5 graphics) bootstrap for macOS dev.
# CEF dist + libcef_dll_wrapper cache in ~/.cache/switchframe/cef/ (survives `make clean`).
CEF_APP := bin/switchframe-cef.app
CEF_BIN := $(CEF_APP)/Contents/MacOS/switchframe-cef
# Path relative to the switchframe binary's directory (bin/), since htmlgfx.Manager
# resolves non-absolute --cef-binary against filepath.Dir(os.Executable()).
CEF_BIN_REL := switchframe-cef.app/Contents/MacOS/switchframe-cef

# File-target: make only re-runs the script when the bundle is missing.
# The script itself is idempotent regardless.
$(CEF_BIN):
	@bash scripts/setup-cef-mac.sh

cef-sidecar: $(CEF_BIN)

# Demo with HTML5 graphics enabled (CEF sidecar). Bootstraps CEF on first run (~10 min).
demo-cef: build-server cef-sidecar node_modules_check
	@echo ""
	@echo "  SwitchFrame Demo + HTML5 Graphics (CEF sidecar)"
	@echo "  Open http://localhost:5173 in your browser"
	@echo "  HTML5 templates: cef-sidecar/demo-templates/ (Graphics tab)"
	@echo "  Press Ctrl+C to stop"
	@echo ""
	@trap 'kill 0' EXIT; \
		CERT_FLAGS="--http-fallback"; \
		CEF_FLAGS="--cef-binary $(CEF_BIN_REL) --cef-templates-dir cef-sidecar/demo-templates"; \
		echo "  Self-signed cert (WebTransport draft-02 + TCP fallback for Vite proxy)"; \
		echo ""; \
		if [ -d test/clips ]; then \
			./bin/switchframe --demo --demo-video test/clips --srt-listen :6464 --srt-output-ports 7464-7467 --format 1080p24 --frame-sync --frc-quality mcfi --scte35 --captions --preview-proxy $$CEF_FLAGS $$CERT_FLAGS & \
		else \
			./bin/switchframe --demo --srt-listen :6464 --srt-output-ports 7464-7467 --format 1080p24 --frame-sync --frc-quality mcfi --scte35 --captions --preview-proxy $$CEF_FLAGS $$CERT_FLAGS & \
		fi; \
		cd ui && npm run dev & \
		wait

# Generate synthetic ASR test fixtures (macOS only -- uses `say`).
asr-eval-fixtures:
	@bash scripts/asr-eval/gen-fixtures.sh

# Run the streaming ASR pipeline against the fixture corpus and print
# WER, latency, and commit-stability metrics. Requires the whisper-tag
# build path to be linkable (whisper.cpp at /opt/whisper.cpp).
ASR_EVAL_FIXTURES_DIR ?= test/asr-fixtures
ASR_EVAL_MODELS_DIR   ?= /opt/whisper.cpp/models
ASR_EVAL_MODEL        ?=
ASR_EVAL_THREADS      ?= 0
ASR_EVAL_MODE         ?= stream
asr-eval:
	@cd server && \
	  ASR_EVAL_FIXTURES_DIR="$(CURDIR)/$(ASR_EVAL_FIXTURES_DIR)" \
	  ASR_EVAL_MODELS_DIR="$(ASR_EVAL_MODELS_DIR)" \
	  ASR_EVAL_MODEL="$(ASR_EVAL_MODEL)" \
	  ASR_EVAL_THREADS="$(ASR_EVAL_THREADS)" \
	  ASR_EVAL_MODE="$(ASR_EVAL_MODE)" \
	  go test -count=1 -tags whisper -v -timeout 10m \
	    -run TestASREvalFixtures ./cmd/switchframe-asr

# Demo with live AI captions: same as demo, plus the whisper.cpp ASR sidecar.
# Requires /opt/whisper.cpp built with -DGGML_METAL=ON (macOS) or -DGGML_CUDA=ON (Linux)
# and at least one ggml-*.bin model under /opt/whisper.cpp/models.
WHISPER_MODELS_DIR ?= /opt/whisper.cpp/models
demo-captions: build-server build-asr-sidecar-whisper node_modules_check
	@echo ""
	@echo "  SwitchFrame Demo + AI Captions (whisper.cpp on $(UNAME_S))"
	@echo "  Open http://localhost:5173 in your browser"
	@echo "  Captions: live ASR via whisper sidecar (Captions tab in bottom panel)"
	@echo "  Press Ctrl+C to stop"
	@echo ""
	@trap 'kill 0' EXIT; \
		CERT_FLAGS="--http-fallback"; \
		ASR_FLAGS="--asr-sidecar ./bin/switchframe-asr --asr-whisper-models-dir $(WHISPER_MODELS_DIR) --asr-default-backend whisper"; \
		echo "  Self-signed cert (WebTransport draft-02 + TCP fallback for Vite proxy)"; \
		echo ""; \
		if [ -d test/clips ]; then \
			./bin/switchframe --demo --demo-video test/clips --srt-listen :6464 --srt-output-ports 7464-7467 --format 1080p24 --frame-sync --frc-quality mcfi --scte35 --captions --preview-proxy $$ASR_FLAGS $$CERT_FLAGS & \
		else \
			./bin/switchframe --demo --srt-listen :6464 --srt-output-ports 7464-7467 --format 1080p24 --frame-sync --frc-quality mcfi --scte35 --captions --preview-proxy $$ASR_FLAGS $$CERT_FLAGS & \
		fi; \
		cd ui && npm run dev & \
		wait

# Demo with BOTH live AI captions AND HTML5 graphics (CEF sidecar).
# Combines demo-captions + demo-cef — same switchframe binary, both sets of
# --asr-sidecar and --cef-binary flags on the command line.
# Prereqs: whisper.cpp at /opt/whisper.cpp with a ggml-*.bin model, and CEF
# bootstrapped (auto-runs scripts/setup-cef-mac.sh on first invocation, ~10 min).
demo-advanced: build-server build-asr-sidecar-whisper cef-sidecar node_modules_check
	@echo ""
	@echo "  SwitchFrame Demo + AI Captions + HTML5 Graphics"
	@echo "  Open http://localhost:5173 in your browser"
	@echo "  Captions: live ASR via whisper sidecar (Captions tab in bottom panel)"
	@echo "  HTML5 templates: cef-sidecar/demo-templates/ (Graphics tab)"
	@echo "  Press Ctrl+C to stop"
	@echo ""
	@trap 'kill 0' EXIT; \
		CERT_FLAGS="--http-fallback"; \
		ASR_FLAGS="--asr-sidecar ./bin/switchframe-asr --asr-whisper-models-dir $(WHISPER_MODELS_DIR) --asr-default-backend whisper"; \
		CEF_FLAGS="--cef-binary $(CEF_BIN_REL) --cef-templates-dir cef-sidecar/demo-templates"; \
		echo "  Self-signed cert (WebTransport draft-02 + TCP fallback for Vite proxy)"; \
		echo ""; \
		if [ -d test/clips ]; then \
			./bin/switchframe --demo --demo-video test/clips --srt-listen :6464 --srt-output-ports 7464-7467 --format 1080p24 --frame-sync --frc-quality mcfi --scte35 --captions --preview-proxy $$ASR_FLAGS $$CEF_FLAGS $$CERT_FLAGS & \
		else \
			./bin/switchframe --demo --srt-listen :6464 --srt-output-ports 7464-7467 --format 1080p24 --frame-sync --frc-quality mcfi --scte35 --captions --preview-proxy $$ASR_FLAGS $$CEF_FLAGS $$CERT_FLAGS & \
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

docker-cuda:
	docker build -f Dockerfile.cuda -t switchframe:cuda .

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
	@if [ -f server/gpu/metal/Makefile ]; then cd server/gpu/metal && $(MAKE) -s clean 2>/dev/null || true; fi

.PHONY: sf-team
sf-team:
	cd server && go build -o ../bin/sf-team ./cmd/sf-team

.PHONY: install-sf-team
install-sf-team: sf-team
	@echo "built bin/sf-team — symlink into your PATH:"
	@echo "    ln -sf $(PWD)/bin/sf-team /usr/local/bin/sf-team"

.PHONY: sf-team-test
sf-team-test:
	cd server && go test ./cmd/sf-team/ -race

.PHONY: sf-team-serve agent-ui
# Boot the sf-team agent board/chat UI (compiles then runs).
# Prints its URL + auth token on startup. Default port 7890; override with PORT=...
PORT ?= 7890
sf-team-serve agent-ui: sf-team
	@echo ""
	@echo "  sf-team agent UI — board, claims, chat"
	@echo "  Appending ?token=<token> once (printed below) stores it in localStorage."
	@echo "  Ctrl+C to stop."
	@echo ""
	./bin/sf-team serve --port $(PORT)
