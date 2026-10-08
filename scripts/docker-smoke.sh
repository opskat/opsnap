#!/usr/bin/env bash
# Docker 镜像冒烟检查（CI 的 docker 任务与 make docker-smoke 共用）：
#   1. 镜像必须已存在（不会去拉取或构建）；
#   2. 用一个临时数据卷启动容器，等 HEALTHCHECK 变为 healthy，再从宿主机请求 /api/v1/system/health，
#      元数据库状态为 ok，并返回构建时写入的提交短号；
#   3. 首次启动的设置码出现在容器日志里；
#   4. /opt/opsnap/tools 的版本子目录齐全（mysql-8.0、mysql-8.4、mysql-9.x、postgresql-14…18、mariadb），
#      其中每个 bin/ 下的工具都能执行 --version，并报出与子目录一致的版本。
# 用法：scripts/docker-smoke.sh [镜像，默认 opsnap:local]
set -euo pipefail

IMAGE="${1:-opsnap:local}"
TIMEOUT="${OPSNAP_SMOKE_TIMEOUT:-120}"
NAME="opsnap-smoke-$$"
TOOLS=/opt/opsnap/tools

fail() {
  echo "冒烟检查失败：$*" >&2
  exit 1
}

if ! docker image inspect "$IMAGE" >/dev/null 2>&1; then
  fail "镜像 $IMAGE 不存在，请先构建（make docker-build IMAGE=$IMAGE）"
fi

cleanup() {
  local code=$?
  if [ "$code" -ne 0 ] && docker container inspect "$NAME" >/dev/null 2>&1; then
    echo "---- 容器日志（最后 50 行）----" >&2
    docker logs --tail 50 "$NAME" >&2 || true
  fi
  docker rm -f -v "$NAME" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "启动容器 $NAME（$IMAGE）"
# 不挂载宿主机目录：/data 为匿名卷，随容器一起删除（docker rm -v）
docker run -d --name "$NAME" -p 127.0.0.1::8210 "$IMAGE" >/dev/null

echo "等待健康检查（最长 ${TIMEOUT}s）"
deadline=$((SECONDS + TIMEOUT))
while :; do
  status="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$NAME")"
  running="$(docker inspect -f '{{.State.Running}}' "$NAME")"
  [ "$status" = "none" ] && fail "镜像没有定义 HEALTHCHECK"
  [ "$running" = "true" ] || fail "容器已退出"
  [ "$status" = "healthy" ] && break
  [ "$SECONDS" -ge "$deadline" ] && fail "${TIMEOUT}s 内健康检查未通过（当前状态 $status）"
  sleep 2
done

port="$(docker port "$NAME" 8210/tcp | head -n1 | sed 's/.*://')"
health="$(curl -fsS "http://127.0.0.1:$port/api/v1/system/health")" || fail "从宿主机请求健康接口失败"
echo "健康接口：$health"
echo "$health" | grep -q '"database":"ok"' || fail "元数据库状态不是 ok"
# 构建参数 COMMIT 应写入二进制：提交短号为十六进制
echo "$health" | grep -qE '"commit":"[0-9a-f]{7,}"' || fail "健康接口没有返回提交短号（构建时是否传了 COMMIT？）"

docker logs "$NAME" 2>&1 | grep -q "设置码" || fail "容器日志中没有设置码"
echo "容器日志中有设置码"

# 子目录名与其中必须有的工具
expect_dirs=(mysql-8.0 mysql-8.4 postgresql-14 postgresql-15 postgresql-16 postgresql-17 postgresql-18 mariadb)
dirs="$(docker exec "$NAME" sh -c "ls -1 $TOOLS")"
for d in "${expect_dirs[@]}"; do
  echo "$dirs" | grep -qx "$d" || fail "$TOOLS 下缺少 $d"
done
[ "$(echo "$dirs" | grep -cE '^mysql-9\.[0-9]+$')" = "1" ] || fail "$TOOLS 下应有且只有一个 mysql-9.x"

required_tools() {
  case "$1" in
  mysql-*) echo mysqldump ;;
  postgresql-*) echo pg_dump pg_dumpall ;;
  mariadb) echo mariadb-dump ;;
  esac
}

# 子目录名对应的版本前缀：mysql-8.4 → 8.4.，postgresql-16 → 16.；mariadb 只要求输出里有 MariaDB
version_pattern() {
  case "$1" in
  mysql-*) echo "Ver ${1#mysql-}\\." ;;
  postgresql-*) echo "PostgreSQL) ${1#postgresql-}\\." ;;
  mariadb) echo "MariaDB" ;;
  esac
}

count=0
for d in $dirs; do
  for t in $(required_tools "$d"); do
    docker exec "$NAME" test -x "$TOOLS/$d/bin/$t" || fail "缺少 $TOOLS/$d/bin/$t"
  done
  pattern="$(version_pattern "$d")"
  for tool in $(docker exec "$NAME" sh -c "ls -1 $TOOLS/$d/bin"); do
    path="$TOOLS/$d/bin/$tool"
    out="$(docker exec "$NAME" "$path" --version 2>&1)" || fail "$path --version 执行失败：$out"
    if [ -n "$pattern" ] && ! echo "$out" | grep -qE "$pattern"; then
      fail "$path 报出的版本与子目录 $d 不一致：$out"
    fi
    echo "$path: $out"
    count=$((count + 1))
  done
done
[ "$count" -gt 0 ] || fail "$TOOLS 下没有任何工具"

echo "冒烟检查通过：镜像 $IMAGE，健康检查通过，$count 个预装工具均报出版本"
