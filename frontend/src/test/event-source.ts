import { act } from "@testing-library/react";
import { vi } from "vitest";

type Listener = (ev: MessageEvent | Event) => void;

/** 测试用的 EventSource：由测试手动推送事件 */
export class FakeEventSource {
  static instances: FakeEventSource[] = [];
  static last(): FakeEventSource {
    return FakeEventSource.instances[FakeEventSource.instances.length - 1];
  }

  url: string;
  closed = false;
  private listeners = new Map<string, Listener[]>();

  constructor(url: string | URL) {
    this.url = String(url);
    FakeEventSource.instances.push(this);
  }

  addEventListener(type: string, fn: Listener) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), fn]);
  }
  removeEventListener(type: string, fn: Listener) {
    this.listeners.set(
      type,
      (this.listeners.get(type) ?? []).filter((f) => f !== fn)
    );
  }
  close() {
    this.closed = true;
  }

  /** 推送一个事件；data 会被序列化成 JSON */
  emit(type: string, data?: unknown) {
    const ev = data === undefined ? new Event(type) : new MessageEvent(type, { data: JSON.stringify(data) });
    act(() => {
      for (const fn of this.listeners.get(type) ?? []) fn(ev);
    });
  }
}

/** 用 FakeEventSource 替换全局 EventSource（测试结束时由 setup 还原） */
export function stubEventSource() {
  FakeEventSource.instances = [];
  vi.stubGlobal("EventSource", FakeEventSource);
  return FakeEventSource;
}
