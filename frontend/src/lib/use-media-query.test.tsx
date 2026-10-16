import { describe, expect, it, vi } from "vitest";
import { act, render, screen } from "@testing-library/react";
import { NARROW_QUERY, useMediaQuery } from "./use-media-query";

function Probe() {
  return <p>{useMediaQuery(NARROW_QUERY) ? "窄屏" : "宽屏"}</p>;
}

describe("useMediaQuery", () => {
  it("命中时为真，窗口大小变化后更新", () => {
    let matches = false;
    let notify: () => void = () => {};
    vi.spyOn(window, "matchMedia").mockImplementation(
      () =>
        ({
          get matches() {
            return matches;
          },
          addEventListener: (_: string, cb: () => void) => (notify = cb),
          removeEventListener: () => {},
        }) as unknown as MediaQueryList
    );
    render(<Probe />);
    expect(screen.getByText("宽屏")).toBeInTheDocument();
    act(() => {
      matches = true;
      notify();
    });
    expect(screen.getByText("窄屏")).toBeInTheDocument();
  });
});
