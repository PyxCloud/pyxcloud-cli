.PHONY: build clean generate sync-contracts

BINARY_NAME=pyxcloud

# LDFLAGS:
# -s: Omit the symbol table and debug information.
# -w: Omit the DWARF symbol table.
LDFLAGS=-s -w

build:
	go build -ldflags="$(LDFLAGS)" -o $(BINARY_NAME) .

# Regenerate the typed API clients (internal/api/gen/...) from api/contracts.
generate:
	go generate ./internal/api/gen/...

# Re-vendor api/contracts at the pinned pyx-backend SHA (api/contracts/SOURCE).
# Bump the pin: scripts/sync-contracts.sh --ref <branch|sha> && make generate
sync-contracts:
	scripts/sync-contracts.sh

release-local:
	docker run --rm --privileged \
		-v $(PWD):/go/src/github.com/user/pyxcloud-cli \
		-w /go/src/github.com/user/pyxcloud-cli \
		goreleaser/goreleaser release --snapshot --clean

clean:
	go clean
	rm -f $(BINARY_NAME)
	rm -rf dist/
