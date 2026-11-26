import {
  forwardRef,
  useEffect,
  useMemo,
  useState,
  type ComponentProps,
  type ReactNode,
} from "react";
import { Link } from "react-router-dom";
import {
  ArrowDown,
  ArrowLeft,
  ArrowUp,
  ArrowUpDown,
  ChevronLeft,
  ChevronRight,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { EmptyState, PageLoader } from "@/components/shared/Display";
import { cn } from "@/lib/utils";
import type { Meta } from "@/lib/api";

export function PageHeader({
  title,
  description,
  actions,
  backTo,
  backLabel,
  badge,
}: {
  title: string;
  description?: string;
  actions?: ReactNode;
  backTo?: string;
  backLabel?: string;
  badge?: ReactNode;
}) {
  return (
    <div className="mb-6 flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
      <div className="min-w-0">
        {backTo && (
          <Link
            to={backTo}
            className="mb-2 inline-flex items-center gap-1.5 text-[12px] font-medium text-[var(--od-ink-muted)] transition-colors hover:text-[var(--od-ink)]"
          >
            <ArrowLeft size={13} />
            <span>{backLabel ?? "Back"}</span>
          </Link>
        )}
        <div className="flex flex-wrap items-center gap-2.5">
          <h1 className="od-page-title text-[var(--od-ink)]">{title}</h1>
          {badge}
        </div>
        {description && (
          <p className="mt-1.5 text-[13px] leading-[1.45] text-[var(--od-ink-secondary)]">
            {description}
          </p>
        )}
      </div>
      {actions && <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div>}
    </div>
  );
}

/** OpenDashboards card container with optional header and action slot. */
export function SectionCard({
  title,
  subtitle,
  actions,
  children,
  className,
  flush,
}: {
  title?: string;
  subtitle?: string;
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
  flush?: boolean;
}) {
  return (
    <section className={cn(flush ? "od-card-flush overflow-hidden" : "od-card", className)}>
      {(title || actions) && (
        <div
          className={cn(
            "flex flex-wrap items-start justify-between gap-3",
            flush && "border-b border-[var(--od-border)] px-6 py-4",
          )}
        >
          <div className="min-w-0">
            {title && <h2 className="od-card-title text-[var(--od-ink)]">{title}</h2>}
            {subtitle && <p className="od-card-subtitle">{subtitle}</p>}
          </div>
          {actions && <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div>}
        </div>
      )}
      {children}
    </section>
  );
}

/** OpenDashboards segmented filter chips (.od-chips / .od-chip). */
export function SegmentedChips<T extends string>({
  options,
  value,
  onChange,
  ariaLabel = "Filter",
  className,
}: {
  options: { value: T; label: string; count?: number }[];
  value: T;
  onChange: (value: T) => void;
  ariaLabel?: string;
  className?: string;
}) {
  return (
    <div role="radiogroup" aria-label={ariaLabel} className={cn("od-chips flex-wrap", className)}>
      {options.map((opt) => {
        const selected = opt.value === value;
        return (
          <button
            key={opt.value}
            type="button"
            role="radio"
            aria-checked={selected}
            onClick={() => onChange(opt.value)}
            className="od-chip inline-flex items-center gap-1.5 font-medium"
          >
            <span>{opt.label}</span>
            {opt.count !== undefined && (
              <span className="od-tabular text-[11px] opacity-65">{opt.count}</span>
            )}
          </button>
        );
      })}
    </div>
  );
}

/** Consistent horizontal toolbar for search, filters, and secondary controls above a table or list. */
export function FilterBar({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div className={cn("mb-4 flex flex-wrap items-center gap-2.5", className)}>{children}</div>
  );
}

/** Structured metadata key-value strip for detail views. */
export function MetaGrid({
  items,
  className,
}: {
  items: { label: string; value: ReactNode; mono?: boolean }[];
  className?: string;
}) {
  return (
    <dl className={cn("grid gap-4 sm:grid-cols-2 lg:grid-cols-4", className)}>
      {items.map((item) => (
        <div
          key={item.label}
          className="rounded-[var(--od-radius-md,12px)] border border-[var(--od-border)] bg-[var(--od-surface-subtle)] px-4 py-3"
        >
          <dt className="od-kpi-label">{item.label}</dt>
          <dd
            className={cn(
              "mt-1 text-[14px] font-semibold text-[var(--od-ink)]",
              item.mono && "od-mono",
            )}
          >
            {item.value}
          </dd>
        </div>
      ))}
    </dl>
  );
}

export const NativeSelect = forwardRef<HTMLSelectElement, ComponentProps<"select">>(
  ({ className, ...props }, ref) => (
    <select
      ref={ref}
      className={cn(
        "flex h-9 w-full rounded-[var(--od-app-radius,10px)] border border-[var(--od-border-strong)] bg-[var(--od-surface)] px-3 py-1 text-sm text-[var(--od-ink)] shadow-[0_1px_2px_rgba(0,0,0,0.02)] transition-colors focus-visible:border-[var(--od-ink-secondary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40 disabled:opacity-50",
        className,
      )}
      {...props}
    />
  ),
);
NativeSelect.displayName = "NativeSelect";

export function Field({
  label,
  error,
  hint,
  children,
  className,
}: {
  label: string;
  error?: string;
  hint?: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("space-y-1.5", className)}>
      <Label className="text-[12px] font-medium text-[var(--od-ink-secondary)]">{label}</Label>
      {children}
      {hint && !error && <p className="text-[11px] text-[var(--od-ink-muted)]">{hint}</p>}
      {error && <p className="text-[12px] font-medium text-[var(--od-bad)]">{error}</p>}
    </div>
  );
}

