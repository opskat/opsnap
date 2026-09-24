import { Monitor, Moon, Sun } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Button } from "@/components/ui/button";
import { changeLanguage, type Language } from "@/i18n";
import { useTheme, type Theme } from "@/lib/theme";
import { cn } from "@/lib/utils";

const themeOptions: { value: Theme; icon: typeof Sun }[] = [
  { value: "light", icon: Sun },
  { value: "dark", icon: Moon },
  { value: "system", icon: Monitor },
];

const languageOptions: { value: Language; label: string }[] = [
  { value: "zh-CN", label: "中文" },
  { value: "en", label: "EN" },
];

/** 主题与语言切换；侧栏里纵向排列，登录与首次设置页里横向排列 */
export function PreferenceToggles({ className }: { className?: string }) {
  const { t, i18n } = useTranslation();
  const { theme, setTheme } = useTheme();
  return (
    <div className={cn("flex gap-2", className)}>
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
  );
}
