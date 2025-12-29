.PHONY: build rust-build rust-test test test-unit test-integration test-e2e fmt tidy clean

TESTFLAGS ?=

ifneq ($(JUNO_TEST_LOG),)
TESTFLAGS += -v
endif

BIN_DIR := bin
BIN := $(BIN_DIR)/juno-exchange-kit

RUST_MANIFEST := rust/keys/Cargo.toml

build: rust-build
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN) ./cmd/juno-exchange-kit

rust-build:
	cargo build --release --manifest-path $(RUST_MANIFEST)

rust-test:
	cargo test --manifest-path $(RUST_MANIFEST)

test-unit:
	CGO_ENABLED=0 go test $(TESTFLAGS) ./...

test-integration:
	$(MAKE) rust-build
	go test $(TESTFLAGS) -tags=integration ./...

test-e2e:
	$(MAKE) build
	go test $(TESTFLAGS) -tags=e2e ./...

test: rust-test test-unit test-integration test-e2e

fmt:
	gofmt -w .

tidy:
	go mod tidy

clean:
	rm -rf $(BIN_DIR)
	rm -rf rust/keys/target
