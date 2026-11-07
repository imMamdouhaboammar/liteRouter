.PHONY: all build test test-go test-rust clean run

all: build test

build:
	@echo "==> Building Rust decision core (release)..."
	cargo build --release --manifest-path crates/literouter-decision/Cargo.toml
	@echo "==> Building Go liteRouter gateway..."
	go build -o bin/literouter ./cmd/literouter

test: test-rust test-go

test-go:
	@echo "==> Running Go unit and integration tests with race detector..."
	go test -v -count=1 -race ./...

test-rust:
	@echo "==> Running Rust unit tests..."
	cargo test --manifest-path crates/literouter-decision/Cargo.toml

run: build
	@echo "==> Starting liteRouter on :8080..."
	./bin/literouter --port=8080 --policy=rules/default_routing.star

clean:
	rm -rf bin/ target/ crates/literouter-decision/target
