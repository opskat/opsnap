import { createBrowserRouter, RouterProvider } from "react-router";

import { AppShell } from "@/components/layout/AppShell";
import { mainNav, systemNav } from "@/components/layout/nav";
import { ComingSoonPage } from "@/pages/ComingSoonPage";
import { OverviewPage } from "@/pages/OverviewPage";

const router = createBrowserRouter([
  {
    element: <AppShell />,
    children: [
      { index: true, element: <OverviewPage /> },
      ...[...mainNav, ...systemNav]
        .filter((item) => item.path !== "/")
        .map((item) => ({ path: item.path, element: <ComingSoonPage title={item.label} /> })),
    ],
  },
]);

export function App() {
  return <RouterProvider router={router} />;
}
