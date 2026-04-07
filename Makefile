.PHONY: build clean

BINARY_NAME=pyxcloud

# LDFLAGS:
# -s: Omit the symbol table and debug information.
# -w: Omit the DWARF symbol table.
LDFLAGS=-s -w

build:
	go build -ldflags="$(LDFLAGS)" -o $(BINARY_NAME) main.go

release-local:
	docker run --rm --privileged \
		-v $(PWD):/go/src/github.com/user/pyxcloud-cli \
		-w /go/src/github.com/user/pyxcloud-cli \
		goreleaser/goreleaser build --snapshot --clean

clean:
	go clean
	rm -f $(BINARY_NAME)
	rm -rf dist/
