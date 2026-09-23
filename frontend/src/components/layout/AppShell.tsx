import { Aperture, Monitor, Moon, Sun } from "lucide-react";
import { useTranslation } from "react-i18next";
import { NavLink, Outlet } from "react-router";

import { Button } from "@/components/ui/button";
import { changeLanguage, type Language } from "@/i18n";
import { useTheme, type Theme } from "@/lib/theme";
import { cn } from "@/lib/utils";

import { mainNav, systemNav, type NavItem } from "./nav";

const themeOptions: { value: Theme; icon: typeof Sun }[] = [
  { value: "light", icon: Sun },
  { value: "dark", icon: Moon },
  { value: "system", icon: Monitor },
];

const languageOptions: { value: Language; label: string }[] = [
  { value: "zh-CN", label: "中文" },
  { value: "en", label: "EN" },
];

function NavEntry({ item }: { item: NavItem }) {
  const { t } = useTranslation();
  const Icon = item.icon;
  return (
    <NavLink
      to={item.path}
      end={item.path === "/"}
      className={({ isActive }) =>
        cn(
          "flex items-center gap-2.5 rounded-md px-2.5 py-2 text-[13.5px] transition-colors",
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
  const { t, i18n } = useTranslation();
  const { theme, setTheme } = useTheme();

  return (
    <div className="flex h-screen">
      <aside className="flex w-58 shrink-0 flex-col gap-0.5 border-r bg-sidebar px-3 py-5">
        <div className="flex items-center gap-2.5 px-2 pb-5">
          <span className="flex size-7 items-center justify-center rounded-md bg-brand text-brand-foreground">
            <Aperture className="size-4" />
          </span>
          <span className="text-[17px] font-bold text-sidebar-foreground">OpsNap</span>
        </div>
        <nav className="flex flex-col gap-0.5" aria-label="main">
          {mainNav.map((item) => (
            <NavEntry key={item.path} item={item} />
          ))}
          <div className="px-2.5 pt-4 pb-1.5 text-[11px] tracking-wide text-faint-foreground">{t("nav.system")}</div>
          {systemNav.map((item) => (
            <NavEntry key={item.path} item={item} />
          ))}
        </nav>
        <div className="mt-auto flex flex-col gap-2 border-t pt-3">
          <div className="flex gap-1" role="group" aria-label={t("theme.label")}>
            {themeOptions.map(({ value, icon: Icon }) => (
              <Button
                key={value}
                variant={theme === value ? "secondary" : "ghost"}
                size="icon-sm"
                aria-pressed={theme === value}
                aria-label={t(`theme.${value}`)}
                title={t(`theme.${value}`)}
                onClick={() => setTheme(value)}
              >
                <Icon />
              </Button>
            ))}
          </div>
          <div className="flex gap-1" role="group" aria-label={t("language.label")}>
            {languageOptions.map(({ value, label }) => (
              <Button
                key={value}
                variant={i18n.language === value ? "secondary" : "ghost"}
                size="sm"
                aria-pressed={i18n.language === value}
                onClick={() => void changeLanguage(value)}
              >
                {label}
              </Button>
            ))}
          </div>
        </div>
      </aside>
      <main className="min-w-0 flex-1 overflow-auto">
        <Outlet />
      </main>
    </div>
  );
}
