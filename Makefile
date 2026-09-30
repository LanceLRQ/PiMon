# PiMon 构建脚本（本期只有 Go 目标，前端目标在 M1c 加入）
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
PKG     := github.com/LanceLRQ/PiMon/src
LDFLAGS := -s -w -X $(PKG)/pkg/version.Version=$(VERSION)
BIN     := $(CURDIR)/bin

.PHONY: build test lint dev clean

build:
	cd src && CGO_ENABLED=0 GOOS=linux  GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN)/pimon-hub-linux-arm64  ./cmd/pimon-hub
	cd src && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN)/pimon-hub-darwin-arm64 ./cmd/pimon-hub

test:
	cd src && CGO_ENABLED=1 go test -race -count=1 ./...

lint:
	cd src && go vet ./...
	cd src && golangci-lint run ./...

# 本地开发：数据目录用仓库下的 data/（已在 .gitignore）
dev:
	cd src && go run ./cmd/pimon-hub serve --data-dir $(CURDIR)/data

clean:
	rm -rf $(BIN)
