#!/usr/bin/env bash
# 管理 docker.local 上的 opsnap-test 测试服务：up | down | status | destroy
# 通过 opsctl 资产 local-docker 操作远端（docs/verification.md#test-environment）。
# 测试密码保存在 e2e/.env 的 OPSNAP_TEST_PASSWORD（不提交），首次 up 时自动生成。
set -euo pipefail

ASSET="${OPSNAP_TEST_ASSET:-local-docker}"
REMOTE_DIR="/opt/opsnap-test"
# docker.local 通过镜像代理拉取镜像（形如 <代理>/docker.io/...、<代理>/quay.io/...）；换机器时用环境变量覆盖，设为空则直连
REGISTRY_MIRROR="${OPSNAP_TEST_REGISTRY_MIRROR-katch.ggnb.top/}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ENV_FILE="$ROOT/e2e/.env"

ssh_exec() { opsctl exec "$ASSET" --type ssh -- "$1"; }

ensure_password() {
  touch "$ENV_FILE" && chmod 600 "$ENV_FILE"
  if ! grep -q '^OPSNAP_TEST_PASSWORD=' "$ENV_FILE"; then
    echo "OPSNAP_TEST_PASSWORD=$(openssl rand -hex 16)" >>"$ENV_FILE"
  fi
  grep '^OPSNAP_TEST_PASSWORD=' "$ENV_FILE" | cut -d= -f2-
}

compose() { echo "cd $REMOTE_DIR && docker compose -f docker-compose.yaml $*"; }

case "${1:-}" in
up)
  password="$(ensure_password)"
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  printf 'OPSNAP_TEST_PASSWORD=%s\nREGISTRY_MIRROR=%s\n' "$password" "$REGISTRY_MIRROR" >"$tmp/.env"
  chmod 600 "$tmp/.env"
  ssh_exec "mkdir -p $REMOTE_DIR && chmod 700 $REMOTE_DIR"
  opsctl cp "$ROOT/deploy/test/docker-compose.yaml" "$ASSET:$REMOTE_DIR/docker-compose.yaml"
  ssh_exec "mkdir -p $REMOTE_DIR/keycloak"
  opsctl cp "$ROOT/deploy/test/keycloak/opsnap-realm.json" "$ASSET:$REMOTE_DIR/keycloak/opsnap-realm.json"
  opsctl cp "$tmp/.env" "$ASSET:$REMOTE_DIR/.env"
  ssh_exec "chmod 600 $REMOTE_DIR/.env && $(compose up -d --wait)"
  ;;
down) ssh_exec "$(compose stop)" ;;
status) ssh_exec "$(compose ps)" ;;
destroy) ssh_exec "$(compose down -v) && rm -rf $REMOTE_DIR" ;;
*)
  echo "用法：$0 up|down|status|destroy" >&2
  exit 2
  ;;
esac
