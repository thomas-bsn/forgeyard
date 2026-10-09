.PHONY: web build run dev-web test clean

web:
	cd web && npm ci && npm run build

build: web
	go build -o bin/forgeyard-server ./cmd/server
	go build -o bin/forgeyard-agent ./cmd/agent

run: build
	./bin/forgeyard-server

# Go server on :8080 in one terminal (`go run ./cmd/server`), Vite with hot reload in another.
dev-web:
	cd web && npm run dev

test:
	go test ./...

clean:
	rm -rf bin
	find web/dist -mindepth 1 ! -name .gitkeep -delete
