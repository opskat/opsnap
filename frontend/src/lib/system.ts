import { request } from "@/lib/api";

export interface Health {
  version: string;
  database: "ok" | "error";
}

export function getHealth() {
  return request<Health>("/system/health");
}