type Tone = "good" | "warning" | "info" | "bad" | "neutral";

const STATUS_TONE_KEY: Record<string, Tone> = {
  completed: "good",
  paid: "good",
  closed: "good",
  approved: "good",
  active: "good",
  done: "good",
  delivered: "good",
  printing: "good",
  renewed: "good",
  received: "good",
  posted: "good",
  present: "good",
  converted: "good",
  success: "good",
  ok: "good",

  reopened: "warning",
  pending: "warning",
  submitted: "warning",
  partial: "warning",
  in_progress: "warning",
  in_design: "warning",
  internal_review: "warning",
  on_hold: "warning",
  urgent: "warning",
  owed: "warning",
  late: "warning",
  half_day: "warning",
  low: "warning",

  open: "info",
  with_client: "info",
  sent: "info",
  issued: "info",
  lead: "info",
  leave: "info",
  in_transit: "info",
  scheduled: "info",

  rejected: "bad",
  voided: "bad",
  cancelled: "bad",
  stuck: "bad",
  overdue: "bad",
  missing: "bad",
  changes_requested: "bad",
  void: "bad",
  absent: "bad",
  failed: "bad",
  expired: "bad",

  inactive: "neutral",
  draft: "neutral",
  new_brief: "neutral",
  not_started: "neutral",
  todo: "neutral",
};

const TONE_CLASS: Record<Tone, string> = {
  good: "bg-[color-mix(in_srgb,var(--od-good-mark)_12%,transparent)] text-[var(--od-good)]",
  warning: "bg-[var(--od-warning-soft)] text-[var(--od-warning)]",
  info: "bg-[color-mix(in_srgb,var(--od-info)_12%,transparent)] text-[var(--od-info)]",
  bad: "bg-[color-mix(in_srgb,var(--od-bad-mark)_12%,transparent)] text-[var(--od-bad)]",
  neutral: "bg-[var(--od-neutral-soft)] text-[var(--od-ink-secondary)]",
};

const DOT_CLASS: Record<Tone, string> = {
  good: "bg-[var(--od-good-mark)]",
  warning: "bg-[var(--od-warning-mark)]",
  info: "bg-[var(--od-info)]",
  bad: "bg-[var(--od-bad-mark)]",
  neutral: "bg-[var(--od-muted-mark)]",
};

export function StatusBadge({ value }: { value: string }) {
  const tone: Tone = STATUS_TONE_KEY[value.toLowerCase()] ?? "neutral";
  return (
    <Badge
      variant="outline"
      className={cn("border-transparent font-medium capitalize", TONE_CLASS[tone])}
    >
      <span className={cn("h-1.5 w-1.5 shrink-0 rounded-full", DOT_CLASS[tone])} aria-hidden />
      <span>{value.replaceAll("_", " ")}</span>
    </Badge>
  );
}

export interface Col<T> {
  header: string;
  cell: (row: T) => ReactNode;
  className?: string;
  align?: "right";
  /** Optional value extractor to enable click-to-sort on this column header. */
  sortValue?: (row: T) => string | number | null | undefined;
}

