/** 冒烟 e2e 专用端口，避开开发实例默认的 8210 */
export const SMOKE_PORT = 18291;

/** 冒烟 e2e 中假 OIDC 提供方（bin/fakeidp）的端口 */
export const FAKE_IDP_PORT = 18292;
export const FAKE_IDP_ISSUER = `http://127.0.0.1:${FAKE_IDP_PORT}`;
export const FAKE_IDP_CLIENT_ID = "opsnap";
export const FAKE_IDP_CLIENT_SECRET = "fake-secret";

/** 冒烟 e2e 中假 SSH 服务端（bin/fakessh，见 sources.spec.ts）：一台作跳板，一台作服务器文件目标 */
export const FAKE_SSH_JUMP_PORT = 18296;
/** 跳板的控制端口：POST /rotate-host-key 换一把新的主机密钥，模拟“主机密钥已变化” */
export const FAKE_SSH_JUMP_CONTROL_PORT = 18297;
export const FAKE_SSH_TARGET_PORT = 18298;
export const FAKE_SSH_USER = "opsnap";
export const FAKE_SSH_PASSWORD = "fake-ssh-password";
