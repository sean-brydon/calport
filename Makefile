GO ?= go
LDFLAGS := -s -w
BIN := bin

.PHONY: all build daemons test clean

# calport for this machine, plus calportd for every box platform, side by side
# in bin/ so `calport add ssh` can find the right daemon to upload.
all: build daemons

build:
	$(GO) build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN)/ ./cmd/calport ./cmd/calportd

daemons:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN)/calportd-linux-amd64 ./cmd/calportd
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN)/calportd-linux-arm64 ./cmd/calportd

test:
	$(GO) vet ./...
	$(GO) test -race ./...

clean:
	rm -rf $(BIN) $(DIST)

# The desktop app bundles calport as a Tauri sidecar (named for the host's
# target triple) and the Linux daemons as resources for `add ssh`.
TRIPLE := $(shell rustc -vV 2>/dev/null | sed -n 's/^host: //p')
SIDECAR := app/src-tauri/binaries

.PHONY: app-binaries app-dev app-build
app-binaries: daemons
	mkdir -p $(SIDECAR)
	$(GO) build -trimpath -ldflags="$(LDFLAGS)" -o $(SIDECAR)/calport-$(TRIPLE) ./cmd/calport
	cp $(BIN)/calportd-linux-amd64 $(BIN)/calportd-linux-arm64 $(SIDECAR)/

app-dev: app-binaries
	cd app && pnpm tauri dev

app-build: app-binaries
	cd app && pnpm tauri build

# release builds every asset install.sh can fetch, plus their checksums, into
# dist/. Upload the whole directory to a GitHub release.
DIST := dist
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: release
release:
	rm -rf $(DIST) && mkdir -p $(DIST)
	for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o $(DIST)/calport-$$os-$$arch ./cmd/calport || exit 1; \
	done
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o $(DIST)/calportd-linux-amd64 ./cmd/calportd
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o $(DIST)/calportd-linux-arm64 ./cmd/calportd
	cd $(DIST) && shasum -a 256 calport-* calportd-* > SHA256SUMS
