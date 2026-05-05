VERSION ?= 0.0.1-dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
  -X github.com/nklisch/agentbox/internal/version.Version=$(VERSION) \
  -X github.com/nklisch/agentbox/internal/version.Commit=$(COMMIT) \
  -X github.com/nklisch/agentbox/internal/version.Date=$(DATE)

.PHONY: build test vet install clean
build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o agentbox ./cmd/agentbox
test:
	go test ./...
vet:
	go vet ./...
install: build
	install -m 0755 agentbox $(HOME)/.local/bin/agentbox
clean:
	rm -f agentbox
