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
