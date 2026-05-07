VERSION ?= 0.0.1-dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
  -X github.com/nklisch/agentbox/internal/version.Version=$(VERSION) \
  -X github.com/nklisch/agentbox/internal/version.Commit=$(COMMIT) \
  -X github.com/nklisch/agentbox/internal/version.Date=$(DATE)

.PHONY: build test test-e2e vet install clean release-check test-install
build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o agentbox ./cmd/agentbox
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o agentbox-netfilter ./cmd/agentbox-netfilter
test:
	go test ./...
# test-e2e exercises the artifacts agentbox writes to disk against their real
# consumer binaries (zellij parses the layout.kdl; jq parses the
# claude-settings.json). The TestReal* tests skip cleanly when those binaries
# are absent, so this target just runs the same suite with -count=1 to bypass
# the cache and a clear PASS/SKIP signal in stdout. Install zellij locally
# (`brew install zellij` / cargo install) to exercise the KDL path.
test-e2e:
	go test -count=1 -v -run "Real(Zellij|Parser)" ./internal/zellij/ ./internal/lifecycle/
vet:
	go vet ./...
install: build
	install -m 0755 agentbox $(HOME)/.local/bin/agentbox
	install -m 0755 agentbox-netfilter $(HOME)/.local/bin/agentbox-netfilter
clean:
	rm -f agentbox agentbox-netfilter
release-check:
	goreleaser check
	goreleaser release --snapshot --clean --skip=publish
test-install:
	sh scripts/test/install_test.sh
