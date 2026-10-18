VERSION ?= 0.1.0-dev
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build run test check vet release clean

build:
	go build -ldflags "$(LDFLAGS)" -o bin/ctm-ops ./cmd/ctm-ops

run: build
	./bin/ctm-ops

test:
	go test ./...

vet:
	go vet ./...

# Validate config and every YAML command definition.
check: build
	./bin/ctm-ops --check

# Release archives for macOS and Linux (arm64 + amd64) and the Homebrew formula.
#   make release VERSION=0.2.0
release:
	scripts/package.sh $(VERSION) dist
	scripts/formula.sh $(VERSION) dist/checksums.txt > dist/aws-dev-ops-utils.rb
	@echo "formula written to dist/aws-dev-ops-utils.rb"

clean:
	rm -rf bin dist
