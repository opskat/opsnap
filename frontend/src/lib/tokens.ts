import { request } from "@/lib/api";

export type TokenStatus = "active" | "expired" | "revoked";

export interface ApiToken {
  id: number;
  name: string;
  prefix: string;
  status: TokenStatus;
  /** 以下均为 Unix 秒；last_used_at 为 0 表示从未使用，expires_at 为 0 表示永不过期 */
  created_at: number;
  last_used_at: number;
  expires_at: number;
  revoked_at: number;
}

/** 可选有效期（天），0 表示永不过期；默认 90 天 */
export const EXPIRY_OPTIONS = [30, 90, 365, 0] as const;
export const DEFAULT_EXPIRY = 90;

export function listTokens() {
  return request<{ items: ApiToken[] }>("/tokens");
}

export function createToken(body: { name: string; expires_in_days: number }) {
  return request<{ item: ApiToken; token: string }>("/tokens", { method: "POST", body: JSON.stringify(body) });
}

export function revokeToken(id: number) {
  return request<object>(`/tokens/${id}/revoke`, { method: "POST" });
}
