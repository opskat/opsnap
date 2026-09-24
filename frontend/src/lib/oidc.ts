import { request } from "@/lib/api";

/** 回调地址的固定路径，与后端 oidc_svc.CallbackPath 一致 */
export const OIDC_CALLBACK_PATH = "/api/v1/auth/oidc/callback";

export interface OidcBinding {
  subject: string;
  display: string;
  bound_at: number;
  last_login_at: number;
}

export interface OidcConfig {
  configured: boolean;
  display_name: string;
  issuer: string;
  client_id: string;
  has_secret: boolean;
  scopes: string[] | null;
  redirect_url: string;
  binding: OidcBinding | null;
  password_login: boolean;
  /** 已绑定且通过 OIDC 登录过时才允许关闭密码登录 */
  can_disable_password_login: boolean;
}

export interface SaveOidcConfig {
  display_name: string;
  issuer: string;
  client_id: string;
  /** 留空表示不修改 */
  client_secret: string;
  scopes: string[];
  redirect_url: string;
  confirm_reset: boolean;
}

/** 回调或发起失败时跳回页面带的 oidc_error，与后端 oidc_svc.Err* 一致 */
export type OidcErrorKind = "not_bound" | "idp" | "invalid" | "unreachable" | "already_bound";

export function getOidcConfig() {
  return request<OidcConfig>("/auth/oidc/config");
}

export function saveOidcConfig(body: SaveOidcConfig) {
  return request<OidcConfig>("/auth/oidc/config", { method: "PUT", body: JSON.stringify(body) });
}

export function setPasswordLogin(enabled: boolean) {
  return request<{ password_login: boolean }>("/auth/password-login", {
    method: "PUT",
    body: JSON.stringify({ enabled }),
  });
}

export function unbindOidc() {
  return request<object>("/auth/oidc/unbind", { method: "POST" });
}

/** 浏览器跳转地址：发起 OIDC 登录 */
export function oidcLoginURL(next: string) {
  return `/api/v1/auth/oidc/login?next=${encodeURIComponent(next)}`;
}

/** 浏览器跳转地址：发起绑定 */
export const OIDC_BIND_URL = "/api/v1/auth/oidc/bind";

/** 当前访问地址对应的回调地址 */
export function currentRedirectURL() {
  return window.location.origin + OIDC_CALLBACK_PATH;
}
