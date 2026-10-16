import * as React from "react";

/** 媒体查询是否命中，窗口大小变化时跟着更新 */
export function useMediaQuery(query: string): boolean {
  const subscribe = React.useCallback(
    (onChange: () => void) => {
      const m = window.matchMedia(query);
      m.addEventListener("change", onChange);
      return () => m.removeEventListener("change", onChange);
    },
    [query]
  );
  return React.useSyncExternalStore(
    subscribe,
    () => window.matchMedia(query).matches,
    () => false
  );
}

/** 窄屏：宽度小于 768px，概览页改用 14 天运行带 */
export const NARROW_QUERY = "(max-width: 767px)";
