import { request } from "@/lib/api";

/** 与后端 internal/pkg/code 一致的错误码 */
export const ErrorCode = {
  Unauthorized: 10001,
  AlreadyInitialized: 10100,
  SetupCodeInvalid: 10101,
  UsernameInvalid: 10102,
  PasswordTooShort: 10103,
  LoginFailed: 10104,
  TooManyAttempts: 10105,
  CurrentPasswordWrong: 10106,
  SessionRequired: 10108,
  TokenNameInvalid: 10203,
  TokenNameDuplicate: 10204,
  OIDCResetConfirmRequired: 10304,
  StorageNameInvalid: 10401,
  StorageNameDuplicate: 10402,
  StoragePathRelative: 10403,
  StorageEndpointScheme: 10405,
  StorageKeyInvalid: 10417,
} as const;

export const MIN_PASSWORD_LENGTH = 12;
export const USERNAME_PATTERN = /^[a-z0-9._-]{3,32}$/;

export interface AuthStatus {
  initialized: boolean;
  /** 是否开启密码登录 */
  password_login: boolean;
  /** 已配置并绑定 OIDC 时可用的登录方式 */
  oidc_login: { display_name: string } | null;
}

export interface Me {
  username: string;
  /** Unix 秒 */
  password_updated_at: number;
  /** 使用 API 令牌访问时为空 */
  session?: { user_agent: string; ip: string; expires_at: number };
}

export function getAuthStatus() {
  return request<AuthStatus>("/auth/status");
}

export function setup(body: { setup_code: string; username: string; password: string }) {
  return request<{ username: string }>("/auth/setup", { method: "POST", body: JSON.stringify(body) });
}

export function login(body: { username: string; password: string }) {
  return request<{ username: string }>("/auth/login", { method: "POST", body: JSON.stringify(body) });
}

export function getMe() {
  return request<Me>("/auth/me");
}

export function changePassword(body: { current_password: string; new_password: string }) {
  return request<object>("/auth/password", { method: "POST", body: JSON.stringify(body) });
}

export function logout() {
  return request<object>("/auth/logout", { method: "POST" });
}

/** 按字符（而不是 UTF-16 码元）计算密码长度，与后端一致 */
export function passwordLength(pw: string) {
  return [...pw].length;
}
