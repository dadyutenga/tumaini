import { useEffect, useState } from "react";
import { AlertTriangle, Calendar, CheckCircle2, Lock, LockOpen } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  DataGrid,
  Field,
  FormDialog,
  PageHeader,
  Pager,
  SectionCard,
  StatusBadge,
} from "@/components/shared/kit";
import { ConfirmDialog, PageLoader, StatCard } from "@/components/shared/Display";
import { useAuth } from "@/features/auth/auth";
import { useGet, useGetPage, useWrite } from "@/hooks/useApi";
import { formatDate, formatDateTime, formatMoney, today } from "@/lib/formatters";
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";

interface ClosingRecord {
  id: string;
  expected_cash: string;
  counted_cash: string;
  difference: string;
  status: string;
  note?: string | null;
  closed_at: string;
  closed_by_name?: string | null;
  reopened_at?: string | null;
  reopened_by_name?: string | null;
  reopen_reason?: string | null;
}

interface DaySummaryRes {
  date: string;
  sales_by_method: Record<string, string>;
  payments_received: string;
  expenses_total: string;
  expected_cash: string;
  closing: ClosingRecord | null;
}

interface ClosingRow {
  id: string;
  date: string;
  branch_name: string;
  expected_cash: string;
  counted_cash: string;
  difference: string;
  payments_received: string;
  expenses_total: string;
  status: string;
  note?: string | null;
  closed_at: string;
  closed_by_name?: string | null;
}

