import { createBrowserRouter, RouterProvider } from "react-router";

import { AuthGate } from "@/components/auth/AuthGate";
import { AppShell } from "@/components/layout/AppShell";
import { mainNav, systemNav } from "@/components/layout/nav";
import { ComingSoonPage } from "@/pages/ComingSoonPage";
import { LoginPage } from "@/pages/LoginPage";
import { OverviewPage } from "@/pages/OverviewPage";
import { SettingsPage } from "@/pages/SettingsPage";
import { SetupPage } from "@/pages/SetupPage";
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
          ...[...mainNav, ...systemNav]
            .filter((item) => !["/", "/settings", "/storage"].includes(item.path))
            .map((item) => ({ path: item.path, element: <ComingSoonPage title={item.label} /> })),
        ],
      },
    ],
  },
]);

export function App() {
  return <RouterProvider router={router} />;
}
