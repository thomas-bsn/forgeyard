.PHONY: web build run dev-web test clean generate

# The commit the binaries are built from, which agents compare with the server's.
LDFLAGS := -X github.com/thomas-bsn/forgeyard/internal/version.Commit=$(shell git rev-parse HEAD 2>/dev/null)

web:
	cd web && npm ci && npm run build

build: web
	go build -ldflags "$(LDFLAGS)" -o bin/forgeyard-server ./cmd/server
	go build -ldflags "$(LDFLAGS)" -o bin/forgeyard-agent ./cmd/agent

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

# Regenerate Go code from proto/ and SQL queries (tools are installed in bin/tools).
generate:
	GOBIN=$(CURDIR)/bin/tools go install github.com/bufbuild/buf/cmd/buf@v1.50.0
	GOBIN=$(CURDIR)/bin/tools go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.5
	GOBIN=$(CURDIR)/bin/tools go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
	bin/tools/buf generate
	go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.29.0 generate
