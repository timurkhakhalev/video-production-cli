BIN      := video-production-cli
PREFIX   ?= $(HOME)/.local
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X main.version=$(VERSION)

.PHONY: build install test test-e2e golden vet clean

build:
	go build -ldflags '$(LDFLAGS)' -o bin/$(BIN) ./cmd/$(BIN)

install: build
	install -d $(PREFIX)/bin
	install -m 0755 bin/$(BIN) $(PREFIX)/bin/$(BIN)

test:
	go test ./...

# end-to-end tests on synthetic media (needs ffmpeg/ffprobe)
test-e2e:
	go test -tags e2e -count=1 ./...

# regenerate golden outputs from the reference render.py (needs python3)
golden:
	python3 testdata/golden/gen.py

vet:
	go vet ./...

clean:
	rm -rf bin
