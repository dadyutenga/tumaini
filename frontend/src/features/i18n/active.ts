/**
 * The language in use, for code that runs outside React: API errors and number and date
 * formatting. Components use `useI18n()`, which re-renders them when the language changes.
 */
import type { Locale } from "@/types";
import en, { type Translations } from "./locales/en";
import sw from "./locales/sw";

export const dictionaries: Record<Locale, Translations> = { en, sw };

const INTL_LOCALES: Record<Locale, string> = { en: "en-TZ", sw: "sw-TZ" };

let active: Locale = "en";

export function setActiveLocale(locale: Locale) {
  active = locale;
}
export const activeLocale = () => active;
export const activeT = () => dictionaries[active];
export const intlLocale = () => INTL_LOCALES[active];
