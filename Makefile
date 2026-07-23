# smith (Toolsmith) — build & install
#
# Common targets:
#   make build      build ./smith for this platform
#   make install    build + install to $(PREFIX)/bin (default ~/.local/bin)
#   make cross      cross-compile release binaries into dist/
#   make test       run the test suite
#
# Go is pinned via mise.toml; commands run under `mise exec` so no separate
# Go install is required beyond mise.

PREFIX ?= $(HOME)/.local
BINDIR := $(PREFIX)/bin
SKILLS_DIR ?= $(HOME)/.claude/skills
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GO := mise exec -- go
PLATFORMS := darwin-arm64 darwin-amd64 linux-amd64 linux-arm64

.PHONY: build install uninstall cross test vet fmt clean

build:
	$(GO) build -trimpath -ldflags "-X main.version=$(VERSION)" -o smith ./cmd/smith

install: build
	@mkdir -p "$(BINDIR)"
	@install -m 0755 smith "$(BINDIR)/smith"
	@echo "installed: $(BINDIR)/smith ($(VERSION))"
	@mkdir -p "$(SKILLS_DIR)/cli-creator"
	@install -m 0644 skills/cli-creator/SKILL.md "$(SKILLS_DIR)/cli-creator/SKILL.md"
	@echo "installed skill: $(SKILLS_DIR)/cli-creator/SKILL.md"
	@case ":$$PATH:" in *":$(BINDIR):"*) ;; \
	  *) echo "note: $(BINDIR) is not on your PATH — add it: export PATH=\"$(BINDIR):\$$PATH\"";; esac
	@command -v mise >/dev/null 2>&1 || echo "note: mise not found on PATH — smith needs it (https://mise.jdx.dev)"

uninstall:
	@rm -f "$(BINDIR)/smith" && echo "removed $(BINDIR)/smith"
	@rm -rf "$(SKILLS_DIR)/cli-creator" && echo "removed $(SKILLS_DIR)/cli-creator"

cross:
	@rm -rf dist
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
	  os=$${p%-*}; arch=$${p#*-}; \
	  echo "building dist/smith-$$p"; \
	  GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 $(GO) build -trimpath \
	    -ldflags "-X main.version=$(VERSION)" -o dist/smith-$$p ./cmd/smith || exit 1; \
	done
	@cp skills/cli-creator/SKILL.md dist/cli-creator-SKILL.md
	@cd dist && { command -v sha256sum >/dev/null 2>&1 && sha256sum smith-* cli-creator-SKILL.md || shasum -a 256 smith-* cli-creator-SKILL.md; } > SHA256SUMS
	@echo "cross-compiled $(VERSION) -> dist/ (binaries + cli-creator-SKILL.md + SHA256SUMS)"

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

clean:
	rm -f smith
	rm -rf dist
