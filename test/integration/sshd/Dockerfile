# 集成测试用的网络节点镜像（opsnap-it/sshd）：同一个镜像按命令扮演 SSH 主机（跳板、目标、SFTP 服务器）或 SOCKS5 代理。
# 只在 compose 项目 opsnap-it 的内部网络里使用。
FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends openssh-server openssh-sftp-server rsync microsocks \
 && rm -rf /var/lib/apt/lists/* \
 # 主机密钥不放进镜像：每个容器启动时现生成，重建容器就会换成新的主机指纹
 && rm -f /etc/ssh/ssh_host_*
COPY sshd_config /etc/ssh/sshd_config.d/opsnap-it.conf
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod 0755 /usr/local/bin/entrypoint.sh
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["sshd"]
