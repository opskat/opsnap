#!/usr/bin/env bash
# 集成测试入口（make it）：把构建上下文打包，经 opsctl 传到集成测试主机的专用目录，
# 在那里执行 scripts/it-remote.sh，结束后删除远端构建目录。
#
#   scripts/it.sh [all|build|up|status|test|rekey|test-rekeyed|down]   默认 all
#
# 环境变量：
#   OPSNAP_IT_ASSET   opsctl 资产 ID，默认 4
#   OPSNAP_IT_SOURCE  worktree（默认：工作区里已跟踪和未忽略的文件）| head（HEAD 提交，
#                     再叠加工作区里的 Dockerfile、.dockerignore、test/integration、scripts）
#
# 远端只会出现 /root/opsnap-it-build 目录、compose 项目 opsnap-it 的资源和 opsnap-it/* 镜像，规则见 it-remote.sh。
set -euo pipefail

ASSET=${OPSNAP_IT_ASSET:-4}
SOURCE=${OPSNAP_IT_SOURCE:-worktree}
REMOTE_DIR=/root/opsnap-it-build
STEP=${1:-all}
ROOT=$(git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel)

# existing 只保留工作区里实际存在的路径（git ls-files 会列出已删除但未提交的文件）
existing() { while IFS= read -r -d '' f; do [[ -e "$ROOT/$f" || -L "$ROOT/$f" ]] && printf '%s\0' "$f"; done; }
pack() { (cd "$ROOT" && existing | COPYFILE_DISABLE=1 tar --null -T - -cf -) | tar -xf - -C "$1"; }

# 只有 all 与 build 上传构建上下文；其余分步命令沿用远端已有的目录（含本次生成的一次性密码 .it-env）
upload() {
	stage=$(mktemp -d)
	trap 'rm -rf "$stage"' EXIT
	mkdir -p "$stage/ctx"

	case "$SOURCE" in
	worktree)
		git -C "$ROOT" ls-files -z --cached --others --exclude-standard | pack "$stage/ctx"
		;;
	head)
		git -C "$ROOT" archive --format=tar HEAD | tar -xf - -C "$stage/ctx"
		git -C "$ROOT" ls-files -z --cached --others --exclude-standard -- Dockerfile .dockerignore test/integration scripts | pack "$stage/ctx"
		;;
	*)
		echo "OPSNAP_IT_SOURCE 只能是 worktree 或 head" >&2
		exit 2
		;;
	esac
	COPYFILE_DISABLE=1 tar -czf "$stage/ctx.tgz" -C "$stage/ctx" .

	echo "==> 上传构建上下文到资产 $ASSET:$REMOTE_DIR（$(du -h "$stage/ctx.tgz" | cut -f1)）"
	# 保留已有的 .it-env：环境还在运行时重新构建，密码要与运行中的容器一致
	opsctl exec "$ASSET" --type ssh -- "mkdir -p $REMOTE_DIR && find $REMOTE_DIR -mindepth 1 -maxdepth 1 ! -name .it-env -exec rm -rf {} +"
	opsctl cp "$stage/ctx.tgz" "$ASSET:$REMOTE_DIR/ctx.tgz"
	opsctl exec "$ASSET" --type ssh -- "tar -xzf $REMOTE_DIR/ctx.tgz -C $REMOTE_DIR && rm -f $REMOTE_DIR/ctx.tgz"
}

case "$STEP" in all | build) upload ;; esac

rc=0
opsctl exec "$ASSET" --type ssh -- "bash $REMOTE_DIR/scripts/it-remote.sh $STEP" || rc=$?

# all 和 down 结束时远端已清理完 opsnap-it 资源，再删掉构建目录
if [[ "$STEP" == all || "$STEP" == down ]]; then
	opsctl exec "$ASSET" --type ssh -- "rm -rf $REMOTE_DIR"
fi
exit "$rc"
