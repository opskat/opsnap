import { useState } from "react";

/**
 * 对话框由 value 是否存在决定开关；关闭时 value 变为 undefined，对话框还要播放退场动画。
 * 这里在 value 变为 undefined 后仍返回上一次的值，让退场期间的标题与内容保持不变。
 */
export function useRetained<T>(value: T | undefined): T | undefined {
  const [last, setLast] = useState(value);
  if (value !== undefined && value !== last) setLast(value);
  return value ?? last;
}

/**
 * 对话框每次打开时调用 reset 清空上一次留下的状态。
 * 关闭时不在这里清空，退场动画期间内容保持不变；下次打开时在首次渲染前就已清空，不会闪现旧内容。
 * 含密钥等敏感内容的对话框另在退场结束（DialogContent 的 onCloseAutoFocus）时清空，不把它留在内存里等下次打开。
 */
export function useResetOnOpen(open: boolean, reset: () => void) {
  const [wasOpen, setWasOpen] = useState(open);
  if (open !== wasOpen) {
    setWasOpen(open);
    if (open) reset();
  }
}
