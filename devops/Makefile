VERSION ?= 0.1.0-dev
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build run test check vet release clean

build:
	go build -ldflags "$(LDFLAGS)" -o bin/devops ./cmd/devops

run: build
	./bin/devops

test:
	go test ./...

vet:
	go vet ./...

# Validate config and every YAML command definition.
check: build
	./bin/devops --check

# Static binaries for macOS (first) and Linux; no CGO needed (pure-Go SQLite).
release:
	@mkdir -p dist
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/devops-darwin-arm64 ./cmd/devops
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/devops-darwin-amd64 ./cmd/devops
	CGO_ENABLED=0 GOOS=linux  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/devops-linux-amd64 ./cmd/devops
	CGO_ENABLED=0 GOOS=linux  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/devops-linux-arm64 ./cmd/devops

clean:
	rm -rf bin dist
