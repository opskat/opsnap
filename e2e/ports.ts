/** 冒烟 e2e 专用端口，避开开发实例默认的 8210 */
export const SMOKE_PORT = 18291;

/** 冒烟 e2e 中假 OIDC 提供方（bin/fakeidp）的端口 */
export const FAKE_IDP_PORT = 18292;
export const FAKE_IDP_ISSUER = `http://127.0.0.1:${FAKE_IDP_PORT}`;
export const FAKE_IDP_CLIENT_ID = "opsnap";
export const FAKE_IDP_CLIENT_SECRET = "fake-secret";
