#!/bin/sh
# sshd：用环境变量 SSH_USER / SSH_PASSWORD 建登录用户（密码经标准输入交给 chpasswd），生成新的主机密钥后前台运行 sshd。
# socks：以 SOCKS_USER / SOCKS_PASSWORD 认证运行 microsocks（microsocks 只能从参数读取凭据；这是测试网络里一次性生成的假密码）。
set -eu
case "${1:-sshd}" in
sshd)
	: "${SSH_USER:?缺少 SSH_USER}" "${SSH_PASSWORD:?缺少 SSH_PASSWORD}"
	id "$SSH_USER" >/dev/null 2>&1 || useradd --create-home --shell /bin/bash "$SSH_USER"
	printf '%s:%s\n' "$SSH_USER" "$SSH_PASSWORD" | chpasswd
	unset SSH_PASSWORD
	ssh-keygen -A >/dev/null
	mkdir -p /run/sshd
	exec /usr/sbin/sshd -D -e
	;;
socks)
	: "${SOCKS_USER:?缺少 SOCKS_USER}" "${SOCKS_PASSWORD:?缺少 SOCKS_PASSWORD}"
	exec microsocks -i 0.0.0.0 -p 1080 -u "$SOCKS_USER" -P "$SOCKS_PASSWORD"
	;;
*)
	exec "$@"
	;;
esac
