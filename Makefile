.PHONY: build rust-build rust-test test test-unit test-integration test-e2e fmt tidy clean docker-build up up-regtest up-testnet up-mainnet down logs

TESTFLAGS ?=

ifneq ($(JUNO_TEST_LOG),)
TESTFLAGS += -v
endif

BIN_DIR := bin
BIN := $(BIN_DIR)/juno-exchange-kit

RUST_MANIFEST := rust/keys/Cargo.toml
DOCKER_COMPOSE := docker compose -f docker-compose.yml

JUNOCASH_VERSION ?= 0.9.7
JUNO_SCAN_REF ?= 95c424b67aa6a8b6e69162bb56bedcfce2edde83
JUNO_BROADCAST_REF ?= 0be86c29573ea9cd1993358e387686b534d212c8

NETWORK_GOAL := $(firstword $(filter mainnet testnet regtest,$(MAKECMDGOALS)))

JUNO_CHAIN ?= regtest
ifneq ($(NETWORK_GOAL),)
ifeq ($(origin JUNO_CHAIN),file)
JUNO_CHAIN := $(NETWORK_GOAL)
endif
endif

JUNO_RPC_PORT_HOST ?= 28232
JUNO_SCAN_PORT_HOST ?= 18080
JUNO_BROADCAST_PORT_HOST ?= 18081

ifeq ($(JUNO_CHAIN),regtest)
JUNO_SCAN_UA_HRP ?= jregtest
JUNO_SCAN_CONFIRMATIONS ?= 1
else ifeq ($(JUNO_CHAIN),testnet)
JUNO_SCAN_UA_HRP ?= jtest
JUNO_SCAN_CONFIRMATIONS ?= 100
else
JUNO_SCAN_UA_HRP ?= j
JUNO_SCAN_CONFIRMATIONS ?= 100
endif

JUNOCASHD_IMAGE := juno-exchange-kit/junocashd:$(JUNOCASH_VERSION)
JUNO_SCAN_IMAGE := juno-exchange-kit/juno-scan:$(JUNO_SCAN_REF)
JUNO_BROADCAST_IMAGE := juno-exchange-kit/juno-broadcast:$(JUNO_BROADCAST_REF)

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

docker-build:
	docker build --platform=linux/amd64 -t $(JUNOCASHD_IMAGE) --build-arg JUNOCASH_VERSION=$(JUNOCASH_VERSION) -f docker/junocashd/Dockerfile .
	docker build --platform=linux/amd64 -t $(JUNO_BROADCAST_IMAGE) --build-arg JUNO_BROADCAST_REF=$(JUNO_BROADCAST_REF) -f docker/juno-broadcast/Dockerfile .
	docker build --platform=linux/amd64 -t $(JUNO_SCAN_IMAGE) --build-arg JUNO_SCAN_REF=$(JUNO_SCAN_REF) -f docker/juno-scan/Dockerfile .

up: docker-build
	COMPOSE_BAKE=0 \
	JUNOCASH_VERSION=$(JUNOCASH_VERSION) \
	JUNO_CHAIN=$(JUNO_CHAIN) \
	JUNO_RPC_PORT_HOST=$(JUNO_RPC_PORT_HOST) \
	JUNO_SCAN_PORT_HOST=$(JUNO_SCAN_PORT_HOST) \
	JUNO_BROADCAST_PORT_HOST=$(JUNO_BROADCAST_PORT_HOST) \
	JUNO_SCAN_REF=$(JUNO_SCAN_REF) \
	JUNO_SCAN_UA_HRP=$(JUNO_SCAN_UA_HRP) \
	JUNO_SCAN_CONFIRMATIONS=$(JUNO_SCAN_CONFIRMATIONS) \
	JUNO_BROADCAST_REF=$(JUNO_BROADCAST_REF) \
	$(DOCKER_COMPOSE) up -d --no-build

up-regtest:
	$(MAKE) up JUNO_CHAIN=regtest

up-testnet:
	$(MAKE) up JUNO_CHAIN=testnet

up-mainnet:
	$(MAKE) up JUNO_CHAIN=mainnet

down:
	COMPOSE_BAKE=0 $(DOCKER_COMPOSE) down -v

logs:
	COMPOSE_BAKE=0 $(DOCKER_COMPOSE) logs -f --tail=200

.PHONY: mainnet testnet regtest
mainnet testnet regtest:
	@:
