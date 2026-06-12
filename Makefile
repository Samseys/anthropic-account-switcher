# Cross-platform build tooling for acc-claude. Needs only `make` and the Go
# toolchain: every recipe is a single command that runs the same under cmd.exe
# and a POSIX shell. All platform-specific work lives in ./scripts/build.

GO   := go
TOOL := $(GO) run ./scripts/build

# Pass `make dist VERSION=4.0.0` to override the git-derived version.
export VERSION

.PHONY: all help build install dist clean version test vet fmt tidy check

all: build

help:
	@echo "Targets:"
	@echo "  build     Compile for the current platform into ./bin"
	@echo "  install   go install into GOBIN/GOPATH"
	@echo "  dist      Cross-compile every release target + SHA256SUMS into ./dist"
	@echo "  test      Run tests"
	@echo "  vet       Run go vet"
	@echo "  fmt       Format sources in place"
	@echo "  tidy      Tidy go.mod / go.sum"
	@echo "  check     vet + test"
	@echo "  clean     Remove build artifacts"
	@echo "  version   Print the version that would be embedded"

build:
	$(TOOL) build

install:
	$(TOOL) install

dist:
	$(TOOL) dist

clean:
	$(TOOL) clean

version:
	@$(TOOL) version

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	gofmt -w .

tidy:
	$(GO) mod tidy

check: vet test
