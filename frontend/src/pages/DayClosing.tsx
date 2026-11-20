import { useEffect, useState } from "react";
import { CheckCircle2, Lock, LockOpen } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  DataGrid,
  Field,
  FormDialog,
  PageHeader,
  Pager,
  StatusBadge,
} from "@/components/shared/kit";
import { ConfirmDialog } from "@/components/shared/Display";
import { useAuth } from "@/features/auth/auth";
import { useGet, useGetPage, useWrite } from "@/hooks/useApi";
import { formatDate, formatDateTime, formatMoney, today } from "@/lib/formatters";

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
      success: "Day closed",
      invalidate: ["/day-closings/summary", "/day-closings"],
      onSuccess: () => setConfirmingClose(false),
    },
  );

  const reopenDay = useWrite<{ reason: string }, unknown>(
    "POST",
    () => `/day-closings/${reopening?.id}/reopen`,
    {
      success: "Day reopened",
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
    <>
      <PageHeader
        title="End-of-Day Closing"
        description="Review daily sales, payments and expenses, count cash in drawer, and close the day."
        actions={
          <div className="flex items-center gap-2">
            <label htmlFor="closing-date" className="text-xs text-muted-foreground">
              Business date
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
        <p className="py-10 text-center text-sm text-muted-foreground">Loading day summary…</p>
      ) : data ? (
        <div className="mb-8 space-y-6">
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <div className="rounded-xl border bg-card p-4 shadow-sm">
              <p className="text-xs uppercase tracking-wider text-muted-foreground">
                Sales ({formatDate(data.date)})
              </p>
              <p className="mt-1 text-2xl font-bold tabular-nums">{formatMoney(totalSales)}</p>
              <div className="mt-2 space-y-0.5 text-xs text-muted-foreground">
                {methods.length === 0 ? (
                  <p>No sales recorded</p>
                ) : (
                  methods.map(([m, v]) => (
                    <div key={m} className="flex justify-between">
                      <span className="capitalize">{m.replaceAll("_", " ")}</span>
                      <span className="tabular-nums font-medium text-foreground">
                        {formatMoney(v)}
                      </span>
                    </div>
                  ))
                )}
              </div>
            </div>

            <div className="rounded-xl border bg-card p-4 shadow-sm">
              <p className="text-xs uppercase tracking-wider text-muted-foreground">
                Total payments received
              </p>
              <p className="mt-1 text-2xl font-bold tabular-nums text-emerald-700">
                {formatMoney(data.payments_received)}
              </p>
              <p className="mt-1 text-xs text-muted-foreground">
                Sales + debt payments + service receipts
              </p>
            </div>

            <div className="rounded-xl border bg-card p-4 shadow-sm">
              <p className="text-xs uppercase tracking-wider text-muted-foreground">
                Expenses recorded
              </p>
              <p className="mt-1 text-2xl font-bold tabular-nums text-rose-700">
                {formatMoney(data.expenses_total)}
              </p>
              <p className="mt-1 text-xs text-muted-foreground">Approved &amp; paid expenses</p>
            </div>

            <div className="rounded-xl border bg-card p-4 shadow-sm">
              <p className="text-xs uppercase tracking-wider text-muted-foreground">
                Expected cash in drawer
              </p>
              <p className="mt-1 text-2xl font-bold tabular-nums">
                {formatMoney(data.expected_cash)}
              </p>
              <p className="mt-1 text-xs text-muted-foreground">
                Opening float + cash in − cash out
              </p>
            </div>
          </div>

          <div className="rounded-2xl border bg-card p-5 shadow-sm">
            <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
              <div className="flex items-center gap-2">
                {isClosed ? (
                  <Lock size={18} className="text-emerald-600" />
                ) : (
                  <LockOpen size={18} className="text-amber-600" />
                )}
                <h2 className="text-lg font-semibold">
                  {isClosed
                    ? `Day closed — ${formatDate(data.date)}`
                    : `Cash count & closing — ${formatDate(data.date)}`}
                </h2>
              </div>
              {closing && <StatusBadge value={closing.status} />}
            </div>

            {isClosed && closing ? (
              <div className="space-y-4">
                <div className="grid gap-4 sm:grid-cols-3">
                  <div className="rounded-lg bg-muted/50 p-3">
                    <p className="text-xs text-muted-foreground">Expected cash</p>
                    <p className="mt-0.5 text-lg font-semibold tabular-nums">
                      {formatMoney(closing.expected_cash)}
                    </p>
                  </div>
                  <div className="rounded-lg bg-muted/50 p-3">
                    <p className="text-xs text-muted-foreground">Counted cash</p>
                    <p className="mt-0.5 text-lg font-semibold tabular-nums">
                      {formatMoney(closing.counted_cash)}
                    </p>
                  </div>
                  <div className="rounded-lg bg-muted/50 p-3">
                    <p className="text-xs text-muted-foreground">Difference (variance)</p>
                    <p
                      className={`mt-0.5 text-lg font-semibold tabular-nums ${
                        Number(closing.difference) === 0
                          ? "text-emerald-700"
                          : Number(closing.difference) < 0
                            ? "text-rose-700"
                            : "text-amber-700"
                      }`}
                    >
                      {Number(closing.difference) > 0 ? "+" : ""}
                      {formatMoney(closing.difference)}
                    </p>
                  </div>
                </div>
                <div className="flex flex-wrap items-center justify-between gap-3 text-sm text-muted-foreground">
                  <div>
                    Closed by{" "}
                    <strong className="text-foreground">{closing.closed_by_name ?? "—"}</strong> on{" "}
                    {formatDateTime(closing.closed_at)}
                    {closing.note && <p className="mt-1 text-foreground">Note: {closing.note}</p>}
                  </div>
                  {can("finance.day.reopen") && (
                    <Button
                      variant="outline"
                      onClick={() => setReopening({ id: closing.id, date: data.date })}
                    >
                      <LockOpen size={15} /> Reopen day
                    </Button>
                  )}
                </div>
              </div>
            ) : (
              <div className="space-y-4">
                {closing?.status === "reopened" && (
                  <div className="rounded-lg border border-amber-300 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-800 dark:bg-amber-950/30 dark:text-amber-200">
                    Previously reopened by <strong>{closing.reopened_by_name ?? "Admin"}</strong>:{" "}
                    {closing.reopen_reason}
                  </div>
                )}

                <div className="grid gap-4 md:grid-cols-3">
                  <Field label="Counted cash in drawer">
                    <Input
                      inputMode="decimal"
                      value={countedCash}
                      onChange={(e) => setCountedCash(e.target.value)}
                      placeholder="Enter physical cash counted"
                    />
                  </Field>
                  <div className="flex flex-col justify-center rounded-lg border bg-muted/40 px-4 py-2">
                    <span className="text-xs text-muted-foreground">Live variance</span>
                    <div className="mt-1 flex items-center gap-2">
                      <span
                        className={`text-lg font-bold tabular-nums ${
                          variance === 0
                            ? "text-emerald-700"
                            : variance < 0
                              ? "text-rose-700"
                              : "text-amber-700"
                        }`}
                      >
                        {variance > 0 ? "+" : ""}
                        {formatMoney(variance)}
                      </span>
                      <Badge variant="outline">
                        {variance === 0 ? "Exact match" : variance < 0 ? "Short" : "Over"}
                      </Badge>
                    </div>
                  </div>
                  <Field
                    label={
                      variance !== 0
                        ? "Closing note (required for variance)"
                        : "Closing note (optional)"
                    }
                  >
                    <Input
                      required={variance !== 0}
                      value={note}
                      onChange={(e) => setNote(e.target.value)}
                      placeholder="Explain any variance or notes…"
                    />
                  </Field>
                </div>

                {can("finance.day.close") ? (
                  <div className="flex justify-end">
                    <Button
                      disabled={variance !== 0 && !note.trim()}
                      onClick={() => setConfirmingClose(true)}
                    >
                      <CheckCircle2 size={16} /> Close Day
                    </Button>
                  </div>
                ) : (
                  <p className="text-xs text-muted-foreground">
                    You have view-only access to day closing.
                  </p>
                )}
              </div>
            )}
          </div>
        </div>
      ) : null}

      <div className="space-y-3">
        <h2 className="text-lg font-semibold">Past Day Closings</h2>
        <DataGrid
          rows={history.data?.data}
          loading={history.isLoading}
          rowKey={(r) => r.id}
          empty="No closed days recorded yet"
          onRowClick={(r) => setDate(r.date)}
          columns={[
            {
              header: "Date",
              cell: (r) => <span className="font-medium">{formatDate(r.date)}</span>,
            },
            { header: "Location", cell: (r) => r.branch_name },
            {
              header: "Payments in",
              align: "right",
              cell: (r) => (r.payments_received != null ? formatMoney(r.payments_received) : "-"),
            },
            {
              header: "Expenses",
              align: "right",
              cell: (r) => (r.expenses_total != null ? formatMoney(r.expenses_total) : "-"),
            },
            { header: "Expected cash", align: "right", cell: (r) => formatMoney(r.expected_cash) },
            { header: "Counted cash", align: "right", cell: (r) => formatMoney(r.counted_cash) },
            {
              header: "Variance",
              align: "right",
              cell: (r) => (
                <span
                  className={
                    Number(r.difference) === 0
                      ? "text-emerald-700"
                      : Number(r.difference) < 0
                        ? "font-medium text-rose-700"
                        : "font-medium text-amber-700"
                  }
                >
                  {Number(r.difference) > 0 ? "+" : ""}
                  {formatMoney(r.difference)}
                </span>
              ),
            },
            { header: "Closed by", cell: (r) => r.closed_by_name ?? "-" },
            { header: "Status", cell: (r) => <StatusBadge value={r.status} /> },
          ]}
        />
        <Pager meta={history.data?.meta} onPage={setPage} />
      </div>

      <ConfirmDialog
        open={confirmingClose}
        onOpenChange={setConfirmingClose}
        title={`Close ${formatDate(date)}?`}
        description={`Expected cash: ${formatMoney(expected)} · Counted cash: ${formatMoney(counted)} · Variance: ${variance > 0 ? "+" : ""}${formatMoney(variance)}. Only the Owner can reopen a closed day.`}
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
        title={`Reopen ${reopening ? formatDate(reopening.date) : ""}`}
        submitLabel="Reopen day"
        busy={reopenDay.isPending}
        onSubmit={() => reopenDay.mutate({ reason: reopenReason })}
      >
        <Field label="Reason for reopening (required)">
          <Textarea
            required
            rows={3}
            value={reopenReason}
            onChange={(e) => setReopenReason(e.target.value)}
            placeholder="Explain why this closed day needs to be reopened…"
          />
        </Field>
      </FormDialog>
    </>
  );
}
