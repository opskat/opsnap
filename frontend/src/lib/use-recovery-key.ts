import * as React from "react";
import { api, errorMessage } from "./api";

interface State {
  value: string | null;
  error: string | null;
  acked: boolean;
}

const EMPTY: State = { value: null, error: null, acked: false };

/**
 * 恢复密钥「先显示、再确认」的流程。active 为真时向后端取一把新密钥（不保存）；
 * 变为假时丢弃密钥和确认状态，再次变真会取新的一把。
 * ready 只在密钥已显示且管理员勾选「已存进密码管理器」后为真。
 */
export function useRecoveryKey(active: boolean) {
  const [state, setState] = React.useState<State>(EMPTY);
  const [tick, setTick] = React.useState(0);

  // active 或重试变化时丢弃上一把密钥：在渲染期间对比，避免在 effect 里同步 setState
  const [prev, setPrev] = React.useState({ active, tick });
  if (prev.active !== active || prev.tick !== tick) {
    setPrev({ active, tick });
    setState(EMPTY);
  }

  React.useEffect(() => {
    if (!active) return;
    let cancelled = false;
    api.repos
      .newRecoveryKey()
      .then((r) => {
        if (!cancelled) setState({ value: r.recovery_key_value, error: null, acked: false });
      })
      .catch((err) => {
        if (!cancelled) setState({ value: null, error: errorMessage(err), acked: false });
      });
    return () => {
      cancelled = true;
    };
  }, [active, tick]);

  return {
    value: state.value,
    error: state.error,
    acked: state.acked,
    ready: active && state.value !== null && state.acked,
    setAcked: (acked: boolean) => setState((s) => ({ ...s, acked })),
    retry: () => setTick((n) => n + 1),
  };
}
