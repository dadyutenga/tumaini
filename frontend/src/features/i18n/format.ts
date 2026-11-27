import type { Locale } from "@/types";

/** Fills `{name}` placeholders. A placeholder with no value is left as written. */
export function fmt(template: string, values: Record<string, string | number>): string {
  return template.replace(/\{(\w+)\}/g, (placeholder, name: string) =>
    Object.prototype.hasOwnProperty.call(values, name) ? String(values[name]) : placeholder,
  );
}

const pluralRules: Record<Locale, Intl.PluralRules> = {
  en: new Intl.PluralRules("en-TZ"),
  sw: new Intl.PluralRules("sw-TZ"),
};

/**
 * Picks the wording for a count, to be filled with `fmt`:
 * `fmt(plural(locale, n, t.sales.items), { count: n })`.
 */
export function plural(locale: Locale, count: number, forms: { one: string; other: string }) {
  return pluralRules[locale].select(count) === "one" ? forms.one : forms.other;
}
