.PHONY: build clean

BINARY_NAME=pyxcloud

# LDFLAGS:
# -s: Omit the symbol table and debug information.
# -w: Omit the DWARF symbol table.
LDFLAGS=-s -w

build:
	go build -ldflags="$(LDFLAGS)" -o $(BINARY_NAME) main.go

clean:
	go clean
	rm -f $(BINARY_NAME)
