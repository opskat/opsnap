import { createBrowserRouter, RouterProvider } from "react-router";

import { AuthGate } from "@/components/auth/AuthGate";
import { AppShell } from "@/components/layout/AppShell";
import { mainNav, systemNav } from "@/components/layout/nav";
import { ComingSoonPage } from "@/pages/ComingSoonPage";
import { JobsPage } from "@/pages/JobsPage";
import { JobWizardPage } from "@/pages/JobWizardPage";
import { LoginPage } from "@/pages/LoginPage";
import { OverviewPage } from "@/pages/OverviewPage";
import { SettingsPage } from "@/pages/SettingsPage";
import { SetupPage } from "@/pages/SetupPage";
import { SourceDetailPage } from "@/pages/SourceDetailPage";
import { SourcesPage } from "@/pages/SourcesPage";
import { StoragePage } from "@/pages/StoragePage";

const router = createBrowserRouter([
  { path: "/setup", element: <SetupPage /> },
  { path: "/login", element: <LoginPage /> },
  {
    element: <AuthGate />,
    children: [
      {
        element: <AppShell />,
        children: [
          { index: true, element: <OverviewPage /> },
          { path: "/settings", element: <SettingsPage /> },
          { path: "/storage", element: <StoragePage /> },
          { path: "/sources", element: <SourcesPage /> },
          { path: "/sources/:id", element: <SourceDetailPage /> },
          { path: "/jobs", element: <JobsPage /> },
          { path: "/jobs/new", element: <JobWizardPage /> },
          { path: "/jobs/:id/edit", element: <JobWizardPage /> },
          // 任务详情页在后续任务中落地；创建 / 编辑保存后先跳转到这里，保持导航完整
          { path: "/jobs/:id", element: <ComingSoonPage title="nav.jobs" /> },
          ...[...mainNav, ...systemNav]
            .filter((item) => !["/", "/settings", "/storage", "/sources", "/jobs"].includes(item.path))
            .map((item) => ({ path: item.path, element: <ComingSoonPage title={item.label} /> })),
        ],
      },
    ],
  },
]);

export function App() {
  return <RouterProvider router={router} />;
}
