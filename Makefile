VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
STATICCHECK := honnef.co/go/tools/cmd/staticcheck@2024.1.1
PLATFORMS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64

.PHONY: build install test cover lint fmt release clean

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/streamingchasers ./cmd/streamingchasers

install:
	go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/streamingchasers

test:
	go test -race -count=1 ./...

cover:
	go test -count=1 -coverpkg=./cmd/...,./internal/api/...,./internal/cli/...,./internal/config/...,./internal/oauth/...,./internal/output/... -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1
	go tool cover -html=coverage.out -o coverage.html

lint:
	@unformatted=$$(gofmt -l .); if [ -n "$$unformatted" ]; then echo "gofmt would change:"; echo "$$unformatted"; exit 1; fi
	go vet ./...
	go run $(STATICCHECK) ./...

fmt:
	gofmt -w .

release: clean
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; ext=; \
		if [ $$os = windows ]; then ext=.exe; fi; \
		echo "dist/streamingchasers-$(VERSION)-$$os-$$arch$$ext"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" \
			-o dist/streamingchasers-$(VERSION)-$$os-$$arch$$ext ./cmd/streamingchasers || exit 1; \
	done
	cd dist && shasum -a 256 streamingchasers-* > SHA256SUMS

clean:
	rm -rf bin dist coverage.out coverage.html
