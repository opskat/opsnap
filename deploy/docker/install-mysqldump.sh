#!/usr/bin/env bash
# 从 MySQL 官方二进制包取出 mysqldump，放到 <目标目录>/mysql-<主>.<次>/bin（spec「目录约定」）。
# 在 Dockerfile 的 mysql 构建阶段执行，运行在目标架构上。
# 用法：install-mysqldump.sh <版本，如 8.4.11> <目标目录> <MySQL 发布公钥文件>
#
# - x86_64 用官方 linux-glibc 的 minimal 包（已去掉调试符号，约 60–80MB）；
#   aarch64 官方没有 minimal 包，只能下载完整包（约 0.9GB），边下载边解出需要的文件，再去掉调试符号。
# - 包的签名用仓库里固定的 MySQL 发布公钥校验，并核对签名者指纹，校验不过则构建失败。
# - 只保留 mysqldump 与它从 lib/private 加载的 OpenSSL（有的包自带，有的用系统的 libssl3）。
set -euo pipefail

version="$1"
dest="$2"
keyfile="$3"
# MySQL Release Engineering <mysql-build@oss.oracle.com>
fingerprint="BCA43417C3B485DD128EC6D4B7B3B788A8D3785C"

series="${version%.*}"
arch="$(uname -m)"
case "$arch" in
x86_64)
  # 8.0 的 minimal 包只有 glibc2.17 版本
  if [ "$series" = "8.0" ]; then glibc=2.17; else glibc=2.28; fi
  name="mysql-${version}-linux-glibc${glibc}-x86_64-minimal"
  ;;
aarch64)
  name="mysql-${version}-linux-glibc2.28-aarch64"
  ;;
*)
  echo "不支持的架构：$arch" >&2
  exit 1
  ;;
esac

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
cd "$work"

# 当前版本在 Downloads 下，出了新的补丁版本后旧版本移到 archives 下
fetched=""
for base in "https://cdn.mysql.com/Downloads/MySQL-${series}" "https://cdn.mysql.com/archives/mysql-${series}"; do
  if curl -fsSL --retry 3 -o pkg.tar.xz.asc "${base}/${name}.tar.xz.asc"; then
    echo "下载 ${base}/${name}.tar.xz"
    # 边下载边解出需要的文件；同时保存整包用于校验签名，校验不过则整个步骤失败
    curl -fsSL --retry 3 "${base}/${name}.tar.xz" | tee pkg.tar.xz |
      tar -xJ --wildcards "${name}/bin/mysqldump" "${name}/lib/private/libssl.so*" "${name}/lib/private/libcrypto.so*" 2>/dev/null || true
    fetched=1
    break
  fi
done
[ -n "$fetched" ] || {
  echo "找不到 ${name}.tar.xz" >&2
  exit 1
}

gpg --dearmor <"$keyfile" >mysql.gpg
if ! gpg --batch --no-default-keyring --keyring ./mysql.gpg --status-fd 1 --verify pkg.tar.xz.asc pkg.tar.xz 2>/dev/null |
  grep -q "^\[GNUPG:\] VALIDSIG ${fingerprint} "; then
  echo "${name}.tar.xz 签名校验失败" >&2
  exit 1
fi
[ -x "${name}/bin/mysqldump" ] || {
  echo "${name}.tar.xz 中没有 bin/mysqldump" >&2
  exit 1
}

out="${dest}/mysql-${series}"
mkdir -p "${out}/bin"
install -m 0755 "${name}/bin/mysqldump" "${out}/bin/mysqldump"
strip --strip-debug "${out}/bin/mysqldump"
# mysqldump 的 RUNPATH 为 $ORIGIN/../lib/private：包里自带 OpenSSL 时一起拷过去
if [ -d "${name}/lib/private" ]; then
  mkdir -p "${out}/lib/private"
  cp -a "${name}/lib/private/." "${out}/lib/private/"
fi
echo "已安装 ${out}/bin/mysqldump（${version}）"
