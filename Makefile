# OpsNap 统一命令入口。前端命令也可在 frontend/ 下用 pnpm 直接执行。
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/cago-frame/cago/configs.Version=$(VERSION)
BIN := bin/opsnap

.PHONY: install dev-server dev-web build build-web build-server build-fakeidp generate lint lint-fix test test-cover e2e verify clean

install: ## 安装前端依赖与 e2e 浏览器
	pnpm -C frontend install --frozen-lockfile
	pnpm -C e2e install --frozen-lockfile
	pnpm -C e2e exec playwright install chromium

configs/config.yaml:
	cp configs/config.example.yaml configs/config.yaml

dev-server: configs/config.yaml ## 启动后端（127.0.0.1:8210）
	go run ./cmd/opsnap

dev-web: ## 启动前端开发服务器，/api 转发到后端
	pnpm -C frontend dev

build-web:
	pnpm -C frontend build

build-server:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/opsnap

build: build-web build-server ## 构建前端并内嵌进单个二进制 bin/opsnap

generate: ## 重新生成 mock 等代码
	go generate ./...

lint: ## 全量静态检查：golangci-lint + ESLint + Prettier + i18n 键检查
	golangci-lint run ./...
	pnpm -C frontend lint
	pnpm -C e2e lint

lint-fix:
	golangci-lint run --fix ./...
	pnpm -C frontend lint:fix
	pnpm -C e2e lint:fix

test: ## 单元测试（含分层守护与 lint 守护测试）
	go test ./...
	pnpm -C frontend test

test-cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

build-fakeidp:
	CGO_ENABLED=0 go build -trimpath -o bin/fakeidp ./tools/fakeidp

e2e: build build-fakeidp ## 冒烟 e2e：临时目录 + 专用端口启动 bin/opsnap 与假 OIDC 提供方，用 Playwright 驱动
	pnpm -C e2e test

verify: lint test e2e ## 提交前的完整验证

clean:
	rm -rf bin coverage.out frontend/dist e2e/test-results e2e/playwright-report
	find internal/web/dist -mindepth 1 ! -name .gitkeep -delete

.PHONY: test-env-up test-env-down test-env-status
test-env-up: ## 在 docker.lan 部署/更新 opsnap-test 测试服务（docs/verification.md）
	scripts/test-env.sh up
test-env-down:
	scripts/test-env.sh down
test-env-status:
	scripts/test-env.sh status
