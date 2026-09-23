import { createContext, use, useEffect, useState, type ReactNode } from "react";

export type Theme = "light" | "dark" | "system";

const STORAGE_KEY = "opsnap-theme";

interface ThemeContextValue {
  theme: Theme;
  setTheme: (theme: Theme) => void;
}

const ThemeContext = createContext<ThemeContextValue | null>(null);

function readStoredTheme(): Theme {
  try {
    const value = localStorage.getItem(STORAGE_KEY);
    if (value === "light" || value === "dark" || value === "system") return value;
  } catch {
    // 隐私模式等场景下读取会失败，退回跟随系统
  }
  return "system";
}

function isDark(theme: Theme) {
  if (theme === "system") return window.matchMedia("(prefers-color-scheme: dark)").matches;
  return theme === "dark";
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setThemeState] = useState<Theme>(readStoredTheme);

  useEffect(() => {
    const apply = () => document.documentElement.classList.toggle("dark", isDark(theme));
    apply();
    if (theme !== "system") return;
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    media.addEventListener("change", apply);
    return () => media.removeEventListener("change", apply);
  }, [theme]);

  const setTheme = (next: Theme) => {
    setThemeState(next);
    try {
      localStorage.setItem(STORAGE_KEY, next);
    } catch {
      // 无法持久化时只在本次会话生效
    }
  };

  return <ThemeContext value={{ theme, setTheme }}>{children}</ThemeContext>;
}

// eslint-disable-next-line react-refresh/only-export-components -- hook 与 Provider 同属主题模块
export function useTheme() {
  const ctx = use(ThemeContext);
  if (!ctx) throw new Error("useTheme 必须在 ThemeProvider 内使用");
  return ctx;
}
