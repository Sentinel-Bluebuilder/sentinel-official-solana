SHELL := /bin/bash

SDK                 := github.com/sentinel-official/sentinel-go-sdk/v3
GIT_COMMIT          := $(shell git log -1 --format='%H' 2>/dev/null || echo unknown)
GIT_TAG             := $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//')
SENTINEL_NETWORK    ?= mainnet
SENTINEL_PROGRAM_ID ?=
SENTINEL_MINT       ?=

LD_FLAGS := -s -w \
	-X $(SDK)/version.Commit=$(GIT_COMMIT) \
	-X $(SDK)/version.Tag=$(GIT_TAG) \
	-X $(SDK)/version.Network=$(SENTINEL_NETWORK) \
	-X $(SDK)/version.ProgramID=$(SENTINEL_PROGRAM_ID) \
	-X $(SDK)/version.Mint=$(SENTINEL_MINT)

BUILD_FLAGS := -trimpath -mod=readonly -ldflags "$(LD_FLAGS)"

.PHONY: build build-cross fmt fmt-check install lint lint-cross test test-integration vet-cross

build:
	CGO_ENABLED=0 go build $(BUILD_FLAGS) -o ./bin/sentinel-dvpnd ./cmd/sentinel-dvpnd

fmt:
	@list_file=$$(mktemp) || exit $$?; trap 'rm -f "$$list_file"' EXIT; \
	git ls-files -z --cached --others --exclude-standard > "$$list_file" || exit; \
	sources=(); \
	while IFS= read -r -d '' file; do \
		[[ -f "$$file" && "$$file" == *.go ]] && sources+=("$$file"); \
	done < "$$list_file"; \
	gofmt -w "$${sources[@]}"

fmt-check:
	@list_file=$$(mktemp) || exit $$?; trap 'rm -f "$$list_file"' EXIT; \
	git ls-files -z --cached --others --exclude-standard > "$$list_file" || exit; \
	sources=(); \
	while IFS= read -r -d '' file; do \
		[[ -f "$$file" && "$$file" == *.go ]] && sources+=("$$file"); \
	done < "$$list_file"; \
	output=$$(gofmt -l "$${sources[@]}") || exit $$?; \
	if [[ -n "$$output" ]]; then printf '%s\n' "$$output"; exit 1; fi

install:
	CGO_ENABLED=0 go install $(BUILD_FLAGS) ./cmd/sentinel-dvpnd

lint:
	golangci-lint run

test:
	go test -race -cover ./...

# test-integration needs solana-test-validator on PATH and kora, and fails rather than skips without them.
#   SENTINEL_PROGRAMS_DIR  sentinel-programs checkout; loads its target/deploy/dvpn.so (anchor build)
#   SENTINEL_DVPN_SO       program binary to load instead of $SENTINEL_PROGRAMS_DIR/target/deploy/dvpn.so
#   SENTINEL_KORA_BIN      kora binary
test-integration:
	SENTINEL_REQUIRE_LOCALNET=1 go test -race -count=1 -tags integration ./...

CROSS_TARGETS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

build-cross:
	@for target in $(CROSS_TARGETS); do \
		os=$${target%/*}; arch=$${target#*/}; ext=""; \
		if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
		echo "building $$target"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build $(BUILD_FLAGS) -o ./bin/sentinel-dvpnd-$$os-$$arch$$ext ./cmd/sentinel-dvpnd || exit 1; \
	done

lint-cross:
	GOOS=darwin golangci-lint run --allow-parallel-runners
	GOOS=windows golangci-lint run --allow-parallel-runners

vet-cross:
	GOOS=darwin go vet ./...
	GOOS=windows go vet ./...
