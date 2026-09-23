import i18n from "i18next";
import { initReactI18next } from "react-i18next";

import en from "./locales/en.json";
import zhCN from "./locales/zh-CN.json";

export const LANGUAGES = ["zh-CN", "en"] as const;
export type Language = (typeof LANGUAGES)[number];

const STORAGE_KEY = "opsnap-lang";

function detectLanguage(): Language {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored === "zh-CN" || stored === "en") return stored;
  } catch {
    // 读取失败时按浏览器语言判断
  }
  return navigator.language.toLowerCase().startsWith("zh") ? "zh-CN" : "en";
}

export function changeLanguage(lang: Language) {
  try {
    localStorage.setItem(STORAGE_KEY, lang);
  } catch {
    // 无法持久化时只在本次会话生效
  }
  document.documentElement.lang = lang;
  return i18n.changeLanguage(lang);
}

void i18n.use(initReactI18next).init({
  resources: { "zh-CN": { translation: zhCN }, en: { translation: en } },
  lng: detectLanguage(),
  fallbackLng: "zh-CN",
  interpolation: { escapeValue: false },
});
document.documentElement.lang = i18n.language;

export default i18n;
