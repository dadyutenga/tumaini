import type { ReactNode } from "react";
import { StatusBadge } from "@/components/shared/kit";
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";
import { formatDate, formatMoney, formatQty, toNumber } from "@/lib/formatters";
import { cn } from "@/lib/utils";
import { Highlight } from "./highlight";
import type { SearchHit } from "./types";
import { KIND_ICONS } from "./icons";

/** Icon, title with the typed text marked, a muted second line and facts on the right. */
export function Row({
  icon,
  title,
  subtitle,
  query = "",
  children,
}: {
  icon: ReactNode;
  title: string;
  subtitle?: string;
  query?: string;
  children?: ReactNode;
}) {
  return (
    <>
      <span className="grid h-7 w-7 shrink-0 place-items-center rounded-lg bg-[var(--od-surface-subtle)] text-[var(--od-ink-muted)]">
        {icon}
      </span>
      <span className="min-w-0 flex-1">
        <span className="block truncate text-[13px] font-medium text-[var(--od-ink)]">
          <Highlight text={title} query={query} />
        </span>
        {subtitle && (
          <span className="block truncate text-xs text-[var(--od-ink-muted)]">
            <Highlight text={subtitle} query={query} />
          </span>
        )}
      </span>
      {children && (
        <span className="flex shrink-0 items-center gap-2 text-xs text-[var(--od-ink-secondary)]">
          {children}
        </span>
      )}
    </>
  );
}

/**
 * The facts that matter for each kind. Only what the server sends is shown, and buying
 * prices are not part of the reply at all.
 */
function Facts({ hit }: { hit: SearchHit }) {
  const { t } = useI18n();
  const m = hit.meta;
  if (!m) return null;
  const money = (value: string) => <span className="od-tabular">{formatMoney(value)}</span>;
  const optional = (value: string | null | undefined) =>
    value ? <span className="hidden sm:inline">{value}</span> : null;

  if (hit.kind === "product") {
    const empty = m.stock != null && toNumber(m.stock) <= 0;
    return (
      <>
        {optional(m.sku || m.barcode)}
        {m.stock != null && (
          <span
            className={cn(
              "rounded-full px-2 py-0.5 font-medium",
              empty || m.low_stock
                ? "bg-[color-mix(in_srgb,var(--od-bad-mark)_12%,transparent)] text-[var(--od-bad)]"
                : "bg-[var(--od-neutral-soft)]",
            )}
          >
            {empty ? t.search.outOfStock : fmt(t.search.stock, { count: formatQty(m.stock) })}
          </span>
        )}
        {m.price != null && money(m.price)}
      </>
    );
  }
  if (hit.kind === "customer" || hit.kind === "client")
    return (
      <>
        {optional(m.phone)}
        {m.balance != null && toNumber(m.balance) > 0 && (
          <span className="od-tabular text-[var(--od-warning)]">
            {fmt(t.search.owes, { amount: formatMoney(m.balance) })}
          </span>
        )}
      </>
    );
  if (hit.kind === "sale")
    return (
      <>
        {optional(m.date ? formatDate(m.date) : null)}
        {m.total != null && money(m.total)}
        {m.status && <StatusBadge value={m.status} />}
      </>
    );
  const billed = hit.kind === "service_invoice" || hit.kind === "quotation";
  return (
    <>
      {optional(m.client)}
      {billed && m.balance != null && money(m.balance)}
      {m.status && <StatusBadge value={m.status} />}
      {!billed && m.due_date && (
        <span className={cn(m.late && "font-semibold text-[var(--od-bad)]")}>
          {fmt(t.search.due, { date: formatDate(m.due_date) })}
        </span>
      )}
    </>
  );
}

export function RecordRow({ hit, query }: { hit: SearchHit; query: string }) {
  const Icon = KIND_ICONS[hit.kind];
  return (
    <Row icon={<Icon size={15} />} title={hit.title} subtitle={hit.subtitle} query={query}>
      {hit.meta && <Facts hit={hit} />}
    </Row>
  );
}