export function DataGrid<T>({
  rows,
  columns,
  loading,
  empty = "Nothing here yet",
  emptyDescription,
  onRowClick,
  rowKey,
  totals,
}: {
  rows: T[] | undefined;
  columns: Col<T>[];
  loading?: boolean;
  empty?: string;
  emptyDescription?: string;
  onRowClick?: (row: T) => void;
  rowKey: (row: T) => string;
  /** Optional footer cells aligned 1:1 with columns to show a bold totals row. */
  totals?: ReactNode[];
}) {
  const [sort, setSort] = useState<{ header: string; dir: "asc" | "desc" } | null>(null);

  const sortedRows = useMemo(() => {
    if (!rows || !sort) return rows;
    const col = columns.find((c) => c.header === sort.header);
    if (!col?.sortValue) return rows;
    const fn = col.sortValue;
    return [...rows].sort((a, b) => {
      const va = fn(a) ?? "";
      const vb = fn(b) ?? "";
      const cmp =
        typeof va === "number" && typeof vb === "number"
          ? va - vb
          : String(va).localeCompare(String(vb), undefined, { numeric: true });
      return sort.dir === "asc" ? cmp : -cmp;
    });
  }, [rows, columns, sort]);

  if (loading && !rows) return <PageLoader />;
  return (
    <div className="od-card-flush overflow-hidden">
      <Table>
        <TableHeader>
          <TableRow className="bg-[var(--od-surface-subtle)]/70 hover:bg-[var(--od-surface-subtle)]/70">
            {columns.map((c) => {
              const active = sort?.header === c.header;
              return (
                <TableHead
                  key={c.header}
                  className={cn(c.align === "right" && "text-right", c.className)}
                >
                  {c.sortValue ? (
                    <button
                      type="button"
                      onClick={() =>
                        setSort((prev) =>
                          prev?.header === c.header
                            ? { header: c.header, dir: prev.dir === "asc" ? "desc" : "asc" }
                            : { header: c.header, dir: "asc" },
                        )
                      }
                      className={cn(
                        "inline-flex cursor-pointer items-center gap-1 transition-colors hover:text-[var(--od-ink)]",
                        active && "text-[var(--od-ink)]",
                        c.align === "right" && "ml-auto",
                      )}
                    >
                      <span>{c.header}</span>
                      {active ? (
                        sort.dir === "asc" ? (
                          <ArrowUp size={12} />
                        ) : (
                          <ArrowDown size={12} />
                        )
                      ) : (
                        <ArrowUpDown size={12} className="opacity-40" />
                      )}
                    </button>
                  ) : (
                    c.header
                  )}
                </TableHead>
              );
            })}
          </TableRow>
        </TableHeader>
        <TableBody>
          {sortedRows?.map((row) => (
            <TableRow
              key={rowKey(row)}
              className={onRowClick ? "cursor-pointer" : undefined}
              onClick={onRowClick ? () => onRowClick(row) : undefined}
            >
              {columns.map((c) => (
                <TableCell
                  key={c.header}
                  className={cn(c.align === "right" && "od-tabular text-right", c.className)}
                >
                  {c.cell(row)}
                </TableCell>
              ))}
            </TableRow>
          ))}
        </TableBody>
        {totals && sortedRows && sortedRows.length > 0 && (
          <tfoot className="border-t border-[var(--od-border-strong)] bg-[var(--od-surface-subtle)] font-semibold">
            <TableRow className="hover:bg-transparent">
              {columns.map((c, idx) => (
                <TableCell
                  key={c.header || idx}
                  className={cn(c.align === "right" && "od-tabular text-right", c.className)}
                >
                  {totals[idx] ?? ""}
                </TableCell>
              ))}
            </TableRow>
          </tfoot>
        )}
      </Table>
      {rows?.length === 0 && <EmptyState title={empty} description={emptyDescription} />}
    </div>
  );
}

/** Warn the user before leaving/closing the tab when a form has unsaved edits. */
export function useUnsavedChanges(dirty: boolean) {
  useEffect(() => {
    if (!dirty) return;
    const handler = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      e.returnValue = "";
    };
    window.addEventListener("beforeunload", handler);
    return () => window.removeEventListener("beforeunload", handler);
  }, [dirty]);
}

export function Pager({ meta, onPage }: { meta?: Meta; onPage: (page: number) => void }) {
  if (!meta || meta.total <= meta.per_page) return null;
  const last = Math.ceil(meta.total / meta.per_page);
  return (
    <div className="mt-4 flex items-center justify-between gap-2 text-[12px] text-[var(--od-ink-muted)]">
      <span className="od-tabular">
        Page {meta.page} of {last} · {meta.total} total
      </span>
      <div className="flex items-center gap-1.5">
        <Button
          variant="outline"
          size="icon"
          aria-label="Previous page"
          disabled={meta.page <= 1}
          onClick={() => onPage(meta.page - 1)}
        >
          <ChevronLeft size={15} />
        </Button>
        <Button
          variant="outline"
          size="icon"
          aria-label="Next page"
          disabled={meta.page >= last}
          onClick={() => onPage(meta.page + 1)}
        >
          <ChevronRight size={15} />
        </Button>
      </div>
    </div>
  );
}

/** Modal with a title, body and a submit button. */
export function FormDialog({
  open,
  onOpenChange,
  title,
  description,
  submitLabel = "Save",
  busy,
  onSubmit,
  children,
  wide,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: string;
  submitLabel?: string;
  busy?: boolean;
  onSubmit: () => void;
  children: ReactNode;
  wide?: boolean;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className={cn("max-h-[90vh] overflow-y-auto", wide && "sm:max-w-2xl")}>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription className={description ? undefined : "sr-only"}>
            {description ?? title}
          </DialogDescription>
        </DialogHeader>
        <form
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            onSubmit();
          }}
        >
          {children}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy}>
              {busy ? "Saving…" : submitLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
