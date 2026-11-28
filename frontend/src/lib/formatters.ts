import { intlLocale } from "@/features/i18n/active";
import { DEFAULT_CURRENCY } from "@/lib/constants";

/** Dates read day, month, year (screen specification §12). */
const DAY_MONTH_YEAR = { day: "numeric", month: "short", year: "numeric" } as const;

export const formatNumber = (value: number, locale = intlLocale()) =>
  new Intl.NumberFormat(locale).format(value);
export const formatCurrency = (value: number, currency = activeCurrency, locale = intlLocale()) =>
  new Intl.NumberFormat(locale, { style: "currency", currency }).format(value);
export const formatDate = (value: Date | string, locale = intlLocale()) =>
  new Intl.DateTimeFormat(locale, DAY_MONTH_YEAR).format(new Date(value));
export const formatBytes = (value: number, locale = intlLocale()) => {
  if (!Number.isFinite(value) || value < 0) return "—";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index =
    value === 0
      ? 0
      : Math.max(0, Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1));
  return `${new Intl.NumberFormat(locale, { maximumFractionDigits: index ? 1 : 0 }).format(value / 1024 ** index)} ${units[index]}`;
};

let activeCurrency = DEFAULT_CURRENCY;

/** Normalises the shapes the API uses for decimals: "12.50", 12.5, or a nullable `{Decimal, Valid}`. */
export function toNumber(value: unknown): number {
  if (value === null || value === undefined || value === "") return 0;
  if (typeof value === "number") return value;
  if (typeof value === "string") return Number(value) || 0;
  if (typeof value === "object" && "Decimal" in value)
    return toNumber((value as { Decimal: unknown }).Decimal);
  return 0;
}

/** Display only. Never send these numbers back to the API. */
export function formatMoney(value: unknown, currency = activeCurrency) {
  const n = toNumber(value);
  return `${currency} ${new Intl.NumberFormat(intlLocale(), { minimumFractionDigits: 2, maximumFractionDigits: 2 }).format(n)}`;
}

export function formatQty(value: unknown) {
  return new Intl.NumberFormat(intlLocale(), { maximumFractionDigits: 3 }).format(toNumber(value));
}

export function formatDateTime(value: string | null | undefined) {
  if (!value) return "-";
  const d = new Date(value);
  return Number.isNaN(d.getTime())
    ? value
    : d.toLocaleString(intlLocale(), { ...DAY_MONTH_YEAR, hour: "2-digit", minute: "2-digit" });
}

export function today() {
  const d = new Date();
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

/** Set once the business profile loads, so every amount shows the business currency. */
export function setActiveCurrency(code: string | undefined) {
  if (code) activeCurrency = code;
}
export function getActiveCurrency() {
  return activeCurrency;
}
