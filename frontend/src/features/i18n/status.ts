import type { Translations } from "./locales/en";

/** The label for a value the API returns (`in_progress`, `mobile_money`), never the raw value. */
export function statusLabel(t: Translations, value: string | null | undefined) {
  if (!value) return "";
  const labels = t.status as Record<string, string>;
  const key = value.toLowerCase();
  return Object.prototype.hasOwnProperty.call(labels, key)
    ? labels[key]
    : value.replaceAll("_", " ");
}
