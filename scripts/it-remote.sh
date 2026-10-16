#!/usr/bin/env bash
# 在集成测试主机（opsctl 资产）上运行，由 scripts/it.sh 调用；也可登录主机后手工分步执行。
#
#   it-remote.sh all        构建镜像 → 启动 → 主流程测试 → 重建 target 换主机密钥 → 指纹测试 → 清理（默认）
#   it-remote.sh build|up|test|rekey|test-rekeyed|status|down
#
# 安全约束：只操作 compose 项目 opsnap-it 的容器、网络、卷，以及本脚本构建的 opsnap-it/* 镜像；
# 不发布任何端口，不连接、不停止、不删除任何其他容器、镜像、卷或网络，不做任何 prune。
set -euo pipefail

PROJECT=opsnap-it
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
COMPOSE_FILE="$ROOT/test/integration/compose.yaml"
ENV_FILE="$ROOT/.it-env" # 本次运行生成的一次性密码，清理时删除
IMAGES=(opsnap-it/runner:latest opsnap-it/sshd:latest opsnap-it/opsnap:latest) # runner 以 opsnap 为基础，先删
SERVICES=(opsnap socks jump target sftp mysql mysql-scratch postgres postgres-scratch redis redis-scratch mongo mongo-scratch)

compose() { docker compose -p "$PROJECT" -f "$COMPOSE_FILE" "$@"; }
log() { printf '\n==> %s\n' "$*"; }

# rand 生成 n 个（偶数）十六进制字符的一次性密码
rand() { od -An -N"$(($1 / 2))" -tx1 /dev/urandom | tr -d ' \n'; }

load_env() {
	if [[ ! -f "$ENV_FILE" ]]; then
		(
			umask 077
			{
				echo "IT_SECRET_KEY=$(rand 64)"
				echo "IT_ADMIN_PASSWORD=$(rand 24)"
				echo "IT_SOCKS_USER=itproxy"
				echo "IT_SOCKS_PASSWORD=$(rand 24)"
				echo "IT_JUMP_PASSWORD=$(rand 24)"
				echo "IT_TARGET_PASSWORD=$(rand 24)"
				echo "IT_SFTP_PASSWORD=$(rand 24)"
				echo "IT_MYSQL_PASSWORD=$(rand 24)"
				echo "IT_PG_PASSWORD=$(rand 24)"
				echo "IT_REDIS_PASSWORD=$(rand 24)"
				echo "IT_MONGO_PASSWORD=$(rand 24)"
			} >"$ENV_FILE"
		)
	fi
	set -a
	# shellcheck disable=SC1090
	. "$ENV_FILE"
	set +a
}

# build_one 构建一个 opsnap-it/* 镜像；拉取基础镜像元数据偶尔会因网络超时失败，最多试 3 次
build_one() {
	local tag=$1 i
	shift
	log "构建 $tag"
	for i in 1 2 3; do
		docker build --progress=plain -t "$tag" "$@" && return 0
		log "构建 $tag 第 $i 次失败"
		sleep 20
	done
	return 1
}

build() {
	build_one opsnap-it/opsnap:latest -f "$ROOT/Dockerfile" "$ROOT"
	build_one opsnap-it/sshd:latest "$ROOT/test/integration/sshd"
	build_one opsnap-it/runner:latest -f "$ROOT/test/integration/runner.Dockerfile" "$ROOT"
}

up() {
	load_env
	log "启动 $PROJECT"
	compose up -d --no-build "${SERVICES[@]}"
	compose ps
}

# status 打印 opsnap 容器里的运行用户与各工具版本（与 runner 里 TestImageTools 的检查对照）
status() {
	compose exec -T opsnap sh -c 'echo "uid=$(id -u) user=$(id -un)"; restic version; rclone version | head -1; rsync --version | head -1; ssh -V 2>&1; mysqldump --version; mariadb-dump --version; pg_dump --version; mongodump --version | head -1; redis-cli --version'
}

run_tests() {
	local phase=$1 pattern=$2
	load_env
	log "测试阶段 $phase"
	IT_PHASE=$phase compose --profile test run --rm --no-deps runner \
		-test.v -test.count=1 -test.timeout=45m -test.run "$pattern"
}

# rekey 用新的主机密钥重建 target：主机密钥在容器启动时生成，强制重建即换指纹
rekey() {
	load_env
	log "重建 target（新的主机密钥）"
	compose up -d --no-build --no-deps --force-recreate target
}

down() {
	log "清理 $PROJECT（只删除本项目的容器、网络、卷和 opsnap-it/* 镜像）"
	if [[ -f "$ENV_FILE" ]]; then load_env; else
		# compose 文件里的 ${VAR:?} 需要变量存在才能解析；清理时给占位值
		for v in IT_SECRET_KEY IT_ADMIN_PASSWORD IT_SOCKS_USER IT_SOCKS_PASSWORD IT_JUMP_PASSWORD IT_TARGET_PASSWORD \
			IT_SFTP_PASSWORD IT_MYSQL_PASSWORD IT_PG_PASSWORD IT_REDIS_PASSWORD IT_MONGO_PASSWORD; do export "$v=teardown"; done
	fi
	compose --profile test down -v --remove-orphans || true
	for img in "${IMAGES[@]}"; do
		if docker image inspect "$img" >/dev/null 2>&1; then docker image rm "$img" || true; fi
	done
	rm -f "$ENV_FILE"
	log "剩余的 $PROJECT 资源（应为空）"
	docker ps -a --filter "label=com.docker.compose.project=$PROJECT" --format '{{.Names}}'
	docker volume ls --filter "label=com.docker.compose.project=$PROJECT" --format '{{.Name}}'
	docker network ls --filter "label=com.docker.compose.project=$PROJECT" --format '{{.Name}}'
	docker images --filter "reference=opsnap-it/*" --format '{{.Repository}}:{{.Tag}}'
}

all() {
	local rc=0
	trap down EXIT
	build
	up
	status
	run_tests main '^(TestImageTools|TestIntegration)$' || rc=$?
	rekey
	run_tests rekeyed '^TestHostKeyChanged$' || rc=$?
	if ((rc == 0)); then log "集成测试全部通过"; else log "集成测试失败（退出码 $rc）"; fi
	return "$rc"
}

case "${1:-all}" in
all) all ;;
build) build ;;
up) up ;;
status) status ;;
test) run_tests main '^(TestImageTools|TestIntegration)$' ;;
rekey) rekey ;;
test-rekeyed) run_tests rekeyed '^TestHostKeyChanged$' ;;
down) down ;;
*)
	echo "用法：$0 [all|build|up|status|test|rekey|test-rekeyed|down]" >&2
	exit 2
	;;
esac
