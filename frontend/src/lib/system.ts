import { request } from "@/lib/api";

export interface Health {
  version: string;
  /** 构建时写入的提交短号；未写入时为空 */
  commit: string;
  database: "ok" | "error";
}

export function getHealth() {
  return request<Health>("/system/health");
}
