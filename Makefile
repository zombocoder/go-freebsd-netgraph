# go-freebsd-netgraph
#
# GO points at the toolchain to build with. The system `go` on the
# development host is 1.21, and go.mod requires 1.26, so the default here is
# the explicitly installed 1.26 toolchain.
GO ?= /usr/local/go126/bin/go

# GOTOOLCHAIN=local is deliberate and load-bearing, not tidiness.
#
# go.mod says `go 1.26`. Handed that module, an older go does NOT simply
# refuse it: since 1.21 the toolchain switch will silently DOWNLOAD a
# matching toolchain from the network (~71 MB) and build with that instead.
# The build then succeeds under a toolchain nobody chose — on this host,
# `go version` run inside the module reports go1.26.0 while the same binary
# outside it reports go1.21.13. GOTOOLCHAIN=local turns that into an
# immediate, honest failure that names the version required:
#
#	go: go.mod requires go >= 1.26 (running go 1.21.13; GOTOOLCHAIN=local)
#
# Override it only if you actually want the network fetch.
GOTOOLCHAIN ?= local

# Privilege escalation for the integration tests. FreeBSD, so doas.
DOAS ?= doas

# The build tag guarding tests that touch the kernel.
INTEGRATION_TAGS = integration

EXAMPLES = list mkpeer data
BINDIR = bin

.PHONY: all freebsd-check lint fmt vet test test-integration examples clean help

all: lint vet test

help:
	@echo "targets:"
	@echo "  freebsd-check     verify the host is FreeBSD and the toolchain is usable"
	@echo "  lint              gofmt -l over the tree; any output is a failure"
	@echo "  fmt               gofmt -w over the tree"
	@echo "  vet               go vet, with and without the integration tag"
	@echo "  test              unit tests (no kernel, no root)"
	@echo "  test-integration  integration tests (needs root; runs via $(DOAS))"
	@echo "  examples          build the example programs into ./$(BINDIR)"
	@echo "  clean             remove ./$(BINDIR) and the test cache"
	@echo ""
	@echo "variables: GO=$(GO) GOTOOLCHAIN=$(GOTOOLCHAIN) DOAS=$(DOAS)"

freebsd-check:
	@if [ "`uname -s`" != "FreeBSD" ]; then \
		echo "this package binds netgraph(4), which is FreeBSD-only; host is `uname -s`" >&2; \
		echo "(the package still builds elsewhere — every call returns ErrNotSupported)" >&2; \
		exit 1; \
	fi
	@GOTOOLCHAIN=$(GOTOOLCHAIN) $(GO) version
	@echo "FreeBSD `freebsd-version`, `uname -m` — ok"

lint:
	@out=`gofmt -l .`; \
	if [ -n "$$out" ]; then \
		echo "gofmt needs to run on:" >&2; echo "$$out" >&2; exit 1; \
	fi
	@echo "gofmt: clean"

fmt:
	gofmt -w .

vet:
	GOTOOLCHAIN=$(GOTOOLCHAIN) $(GO) vet ./...
	GOTOOLCHAIN=$(GOTOOLCHAIN) $(GO) vet -tags $(INTEGRATION_TAGS) ./...

build:
	GOTOOLCHAIN=$(GOTOOLCHAIN) $(GO) build ./...

test:
	GOTOOLCHAIN=$(GOTOOLCHAIN) $(GO) test ./... -count=1

# Needs root: every netgraph operation does. -count=1 because a cached PASS
# from a previous kernel state is worthless here.
test-integration:
	$(DOAS) env GOTOOLCHAIN=$(GOTOOLCHAIN) $(GO) test -tags $(INTEGRATION_TAGS) ./... -count=1

examples:
	@mkdir -p $(BINDIR)
	@for e in $(EXAMPLES); do \
		echo "GOTOOLCHAIN=$(GOTOOLCHAIN) $(GO) build -o $(BINDIR)/ng-$$e ./examples/$$e"; \
		GOTOOLCHAIN=$(GOTOOLCHAIN) $(GO) build -o $(BINDIR)/ng-$$e ./examples/$$e || exit 1; \
	done

clean:
	rm -rf $(BINDIR)
	GOTOOLCHAIN=$(GOTOOLCHAIN) $(GO) clean -testcache
