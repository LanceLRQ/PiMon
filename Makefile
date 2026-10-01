# PiMon 构建脚本
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
PKG     := github.com/LanceLRQ/PiMon/src
LDFLAGS := -s -w -X $(PKG)/pkg/version.Version=$(VERSION)
BIN     := $(CURDIR)/bin

.PHONY: generate check-generated web build build-go test test-go test-web test-e2e lint lint-go lint-web dev clean

TYGO_VERSION := v0.2.21

# 由 Go 模型生成前端 TS 类型（src/tygo.yaml）
generate:
	cd src && go run github.com/gzuidhof/tygo@$(TYGO_VERSION) generate

# 生成物漂移检查：重新生成后，web/src/types 下的内容必须与已提交的一致（只比较该目录，不受工作区其他改动影响）
check-generated:
	@before="$$(git diff -- web/src/types; git ls-files --others --exclude-standard web/src/types | xargs -I{} sh -c 'echo {}; cat {}')"; \
	$(MAKE) --no-print-directory generate || exit 1; \
	after="$$(git diff -- web/src/types; git ls-files --others --exclude-standard web/src/types | xargs -I{} sh -c 'echo {}; cat {}')"; \
	if [ "$$before" != "$$after" ]; then echo "tygo 生成物与 Go 模型不一致，请运行 make generate 并提交 web/src/types"; git diff --stat -- web/src/types; exit 1; fi

# 构建前端，产物写入 src/internal/hub/webui/dist（由 hub 通过 go:embed 打包）
web:
	cd web && pnpm install --frozen-lockfile && pnpm build

build: web build-go

build-go:
	cd src && CGO_ENABLED=0 GOOS=linux  GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN)/pimon-hub-linux-arm64  ./cmd/pimon-hub
	cd src && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN)/pimon-hub-darwin-arm64 ./cmd/pimon-hub

test: test-go test-web test-e2e

test-go:
	cd src && CGO_ENABLED=1 go test -race -count=1 ./...

test-web:
	cd web && pnpm install --frozen-lockfile && pnpm test

# Playwright 冒烟：先构建前端（hub 内嵌最新产物），再由 globalSetup 编译并启动真实 hub
# （临时数据目录 + 随机端口），跑完关闭并清理。需要 Go 与本机 Chromium（缺失时自动安装）
test-e2e: web
	cd web && pnpm exec playwright install chromium
	cd web && pnpm e2e

lint: check-generated lint-go lint-web

lint-go:
	@out="$$(cd src && gofmt -l .)"; if [ -n "$$out" ]; then echo "以下文件未 gofmt："; echo "$$out"; exit 1; fi
	cd src && go vet ./...
	cd src && golangci-lint run ./...

lint-web:
	cd web && pnpm install --frozen-lockfile && pnpm typecheck && pnpm lint

# 本地开发：同时启动 hub 与 Vite（Vite 把 /api、/ws、/screen/auth 代理到 hub）
# 数据目录用仓库下的 data/（已在 .gitignore）；Ctrl-C 一并结束两个进程
dev:
	cd web && pnpm install --frozen-lockfile
	@trap 'kill 0' EXIT INT TERM; \
	(cd web && pnpm dev) & \
	(cd src && go run ./cmd/pimon-hub serve --data-dir $(CURDIR)/data) & \
	wait

clean:
	rm -rf $(BIN)
