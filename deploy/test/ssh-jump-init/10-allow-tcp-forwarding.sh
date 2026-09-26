#!/usr/bin/env bash
# ssh-jump 要充当数据源运行时验证的跳板，需要转发 TCP 连接，但 linuxserver/openssh-server
# 镜像默认在 /config/sshd/sshd_config 里写 AllowTcpForwarding no。
# custom-cont-init.d 下的脚本以 root 身份、在镜像自身生成 sshd_config 之后、sshd 启动之前执行，
# 用它就地改掉这一项，不用再拉取 DOCKER_MODS（镜像代理常返回 502，拉取不稳定）。
# 幂等：无论当前是 no、yes 还是顶格注释掉，都改成 yes；容器重启会重复执行这个脚本。
# 只匹配顶格的指令，不动文件末尾“#Match User anoncvs”示例块里缩进注释的那一行。
set -euo pipefail

sed -i -E 's/^#?AllowTcpForwarding[[:space:]].*/AllowTcpForwarding yes/' /config/sshd/sshd_config
