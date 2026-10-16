import * as React from "react";
import { errorMessage } from "./api";

/**
 * 页面读取数据：返回数据、加载中、错误说明和重试。
 * 重新读取时保留旧数据，避免列表闪烁。
 */
export function useLoad<T>(fetcher: () => Promise<T>) {
  const [data, setData] = React.useState<T | undefined>(undefined);
  const [error, setError] = React.useState<string | null>(null);
  const [loading, setLoading] = React.useState(true);
  const [tick, setTick] = React.useState(0);

  // fetcher 或 tick 变化时进入加载中：在渲染期间对比，避免在 effect 里同步 setState
  const [prevKey, setPrevKey] = React.useState({ fetcher, tick });
  if (prevKey.fetcher !== fetcher || prevKey.tick !== tick) {
    setPrevKey({ fetcher, tick });
    setLoading(true);
  }

  React.useEffect(() => {
    let cancelled = false;
    fetcher()
      .then((d) => {
        if (cancelled) return;
        setData(d);
        setError(null);
      })
      .catch((err) => {
        if (!cancelled) setError(errorMessage(err));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [fetcher, tick]);

  const reload = React.useCallback(() => setTick((n) => n + 1), []);
  return { data, error, loading, reload, setData };
}