export default function DayClosing() {
  const { t } = useI18n();
  const { can } = useAuth();
  const [date, setDate] = useState(today());
  const [page, setPage] = useState(1);

  const summary = useGet<DaySummaryRes>("/day-closings/summary", { date });
  const history = useGetPage<ClosingRow>("/day-closings", { page });

  const [countedCash, setCountedCash] = useState("");
  const [note, setNote] = useState("");
  const [confirmingClose, setConfirmingClose] = useState(false);

  const [reopening, setReopening] = useState<{ id: string; date: string } | null>(null);
  const [reopenReason, setReopenReason] = useState("");

  const summaryDate = summary.data?.date;
  const summaryExpected = summary.data?.expected_cash;
  const closingCounted = summary.data?.closing?.counted_cash;
  const closingNote = summary.data?.closing?.note;
  const closingStatus = summary.data?.closing?.status;

  useEffect(() => {
    if (summaryDate) {
      setCountedCash(closingCounted ?? summaryExpected ?? "0");
      setNote(closingNote ?? "");
    }
  }, [summaryDate, summaryExpected, closingCounted, closingNote, closingStatus]);

  const closeDay = useWrite<{ date: string; counted_cash: string; note: string }, unknown>(
    "POST",
    "/day-closings",
    {
      success: t.finance.dayClosed,
      invalidate: ["/day-closings/summary", "/day-closings"],
      onSuccess: () => setConfirmingClose(false),
    },
  );

  const reopenDay = useWrite<{ reason: string }, unknown>(
    "POST",
    () => `/day-closings/${reopening?.id}/reopen`,
    {
      success: t.finance.dayReopened,
      invalidate: ["/day-closings/summary", "/day-closings"],
      onSuccess: () => {
        setReopening(null);
        setReopenReason("");
      },
    },
  );

  const data = summary.data;
  const closing = data?.closing;
  const isClosed = closing?.status === "closed";
  const expected = Number(data?.expected_cash ?? 0);
  const counted = Number(countedCash || 0);
  const variance = counted - expected;

  const methods = Object.entries(data?.sales_by_method ?? {});
  const totalSales = methods.reduce((s, [, v]) => s + Number(v || 0), 0);

  return (
    <div className="space-y-6">
      <PageHeader
        title={t.finance.endOfDayClosing}
        description={t.finance.reviewDailySalesPaymentsAnd}
        actions={
          <div className="flex items-center gap-2">
            <label
              htmlFor="closing-date"
              className="flex items-center gap-1.5 text-xs font-medium text-[var(--od-ink-secondary)]"
            >
              <Calendar size={14} /> {t.finance.businessDate}
            </label>
            <Input
              id="closing-date"
              type="date"
              className="w-40"
              value={date}
              onChange={(e) => setDate(e.target.value)}
            />
          </div>
        }
      />

      {summary.isLoading ? (
        <PageLoader />
      ) : data ? (
        <div className="space-y-6">
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <div className="od-card p-5">
              <p className="od-kpi-label">
                {fmt(t.finance.sales, { date: formatDate(data.date) })}
              </p>
              <p className="od-kpi-value mt-1.5">{formatMoney(totalSales)}</p>
              <div className="mt-3 space-y-1 border-t border-[var(--od-border)] pt-2.5 text-xs">
                {methods.length === 0 ? (
                  <p className="text-[var(--od-ink-muted)]">{t.finance.noSalesRecorded}</p>
                ) : (
                  methods.map(([m, v]) => (
                    <div key={m} className="flex items-center justify-between gap-2">
                      <span className="capitalize text-[var(--od-ink-secondary)]">
                        {m.replaceAll("_", " ")}
                      </span>
                      <span className="od-tabular font-medium text-[var(--od-ink)]">
                        {formatMoney(v)}
                      </span>
                    </div>
                  ))
                )}
              </div>
            </div>

            <StatCard
              label={t.finance.totalPaymentsReceived}
              value={formatMoney(data.payments_received)}
              trend={t.finance.salesDebtPaymentsServiceReceipts}
              tone="good"
            />

            <StatCard
              label={t.finance.expensesRecorded}
              value={formatMoney(data.expenses_total)}
              trend={t.finance.approvedPaidExpenses}
              tone={Number(data.expenses_total) > 0 ? "bad" : "neutral"}
            />

            <StatCard
              label={t.finance.expectedCashInDrawer}
              value={formatMoney(data.expected_cash)}
              trend={t.finance.openingFloatCashInCash}
            />
          </div>

          <SectionCard
            title={
              isClosed
                ? fmt(t.finance.dayClosed2, { date: formatDate(data.date) })
                : fmt(t.finance.cashCountClosing, { date: formatDate(data.date) })
            }
            subtitle={
              isClosed ? t.finance.dailyCashDrawerHasBeen : t.finance.enterPhysicalCashCountedIn
            }
            actions={
              <div className="flex items-center gap-2">
                {isClosed ? (
                  <Lock size={15} className="text-[var(--od-good)]" />
                ) : (
                  <LockOpen size={15} className="text-[var(--od-warning)]" />
                )}
                {closing && <StatusBadge value={closing.status} />}
              </div>
            }
          >
            {isClosed && closing ? (
              <div className="space-y-5">
                <div className="grid gap-3 sm:grid-cols-3">
                  <div className="rounded-xl border border-[var(--od-border)] bg-[var(--od-surface-subtle)] p-4">
                    <p className="od-kpi-label">{t.finance.expectedCash}</p>
                    <p className="od-tabular mt-1 text-lg font-semibold text-[var(--od-ink)]">
                      {formatMoney(closing.expected_cash)}
                    </p>
                  </div>
                  <div className="rounded-xl border border-[var(--od-border)] bg-[var(--od-surface-subtle)] p-4">
                    <p className="od-kpi-label">{t.finance.countedCash}</p>
                    <p className="od-tabular mt-1 text-lg font-semibold text-[var(--od-ink)]">
                      {formatMoney(closing.counted_cash)}
                    </p>
                  </div>
                  <div className="rounded-xl border border-[var(--od-border)] bg-[var(--od-surface-subtle)] p-4">
                    <p className="od-kpi-label">{t.finance.differenceVariance}</p>
                    <p
                      className={`od-tabular mt-1 text-lg font-semibold ${
                        Number(closing.difference) === 0
                          ? "text-[var(--od-good)]"
                          : Number(closing.difference) < 0
                            ? "text-[var(--od-bad)]"
                            : "text-[var(--od-warning)]"
                      }`}
                    >
                      {Number(closing.difference) > 0 ? "+" : ""}
                      {formatMoney(closing.difference)}
                    </p>
                  </div>
                </div>
                <div className="flex flex-wrap items-center justify-between gap-3 border-t border-[var(--od-border)] pt-4 text-sm text-[var(--od-ink-secondary)]">
                  <div>
                    {t.finance.closedBy}{" "}
                    <strong className="font-medium text-[var(--od-ink)]">
                      {closing.closed_by_name ?? "—"}
                    </strong>{" "}
                    {fmt(t.finance.on, { closed_at: formatDateTime(closing.closed_at) })}
                    {closing.note && (
                      <p className="mt-1 text-[var(--od-ink)]">
                        {fmt(t.finance.note, { note: closing.note })}
                      </p>
                    )}
                  </div>
                  {can("finance.day.reopen") && (
                    <Button
                      variant="outline"
                      onClick={() => setReopening({ id: closing.id, date: data.date })}
                    >
                      <LockOpen size={15} /> {t.finance.reopenDay}
                    </Button>
                  )}
                </div>
              </div>
            ) : (
              <div className="space-y-5">
                {closing?.status === "reopened" && (
                  <div className="flex items-start gap-2.5 rounded-xl border border-[var(--od-warning-mark)]/30 bg-[var(--od-warning-soft)] px-4 py-3 text-sm text-[var(--od-warning)]">
                    <AlertTriangle size={16} className="mt-0.5 shrink-0" />
                    <div>
                      {t.finance.previouslyReopenedBy}{" "}
                      <strong>{closing.reopened_by_name ?? t.finance.admin}</strong>:{" "}
                      {closing.reopen_reason}
                    </div>
                  </div>
                )}

                <div className="grid gap-4 md:grid-cols-3">
                  <Field label={t.finance.countedCashInDrawer}>
                    <Input
                      inputMode="decimal"
                      className="od-tabular"
                      value={countedCash}
                      onChange={(e) => setCountedCash(e.target.value)}
                      placeholder={t.finance.enterPhysicalCashCounted}
                    />
                  </Field>
                  <div className="flex flex-col justify-center rounded-xl border border-[var(--od-border)] bg-[var(--od-surface-subtle)] px-4 py-2.5">
                    <span className="od-kpi-label">{t.finance.liveVariance}</span>
                    <div className="mt-1 flex flex-wrap items-center gap-2">
                      <span
                        className={`od-tabular text-lg font-semibold ${
                          variance === 0
                            ? "text-[var(--od-good)]"
                            : variance < 0
                              ? "text-[var(--od-bad)]"
                              : "text-[var(--od-warning)]"
                        }`}
                      >
                        {variance > 0 ? "+" : ""}
                        {formatMoney(variance)}
                      </span>
                      <span
                        className={`od-delta ${
                          variance === 0
                            ? "od-delta-good"
                            : variance < 0
                              ? "od-delta-bad"
                              : "od-delta-neutral"
                        }`}
                      >
                        {variance === 0
                          ? t.finance.exactMatch
                          : variance < 0
                            ? t.finance.short
                            : t.finance.over}
                      </span>
                    </div>
                  </div>
                  <Field
                    label={
                      variance !== 0
                        ? t.finance.closingNoteRequiredForVariance
                        : t.finance.closingNoteOptional
                    }
                  >
                    <Input
                      required={variance !== 0}
                      value={note}
                      onChange={(e) => setNote(e.target.value)}
                      placeholder={t.finance.explainAnyVarianceOrNotes}
                    />
                  </Field>
                </div>

                {can("finance.day.close") ? (
                  <div className="flex justify-end border-t border-[var(--od-border)] pt-4">
                    <Button
                      disabled={variance !== 0 && !note.trim()}
                      onClick={() => setConfirmingClose(true)}
                    >
                      <CheckCircle2 size={16} /> {t.finance.closeDay}
                    </Button>
                  </div>
                ) : (
                  <p className="od-caption">{t.finance.youHaveViewOnlyAccess}</p>
                )}
              </div>
            )}
          </SectionCard>
        </div>
      ) : null}

      <div className="space-y-3">
        <div>
          <h2 className="od-section-title">{t.finance.pastDayClosings}</h2>
          <p className="od-caption mt-0.5">{t.finance.selectAnyPastDateTo}</p>
        </div>
        <DataGrid
          rows={history.data?.data}
          loading={history.isLoading}
          rowKey={(r) => r.id}
          empty={t.finance.noClosedDaysRecordedYet}
          onRowClick={(r) => setDate(r.date)}
          columns={[
            {
              header: t.common.date,
              cell: (r) => (
                <span className="font-medium text-[var(--od-ink)]">{formatDate(r.date)}</span>
              ),
            },
            { header: t.common.location, cell: (r) => r.branch_name },
            {
              header: t.finance.paymentsIn,
              align: "right",
              cell: (r) =>
                r.payments_received != null ? (
                  <span className="od-tabular">{formatMoney(r.payments_received)}</span>
                ) : (
                  "-"
                ),
            },
            {
              header: t.common.expenses,
              align: "right",
              cell: (r) =>
                r.expenses_total != null ? (
                  <span className="od-tabular">{formatMoney(r.expenses_total)}</span>
                ) : (
                  "-"
                ),
            },
            {
              header: t.finance.expectedCash,
              align: "right",
              cell: (r) => <span className="od-tabular">{formatMoney(r.expected_cash)}</span>,
            },
            {
              header: t.finance.countedCash,
              align: "right",
              cell: (r) => <span className="od-tabular">{formatMoney(r.counted_cash)}</span>,
            },
            {
              header: t.finance.variance,
              align: "right",
              cell: (r) => (
                <span
                  className={`od-tabular font-medium ${
                    Number(r.difference) === 0
                      ? "text-[var(--od-good)]"
                      : Number(r.difference) < 0
                        ? "text-[var(--od-bad)]"
                        : "text-[var(--od-warning)]"
                  }`}
                >
                  {Number(r.difference) > 0 ? "+" : ""}
                  {formatMoney(r.difference)}
                </span>
              ),
            },
            { header: t.finance.closedBy, cell: (r) => r.closed_by_name ?? "-" },
            { header: t.common.status, cell: (r) => <StatusBadge value={r.status} /> },
          ]}
        />
        <Pager meta={history.data?.meta} onPage={setPage} />
      </div>

      <ConfirmDialog
        open={confirmingClose}
        onOpenChange={setConfirmingClose}
        title={fmt(t.finance.close, { date: formatDate(date) })}
        description={fmt(t.finance.expectedCashCountedCashVariance, {
          expected: formatMoney(expected),
          counted: formatMoney(counted),
          value: variance > 0 ? "+" : "",
          variance: formatMoney(variance),
        })}
        onConfirm={() =>
          closeDay.mutate({
            date,
            counted_cash: countedCash || "0",
            note,
          })
        }
      />

      <FormDialog
        open={reopening !== null}
        onOpenChange={(o) => !o && setReopening(null)}
        title={fmt(t.finance.reopen, { value: reopening ? formatDate(reopening.date) : "" })}
        submitLabel={t.finance.reopenDay}
        busy={reopenDay.isPending}
        onSubmit={() => reopenDay.mutate({ reason: reopenReason })}
      >
        <Field label={t.finance.reasonForReopeningRequired}>
          <Textarea
            required
            rows={3}
            value={reopenReason}
            onChange={(e) => setReopenReason(e.target.value)}
            placeholder={t.finance.explainWhyThisClosedDay}
          />
        </Field>
      </FormDialog>
    </div>
  );
}
