VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.Version=$(VERSION)
BIN := build

.PHONY: all build test vet lint run linux windows darwin docker clean check

all: vet test build

build:
	mkdir -p $(BIN)
	go build -ldflags "$(LDFLAGS)" -o $(BIN)/greenroom ./cmd/greenroom

linux:
	mkdir -p $(BIN)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BIN)/greenroom-linux-amd64 ./cmd/greenroom
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BIN)/greenroom-linux-arm64 ./cmd/greenroom

windows:
	mkdir -p $(BIN)
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BIN)/greenroom-windows-amd64.exe ./cmd/greenroom

darwin:
	mkdir -p $(BIN)
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BIN)/greenroom-darwin-arm64 ./cmd/greenroom

test:
	go test ./... -count=1

vet:
	go vet ./...

check: vet test

run: build
	$(BIN)/greenroom -C examples serve

docker:
	docker build -t greenroom:$(VERSION) --build-arg VERSION=$(VERSION) .

clean:
	rm -rf $(BIN)
