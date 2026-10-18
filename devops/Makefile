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

# Release archives for macOS and Linux (arm64 + amd64) and the Homebrew formula.
#   make release VERSION=0.2.0
release:
	scripts/package.sh $(VERSION) dist
	scripts/formula.sh $(VERSION) dist/checksums.txt > dist/devops.rb
	@echo "formula written to dist/devops.rb"

clean:
	rm -rf bin dist
