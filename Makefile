BINARY := myai
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/carlbomsdata/myai/internal/app.Version=$(VERSION)

.PHONY: build test vet fmt install dist clean

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/myai

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

install: build
	./$(BINARY) install

# dist cross-compiles every supported target.
dist:
	rm -rf dist && mkdir -p dist
	GOOS=darwin  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-darwin-arm64 ./cmd/myai
	GOOS=linux   GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-amd64 ./cmd/myai
	GOOS=linux   GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-arm64 ./cmd/myai
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-windows-amd64.exe ./cmd/myai
	GOOS=windows GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-windows-arm64.exe ./cmd/myai

clean:
	rm -rf $(BINARY) $(BINARY).exe dist
