SHELL := /bin/bash

BASE ?= HEAD

.PHONY: build build-cross check-contracts check-generate fmt fmt-check generate lint lint-cross test test-integration vet-cross

build:
	go build ./...

check-contracts:
	buf breaking daemon/v1 --against ".git#branch=$(BASE),subdir=daemon/v1"
	go run github.com/oasdiff/oasdiff@v1.35.0 breaking <(git show $(BASE):nodeapi/openapi.yaml) nodeapi/openapi.yaml --fail-on ERR
	go run github.com/oasdiff/oasdiff@v1.35.0 breaking <(git show $(BASE):providerapi/openapi.yaml) providerapi/openapi.yaml --fail-on ERR

check-generate: generate
	git diff --exit-code HEAD -- .
	test -z "$$(git status --porcelain --untracked-files=all -- .)"

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

generate:
	buf generate
	go generate ./...

lint:
	golangci-lint run
	buf lint

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
		echo "building $$target"; \
		GOOS=$${target%/*} GOARCH=$${target#*/} CGO_ENABLED=0 go build ./... || exit 1; \
	done

lint-cross:
	GOOS=darwin golangci-lint run --allow-parallel-runners
	GOOS=windows golangci-lint run --allow-parallel-runners

vet-cross:
	GOOS=darwin go vet ./...
	GOOS=windows go vet ./...
