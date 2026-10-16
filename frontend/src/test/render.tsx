import * as React from "react";
import { render } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { AppProviders } from "@/app/providers";

/** 把当前地址渲染出来，供断言跳转 */
export function LocationProbe() {
  const loc = useLocation();
  return <div data-testid="location">{loc.pathname + loc.search}</div>;
}

/**
 * 在完整的 Provider（主题、登录态、Tooltip、Toaster）和内存路由里渲染页面。
 * path 是页面挂载的路由，at 是初始地址。
 */
export function renderPage(ui: React.ReactElement, { path = "/", at = path }: { path?: string; at?: string } = {}) {
  return render(
    <MemoryRouter initialEntries={[at]}>
      <AppProviders>
        <Routes>
          <Route path={path} element={ui} />
          <Route path="*" element={null} />
        </Routes>
        <LocationProbe />
      </AppProviders>
    </MemoryRouter>
  );
}
