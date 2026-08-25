# Build and check the jackfield CLI.
#
# The man page is generated, not written by hand. `make man` rebuilds
# docs/man/jf.1 from the command tree, so the page cannot describe a command that
# jf does not have.

BINARY := jf
BUILD_DIR := build
MAN_PAGE := docs/man/jf.1

.PHONY: all build test vet fmt check man clean

all: check build

build:
	go build -o $(BUILD_DIR)/$(BINARY) ./cmd/jf

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

check: vet test

# man regenerates the manual page from the command tree.
#
# Run it after any change to a command name, a flag, or a help text, and commit
# the result. The page ships in the release archive and both installers put a
# copy under the home directory.
man: build
	@mkdir -p $(dir $(MAN_PAGE))
	./$(BUILD_DIR)/$(BINARY) man > $(MAN_PAGE)
	@echo "Wrote $(MAN_PAGE)."

clean:
	rm -rf $(BUILD_DIR)
