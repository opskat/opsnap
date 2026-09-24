import { Aperture, LogOut, User } from "lucide-react";
import { useTranslation } from "react-i18next";
import { NavLink, Outlet } from "react-router";

import { Button } from "@/components/ui/button";
import { useSession } from "@/lib/session";
import { cn } from "@/lib/utils";

import { mainNav, systemNav, type NavItem } from "./nav";
import { PreferenceToggles } from "./PreferenceToggles";
function NavEntry({ item }: { item: NavItem }) {
  const { t } = useTranslation();
  const Icon = item.icon;
  return (
    <NavLink
      to={item.path}
      end={item.path === "/"}
      className={({ isActive }) =>
        cn(
          "flex items-center gap-2.5 rounded-md px-2.5 py-2 text-sm transition-colors",
          isActive
            ? "bg-accent font-semibold text-sidebar-foreground"
            : "text-faint-foreground hover:bg-accent/60 hover:text-sidebar-foreground"
        )
      }
    >
      <Icon className="size-4" />
      {t(item.label)}
    </NavLink>
  );
}

export function AppShell() {
  const { t } = useTranslation();
  const { user, logout } = useSession();

  return (
    <div className="flex h-screen">
      <aside className="flex w-58 shrink-0 flex-col gap-0.5 border-r bg-sidebar px-3 py-5">
        <div className="flex items-center gap-2.5 px-2 pb-5">
          <span className="flex size-7 items-center justify-center rounded-md bg-brand text-brand-foreground">
            <Aperture className="size-4" />
          </span>
          <span className="text-lg font-bold text-sidebar-foreground">OpsNap</span>
        </div>
        <nav className="flex flex-col gap-0.5" aria-label="main">
          {mainNav.map((item) => (
            <NavEntry key={item.path} item={item} />
          ))}
          <div className="px-2.5 pt-4 pb-1.5 text-2xs tracking-wide text-faint-foreground">{t("nav.system")}</div>
          {systemNav.map((item) => (
            <NavEntry key={item.path} item={item} />
          ))}
        </nav>
        <div className="mt-auto flex flex-col gap-2 border-t pt-3">
          <div className="flex items-center gap-2.5 px-1">
            <span className="flex size-6.5 items-center justify-center rounded-full bg-accent text-muted-foreground">
              <User className="size-3.5" />
            </span>
            <span
              className="min-w-0 flex-1 truncate font-mono text-sm text-sidebar-foreground"
              data-testid="current-user"
            >
              {user.username}
            </span>
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label={t("auth.logout")}
              title={t("auth.logout")}
              onClick={() => void logout()}
            >
              <LogOut />
            </Button>
          </div>
          <PreferenceToggles className="flex-col" />
        </div>
      </aside>
      <main className="min-w-0 flex-1 overflow-auto">
        <Outlet />
      </main>
    </div>
  );
}
