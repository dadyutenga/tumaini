import type { ReactNode } from "react";
import { Check, Copy, Inbox, LoaderCircle, Printer } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { cn } from "@/lib/utils";
import { useI18n } from "@/features/i18n/i18n";

export function StatCard({
  label,
  value,
  trend,
  tone = "neutral",
  icon,
  className,
}: {
  label: string;
  value: ReactNode;
  trend?: string;
  tone?: "good" | "bad" | "neutral";
  icon?: ReactNode;
  className?: string;
}) {
  return (
    <section className={cn("od-card justify-between gap-3", className)}>
      <div className="flex items-center justify-between gap-2">
        <p className="od-kpi-label">{label}</p>
        {icon && <span className="text-[var(--od-ink-muted)]">{icon}</span>}
      </div>
      <div>
        <p className="od-kpi-value text-[var(--od-ink)]">{value}</p>
        {trend && <TrendIndicator value={trend} tone={tone} />}
      </div>
    </section>
  );
}

export function TrendIndicator({
  value,
  tone = "neutral",
}: {
  value: string;
  tone?: "good" | "bad" | "neutral";
}) {
  const toneClass =
    tone === "good" ? "od-delta-good" : tone === "bad" ? "od-delta-bad" : "od-delta-neutral";
  return <p className={cn("od-delta mt-1.5", toneClass)}>{value}</p>;
}

export function ChartCard({
  title,
  description,
  actions,
  children,
  className,
}: {
  title: string;
  description?: string;
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <section className={cn("od-card", className)}>
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <h2 className="od-card-title text-[var(--od-ink)]">{title}</h2>
          {description && <p className="od-card-subtitle">{description}</p>}
        </div>
        {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
      </div>
      <div className="mt-2">{children}</div>
    </section>
  );
}

export type Activity = { id: string; label: string; time: string };

export function ActivityFeed({ items }: { items: Activity[] }) {
  return (
    <ol className="divide-y divide-[var(--od-border)]">
      {items.map((item) => (
        <li key={item.id} className="flex items-baseline justify-between gap-4 py-3 text-[13px]">
          <span className="text-[var(--od-ink)]">{item.label}</span>
          <time className="od-tabular shrink-0 text-[12px] text-[var(--od-ink-muted)]">
            {item.time}
          </time>
        </li>
      ))}
    </ol>
  );
}

export function Spinner({ label }: { label?: string }) {
  const { t } = useI18n();
  return (
    <span
      role="status"
      aria-label={label ?? t.common.loading}
      className="inline-flex text-[var(--od-ink-muted)]"
    >
      <LoaderCircle className="h-5 w-5 animate-spin" aria-hidden="true" />
    </span>
  );
}

export function PageLoader() {
  const { t } = useI18n();
  return (
    <div className="grid min-h-48 place-items-center py-12">
      <div className="flex flex-col items-center gap-2.5">
        <Spinner />
        <p className="od-caption">{t.common.loading2}</p>
      </div>
    </div>
  );
}

export function EmptyState({
  title,
  description,
  action,
}: {
  title: string;
  description?: string;
  action?: ReactNode;
}) {
  return (
    <div className="flex flex-col items-center justify-center px-6 py-12 text-center">
      <div className="mb-3 grid h-10 w-10 place-items-center rounded-full bg-[var(--od-surface-subtle)] text-[var(--od-ink-muted)]">
        <Inbox size={18} />
      </div>
      <p className="text-[14px] font-semibold text-[var(--od-ink)]">{title}</p>
      {description && (
        <p className="mt-1 max-w-sm text-[12px] leading-[1.45] text-[var(--od-ink-muted)]">
          {description}
        </p>
      )}
      {action && <div className="mt-4">{action}</div>}
    </div>
  );
}

export function ConfirmDialog({
  open,
  onOpenChange,
  onConfirm,
  title,
  description,
  confirmLabel,
  destructive,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onConfirm: () => void;
  title: string;
  description: string;
  confirmLabel?: string;
  destructive?: boolean;
}) {
  const { t } = useI18n();
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent className="rounded-[var(--od-radius,16px)] border-[var(--od-border-strong)] bg-[var(--od-surface)] shadow-[var(--od-shadow-pop)]">
        <AlertDialogHeader>
          <AlertDialogTitle className="text-[16px] font-semibold">{title}</AlertDialogTitle>
          <AlertDialogDescription className="text-[13px] text-[var(--od-ink-secondary)]">
            {description}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t.common.cancel}</AlertDialogCancel>
          <AlertDialogAction
            onClick={onConfirm}
            className={
              destructive
                ? "bg-destructive text-destructive-foreground hover:opacity-90"
                : undefined
            }
          >
            {confirmLabel ?? t.common.confirm}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

export function CopyButton({ text, label: given }: { text: string; label?: string }) {
  const { t } = useI18n();
  const label = given ?? t.common.copy;
  const [copied, setCopied] = useState(false);
  return (
    <Button
      variant="outline"
      size="sm"
      onClick={() => {
        void navigator.clipboard.writeText(text).then(
          () => {
            setCopied(true);
            toast.success(t.common.copied);
            setTimeout(() => setCopied(false), 1800);
          },
          () => toast.error(t.common.couldNotCopy),
        );
      }}
    >
      {copied ? <Check size={14} /> : <Copy size={14} />}
      <span>{copied ? t.common.copied : label}</span>
    </Button>
  );
}

export function Stepper({ steps, current }: { steps: string[]; current: number }) {
  const { t } = useI18n();
  return (
    <ol className="flex flex-wrap items-center gap-2" aria-label={t.common.progress}>
      {steps.map((step, index) => {
        const done = index < current;
        const active = index === current;
        return (
          <li
            key={step}
            aria-current={active ? "step" : undefined}
            className={cn(
              "inline-flex items-center gap-2 rounded-full border px-3 py-1 text-[12px] font-medium",
              active &&
                "border-[var(--od-ink)] bg-[var(--od-app-primary)] text-[var(--od-app-primary-foreground)]",
              done &&
                "border-transparent bg-[color-mix(in_srgb,var(--od-good-mark)_12%,transparent)] text-[var(--od-good)]",
              !active &&
                !done &&
                "border-[var(--od-border-strong)] bg-[var(--od-surface)] text-[var(--od-ink-muted)]",
            )}
          >
            <span className="od-tabular text-[11px] font-semibold">{index + 1}</span>
            <span>{step}</span>
          </li>
        );
      })}
    </ol>
  );
}

export function PrintableDocument({ title, children }: { title: string; children: ReactNode }) {
  const { t } = useI18n();
  return (
    <article className="print-document od-card p-6 sm:p-8">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--od-border)] pb-4">
        <h1 className="text-[18px] font-semibold tracking-[-0.01em] text-[var(--od-ink)]">
          {title}
        </h1>
        <Button className="print:hidden" variant="outline" size="sm" onClick={() => window.print()}>
          <Printer size={14} /> {t.common.print}
        </Button>
      </div>
      <div className="mt-5">{children}</div>
    </article>
  );
}
