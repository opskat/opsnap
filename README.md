# OpsNap

自托管的服务器备份与同步 Web 控制台：把 MySQL、PostgreSQL、MongoDB、Redis、Kafka 和服务器文件按计划备份到本地目录或 S3 兼容存储，并支持同类实例之间的同步。单个 Go 二进制内嵌前端。

> 项目处于早期开发阶段。v1 的需求规格见 [`docs/specs/2026-09-23-opsnap-v1.md`](docs/specs/2026-09-23-opsnap-v1.md)。

## 快速开始

需要 Go 1.26、Node.js 22、pnpm 10。

```bash
make install      # 安装前端与 e2e 依赖
make build        # 构建 bin/opsnap（内嵌前端）
cp configs/config.example.yaml configs/config.yaml
bin/opsnap        # 访问 http://127.0.0.1:8210
```

开发时分别运行 `make dev-server` 和 `make dev-web`。

## 文档

- 贡献与 AI 协作规则：[`AGENTS.md`](AGENTS.md)
- 开发文档索引：[`docs/README.md`](docs/README.md)
