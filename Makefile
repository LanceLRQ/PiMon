# PiMon 构建脚本
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
PKG     := github.com/LanceLRQ/PiMon/src
LDFLAGS := -s -w -X $(PKG)/pkg/version.Version=$(VERSION)
BIN     := $(CURDIR)/bin

# agent 目标平台（<os>/<arch>）与内嵌分发目录
AGENT_PLATFORMS := linux/amd64 linux/arm64 darwin/arm64
AGENT_DIST      := $(CURDIR)/src/internal/hub/agentdist/dist

.PHONY: generate check-generated web build build-go build-agent agent-dist test test-go test-web test-e2e lint lint-go lint-web dev clean

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

# 交叉编译 agent 到 bin/pimon-agent-<os>-<arch>
build-agent:
	@set -e; for p in $(AGENT_PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		echo "build pimon-agent-$$os-$$arch"; \
		(cd src && CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN)/pimon-agent-$$os-$$arch ./cmd/pimon-agent); \
	done

# 生成 hub 内嵌的 agent 分发物：gzip 二进制、SHA256SUMS（对未压缩二进制计算，文件名列不带 .gz）与 VERSION
agent-dist: build-agent
	@set -e; mkdir -p $(AGENT_DIST); \
	find $(AGENT_DIST) -mindepth 1 ! -name .gitkeep -delete; \
	names=""; \
	for p in $(AGENT_PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; n=pimon-agent-$$os-$$arch; \
		gzip -9 -n -c $(BIN)/$$n > $(AGENT_DIST)/$$n.gz; \
		names="$$names $$n"; \
	done; \
	if command -v sha256sum >/dev/null 2>&1; then sum="sha256sum"; else sum="shasum -a 256"; fi; \
	(cd $(BIN) && $$sum $$names) > $(AGENT_DIST)/SHA256SUMS; \
	printf '%s\n' '$(VERSION)' > $(AGENT_DIST)/VERSION

# 发布构建的 hub 内嵌 agent，所以先生成分发物
build-go: agent-dist
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
