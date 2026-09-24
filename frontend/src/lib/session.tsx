import { createContext, use } from "react";

import type { Me } from "@/lib/auth";

export interface SessionValue {
  user: Me;
  logout: () => Promise<void>;
}

export const SessionContext = createContext<SessionValue | null>(null);

/** 当前登录的管理员；只能在 AuthGate 之内使用 */
export function useSession() {
  const ctx = use(SessionContext);
  if (!ctx) throw new Error("useSession 必须在 AuthGate 内使用");
  return ctx;
}
