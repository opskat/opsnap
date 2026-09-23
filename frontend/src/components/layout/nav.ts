import {
  Bell,
  Camera,
  Database,
  HardDrive,
  LayoutDashboard,
  ListChecks,
  Settings,
  type LucideIcon,
} from "lucide-react";

export interface NavItem {
  path: string;
  /** i18n key */
  label: string;
  icon: LucideIcon;
}

// 导航结构见 docs/specs/2026-09-23-opsnap-v1.md 的「信息架构」
export const mainNav: NavItem[] = [
  { path: "/", label: "nav.overview", icon: LayoutDashboard },
  { path: "/jobs", label: "nav.jobs", icon: ListChecks },
  { path: "/snapshots", label: "nav.snapshots", icon: Camera },
  { path: "/sources", label: "nav.sources", icon: Database },
  { path: "/storage", label: "nav.storage", icon: HardDrive },
];

export const systemNav: NavItem[] = [
  { path: "/notifications", label: "nav.notifications", icon: Bell },
  { path: "/settings", label: "nav.settings", icon: Settings },
];
