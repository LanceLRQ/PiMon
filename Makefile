# PiMon 构建脚本
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
PKG     := github.com/LanceLRQ/PiMon/src
LDFLAGS := -s -w -X $(PKG)/pkg/version.Version=$(VERSION)
BIN     := $(CURDIR)/bin

.PHONY: generate web build test test-go test-web lint lint-go lint-web dev clean

TYGO_VERSION := v0.2.21

# 由 Go 模型生成前端 TS 类型（src/tygo.yaml）
generate:
	cd src && go run github.com/gzuidhof/tygo@$(TYGO_VERSION) generate

# 构建前端，产物写入 src/internal/hub/webui/dist（由 hub 通过 go:embed 打包）
web:
	cd web && pnpm install --frozen-lockfile && pnpm build

build: web
	cd src && CGO_ENABLED=0 GOOS=linux  GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN)/pimon-hub-linux-arm64  ./cmd/pimon-hub
	cd src && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN)/pimon-hub-darwin-arm64 ./cmd/pimon-hub

test: test-go test-web

test-go:
	cd src && CGO_ENABLED=1 go test -race -count=1 ./...

test-web:
	cd web && pnpm install --frozen-lockfile && pnpm test

lint: lint-go lint-web

lint-go:
	cd src && go vet ./...
	cd src && golangci-lint run ./...

lint-web:
	cd web && pnpm install --frozen-lockfile && pnpm typecheck && pnpm lint

# 本地开发：同时启动 hub 与 Vite（Vite 把 /api、/ws、/screen/auth 代理到 hub）
# 数据目录用仓库下的 data/（已在 .gitignore）；Ctrl-C 一并结束两个进程
dev:
	@trap 'kill 0' EXIT INT TERM; \
	(cd web && pnpm dev) & \
	(cd src && go run ./cmd/pimon-hub serve --data-dir $(CURDIR)/data) & \
	wait

clean:
	rm -rf $(BIN)
