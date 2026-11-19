import { forwardRef, type ComponentProps, type ReactNode } from "react";
import { ChevronLeft, ChevronRight } from "lucide-react";
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
import { EmptyState, PageLoader } from "@/components/shared/Display";
import { cn } from "@/lib/utils";
import type { Meta } from "@/lib/api";

export function PageHeader({
  title,
  description,
  actions,
}: {
  title: string;
  description?: string;
  actions?: ReactNode;
}) {
  return (
    <div className="mb-6 flex flex-wrap items-start justify-between gap-3">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
        {description && <p className="mt-1 text-sm text-muted-foreground">{description}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  );
}

export const NativeSelect = forwardRef<HTMLSelectElement, ComponentProps<"select">>(
  ({ className, ...props }, ref) => (
    <select
      ref={ref}
      className={cn(
        "flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:opacity-50",
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
  children,
  className,
}: {
  label: string;
  error?: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("space-y-1.5", className)}>
      <Label>{label}</Label>
      {children}
      {error && <p className="text-xs text-destructive">{error}</p>}
    </div>
  );
}

const STATUS_TONE: Record<string, string> = {
  completed: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-400",
  paid: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-400",
  closed: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-400",
  reopened: "bg-amber-500/15 text-amber-700 dark:text-amber-400",
  approved: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-400",
  active: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-400",
  open: "bg-sky-500/15 text-sky-700 dark:text-sky-400",
  pending: "bg-amber-500/15 text-amber-700 dark:text-amber-400",
  submitted: "bg-amber-500/15 text-amber-700 dark:text-amber-400",
  partial: "bg-amber-500/15 text-amber-700 dark:text-amber-400",
  rejected: "bg-red-500/15 text-red-700 dark:text-red-400",
  voided: "bg-red-500/15 text-red-700 dark:text-red-400",
  cancelled: "bg-red-500/15 text-red-700 dark:text-red-400",
  inactive: "bg-muted text-muted-foreground",
  // Services: green for finished work, yellow for work underway, blue for waiting on someone,
  // red for late or blocked, grey for drafts.
  done: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-400",
  delivered: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-400",
  printing: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-400",
  renewed: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-400",
  received: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-400",
  posted: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-400",
  in_progress: "bg-amber-500/15 text-amber-700 dark:text-amber-400",
  in_design: "bg-amber-500/15 text-amber-700 dark:text-amber-400",
  internal_review: "bg-amber-500/15 text-amber-700 dark:text-amber-400",
  on_hold: "bg-amber-500/15 text-amber-700 dark:text-amber-400",
  urgent: "bg-amber-500/15 text-amber-700 dark:text-amber-400",
  with_client: "bg-sky-500/15 text-sky-700 dark:text-sky-400",
  sent: "bg-sky-500/15 text-sky-700 dark:text-sky-400",
  issued: "bg-sky-500/15 text-sky-700 dark:text-sky-400",
  lead: "bg-sky-500/15 text-sky-700 dark:text-sky-400",
  owed: "bg-amber-500/15 text-amber-700 dark:text-amber-400",
  stuck: "bg-red-500/15 text-red-700 dark:text-red-400",
  overdue: "bg-red-500/15 text-red-700 dark:text-red-400",
  missing: "bg-red-500/15 text-red-700 dark:text-red-400",
  changes_requested: "bg-red-500/15 text-red-700 dark:text-red-400",
  void: "bg-red-500/15 text-red-700 dark:text-red-400",
  draft: "bg-muted text-muted-foreground",
  new_brief: "bg-muted text-muted-foreground",
  not_started: "bg-muted text-muted-foreground",
  todo: "bg-muted text-muted-foreground",
};

export function StatusBadge({ value }: { value: string }) {
  return (
    <Badge
      variant="outline"
      className={cn("border-transparent capitalize", STATUS_TONE[value.toLowerCase()])}
    >
      {value.replaceAll("_", " ")}
    </Badge>
  );
}

export interface Col<T> {
  header: string;
  cell: (row: T) => ReactNode;
  className?: string;
  align?: "right";
}

export function DataGrid<T>({
  rows,
  columns,
  loading,
  empty = "Nothing here yet",
  onRowClick,
  rowKey,
}: {
  rows: T[] | undefined;
  columns: Col<T>[];
  loading?: boolean;
  empty?: string;
  onRowClick?: (row: T) => void;
  rowKey: (row: T) => string;
}) {
  if (loading && !rows) return <PageLoader />;
  return (
    <div className="overflow-x-auto rounded-2xl border bg-card">
      <Table>
        <TableHeader>
          <TableRow>
            {columns.map((c) => (
              <TableHead
                key={c.header}
                className={cn(c.align === "right" && "text-right", c.className)}
              >
                {c.header}
              </TableHead>
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows?.map((row) => (
            <TableRow
              key={rowKey(row)}
              className={onRowClick ? "cursor-pointer" : undefined}
              onClick={onRowClick ? () => onRowClick(row) : undefined}
            >
              {columns.map((c) => (
                <TableCell
                  key={c.header}
                  className={cn(c.align === "right" && "text-right tabular-nums", c.className)}
                >
                  {c.cell(row)}
                </TableCell>
              ))}
            </TableRow>
          ))}
        </TableBody>
      </Table>
      {rows?.length === 0 && <EmptyState title={empty} />}
    </div>
  );
}

export function Pager({ meta, onPage }: { meta?: Meta; onPage: (page: number) => void }) {
  if (!meta || meta.total <= meta.per_page) return null;
  const last = Math.ceil(meta.total / meta.per_page);
  return (
    <div className="mt-3 flex items-center justify-end gap-2 text-sm text-muted-foreground">
      <span>
        Page {meta.page} of {last} · {meta.total} total
      </span>
      <Button
        variant="outline"
        size="icon"
        aria-label="Previous page"
        disabled={meta.page <= 1}
        onClick={() => onPage(meta.page - 1)}
      >
        <ChevronLeft size={16} />
      </Button>
      <Button
        variant="outline"
        size="icon"
        aria-label="Next page"
        disabled={meta.page >= last}
        onClick={() => onPage(meta.page + 1)}
      >
        <ChevronRight size={16} />
      </Button>
    </div>
  );
}

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

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
