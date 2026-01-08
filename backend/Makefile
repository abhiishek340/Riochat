.PHONY: build vet test check run run-local lint fmt

build:
	go build ./...

vet:
	go vet ./...

test:
	go test ./...

# the gate every change must pass
check:
	go build ./... && go vet ./... && go test ./...

run-local:
	go run ./cmd/riochat --local --data-dir ./data

# run the server with the configuration found through CONFIG_FILE or config.yaml
run:
	go run ./cmd/riochat

# static checks: vet plus a gofmt cleanliness gate
lint:
	go vet ./...
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

fmt:
	gofmt -w .
