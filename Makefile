.PHONY: build test lint dev ui-install ui-dev ui-build ui-test test-all

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

# Combined
test-all: test ui-test

dev: build
	@echo "Start Go server: ./bin/switchframe"
	@echo "Start UI dev server: cd ui && npm run dev"
	@echo "UI proxies /api to Go server"
