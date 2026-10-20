.PHONY: build build-go frontend frontend-test check run test lint gen tidy it it-vet

VERSION ?= 0.1.1
LDFLAGS := -s -w -X github.com/cago-frame/cago/configs.Version=$(VERSION)
CAGO_VERSION := v0.0.0-20260926025905-604723c9e69d

# 构建前端并把产物内嵌进 opsnap 二进制
build: frontend build-go

# 只编译 Go 二进制（内嵌 web/dist 现有内容；没有前端产物时内嵌占位页）
build-go:
	@mkdir -p web/dist && [ -e web/dist/.keep ] || touch web/dist/.keep
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/opsnap ./cmd/app

# 构建前端（frontend/dist），再替换 web/dist 的内容；保留 git 跟踪的 web/dist/.keep
frontend:
	cd frontend && pnpm install --frozen-lockfile && pnpm build
	@mkdir -p web/dist && [ -e web/dist/.keep ] || touch web/dist/.keep
	find web/dist -mindepth 1 -maxdepth 1 ! -name .keep -exec rm -rf {} +
	cp -R frontend/dist/. web/dist/

# 前端类型检查与组件测试（vitest）
frontend-test:
	cd frontend && pnpm install --frozen-lockfile && pnpm typecheck && pnpm lint && pnpm test

# 提交前的完整检查：Go 测试、lint 与前端测试
check: test lint frontend-test

# 本地运行；需要先设置 OPSNAP_SECRET_KEY，并把配置里的 dataDir、db.dsn 改到可写目录
run:
	go run ./cmd/app -config ./configs/config.yaml

test:
	go test ./...

lint:
	golangci-lint run

# 用与 go.mod 一致的 cago 版本生成 controller/service 与 docs/ 下的 OpenAPI 文档
gen:
	go run github.com/cago-frame/cago/cmd/cago@$(CAGO_VERSION) gen

tidy:
	go mod tidy

# 集成测试：经 opsctl 在集成测试主机的 compose 项目 opsnap-it 里构建镜像、运行、清理（见 scripts/it.sh）
# OPSNAP_IT_SOURCE=head 只用 HEAD 提交的代码（叠加工作区里的集成测试文件）
it:
	scripts/it.sh all

# 只在本机编译检查集成测试代码（带 integration 构建标签，make test 不会编译它）
it-vet:
	go vet -tags integration ./test/integration/
