.PHONY: build test lint dev ui-install ui-dev ui-build ui-test

build:
	cd server && go build -o ../bin/switchframe ./cmd/switchframe

test:
	cd server && go test ./... -v -race

lint:
	cd server && golangci-lint run ./...

dev: build
	./bin/switchframe

ui-install:
	cd ui && npm install

ui-dev:
	cd ui && npm run dev

ui-build:
	cd ui && npm run build

ui-test:
	cd ui && npx vitest run
