# 集成测试 runner（opsnap-it/runner）：以刚构建的 opsnap 镜像为基础（工具二进制与生产镜像完全相同），
# 加上编译好的集成测试二进制。构建上下文是仓库根目录，见 scripts/it-remote.sh。
ARG GO_VERSION=1.27
ARG OPSNAP_IMAGE=opsnap-it/opsnap:latest

FROM golang:${GO_VERSION}-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY test/integration ./test/integration
RUN CGO_ENABLED=0 go test -c -tags integration -o /out/opsnap-it.test ./test/integration

FROM ${OPSNAP_IMAGE}
# 测试要在共享卷里建目录、改属主，并读取 opsnap 用户写下的仓库文件
USER root
COPY --from=build /out/opsnap-it.test /usr/local/bin/opsnap-it.test
WORKDIR /tmp
HEALTHCHECK NONE
ENTRYPOINT ["/usr/local/bin/opsnap-it.test"]
CMD ["-test.v"]
