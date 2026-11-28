import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import type { Locale } from "@/types";
import { dictionaries, setActiveLocale } from "./active";
import type { Translations } from "./locales/en";

export type { Translations };

const STORAGE_KEY = "bms-locale";
/** The key used before the rename. Its value is moved across once, then removed. */
const LEGACY_STORAGE_KEY = "template-locale";

function readStoredLocale(): Locale {
  try {
    const legacy = localStorage.getItem(LEGACY_STORAGE_KEY);
    if (legacy !== null) {
      if (localStorage.getItem(STORAGE_KEY) === null) localStorage.setItem(STORAGE_KEY, legacy);
      localStorage.removeItem(LEGACY_STORAGE_KEY);
    }
    return localStorage.getItem(STORAGE_KEY) === "sw" ? "sw" : "en";
  } catch {
    return "en";
  }
}

interface I18nContextType {
  locale: Locale;
  setLocale: (locale: Locale) => void;
  t: Translations;
  routeLabel: (path: string, fallback: string) => string;
  groupLabel: (group: string) => string;
}

const makeHelpers = (locale: Locale) => ({
  routeLabel: (path: string, fallback: string) =>
    (dictionaries[locale].nav.routes as Record<string, string>)[path] ?? fallback,
  groupLabel: (group: string) =>
    (dictionaries[locale].nav.groups as Record<string, string>)[group] ?? group,
});

const defaultI18nValue: I18nContextType = {
  locale: "en",
  setLocale: () => {},
  t: dictionaries.en,
  ...makeHelpers("en"),
};

const I18nContext = createContext<I18nContextType | null>(null);

export function I18nProvider({
  children,
  fixedLocale,
}: {
  children: ReactNode;
  /** For pages without a signed-in user: use this language and leave the saved choice alone. */
  fixedLocale?: Locale;
}) {
  const [saved, setLocaleState] = useState<Locale>(readStoredLocale);
  const locale = fixedLocale ?? saved;
  // Set before the children render, so formatting and API errors use the same language.
  setActiveLocale(locale);

  const setLocale = (next: Locale) => {
    setLocaleState(next);
    try {
      localStorage.setItem(STORAGE_KEY, next);
    } catch {
      /* ignore */
    }
  };

  useEffect(() => {
    if (typeof document !== "undefined") {
      document.documentElement.lang = locale;
    }
  }, [locale]);

  const helpers = makeHelpers(locale);

  return (
    <I18nContext.Provider value={{ locale, setLocale, t: dictionaries[locale], ...helpers }}>
      {children}
    </I18nContext.Provider>
  );
}

export function useI18n() {
  const context = useContext(I18nContext);
  return context ?? defaultI18nValue;
}
