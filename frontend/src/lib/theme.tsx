import * as React from "react";

export type Theme = "light" | "dark";

/** 本地保存主题的键；index.html 里的首屏脚本也读它，改名需同步 */
export const THEME_KEY = "opsnap-theme";

function storage(): Storage | null {
  try {
    return typeof window !== "undefined" && window.localStorage ? window.localStorage : null;
  } catch {
    return null;
  }
}

/** 读取已保存的主题；没有保存过时跟随系统 */
export function readTheme(): Theme {
  const saved = storage()?.getItem(THEME_KEY);
  if (saved === "light" || saved === "dark") return saved;
  return window.matchMedia?.("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

function applyTheme(theme: Theme) {
  document.documentElement.classList.toggle("dark", theme === "dark");
  document.documentElement.style.colorScheme = theme;
}

interface ThemeContextValue {
  theme: Theme;
  setTheme: (t: Theme) => void;
  toggleTheme: () => void;
}

const ThemeContext = React.createContext<ThemeContextValue | null>(null);

export function ThemeProvider({ children }: { children: React.ReactNode }) {
  const [theme, setThemeState] = React.useState<Theme>(readTheme);

  React.useLayoutEffect(() => applyTheme(theme), [theme]);

  const setTheme = React.useCallback((t: Theme) => {
    storage()?.setItem(THEME_KEY, t);
    setThemeState(t);
  }, []);

  const value = React.useMemo(
    () => ({ theme, setTheme, toggleTheme: () => setTheme(theme === "dark" ? "light" : "dark") }),
    [theme, setTheme]
  );
  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

export function useTheme(): ThemeContextValue {
  const ctx = React.useContext(ThemeContext);
  if (!ctx) throw new Error("useTheme 必须在 ThemeProvider 内使用");
  return ctx;
}
