import { BrowserRouter, Navigate, Outlet, Route, Routes } from "react-router";
import { AppProviders } from "@/app/providers";
import { ShellStatus } from "@/components/shell-status";
import { RequireAuth } from "@/lib/auth";
import { JobsRoutes } from "@/pages/job-form";
import { LoginPage } from "@/pages/login";
import { NotifyPage } from "@/pages/notify";
import { OverviewPage } from "@/pages/overview";
import { ReposPage } from "@/pages/repos";
import { RunDetailPage, RunsPage } from "@/pages/runs";
import { ServersPage } from "@/pages/servers";
import { SettingsPage } from "@/pages/settings";
import { SetupPage } from "@/pages/setup";
import { SnapshotsPage } from "@/pages/snapshots";

/** 需要登录、带侧边栏的页面 */
function ShellLayout() {
  return (
    <RequireAuth>
      <ShellStatus>
        <Outlet />
      </ShellStatus>
    </RequireAuth>
  );
}

/** 面板路由 */
export function AppRoutes() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route path="/setup" element={<SetupPage />} />
      <Route element={<ShellLayout />}>
        <Route index element={<OverviewPage />} />
        <Route path="jobs/*" element={<JobsRoutes />} />
        <Route path="snapshots/*" element={<SnapshotsPage />} />
        <Route path="runs" element={<RunsPage />} />
        <Route path="runs/:id" element={<RunDetailPage />} />
        <Route path="servers/*" element={<ServersPage />} />
        <Route path="repos/*" element={<ReposPage />} />
        <Route path="notify" element={<NotifyPage />} />
        <Route path="settings" element={<SettingsPage />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  );
}

export function App() {
  return (
    <BrowserRouter>
      <AppProviders>
        <AppRoutes />
      </AppProviders>
    </BrowserRouter>
  );
}
