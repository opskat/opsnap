import * as React from "react";

/** 返回当前时间（毫秒），active 为真时每秒更新一次，用于「已用时」这类实时数字 */
export function useNow(active: boolean): number {
  const [now, setNow] = React.useState(() => Date.now());
  React.useEffect(() => {
    if (!active) return;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [active]);
  return now;
}
