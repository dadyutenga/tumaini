import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  DataGrid,
  Field,
  FilterBar,
  FormDialog,
  Pager,
  SegmentedChips,
  StatusBadge,
} from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { useGetPage, useWrite } from "@/hooks/useApi";
import { formatDate, formatMoney } from "@/lib/formatters";
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";

interface Payment {
  id: string;
  number: string;
  payment_date: string;
  direction: "in" | "out";
  kind: string;
  party_name?: string | null;
  account_name: string;
  amount: string;
  status: string;
}

export function PaymentsTab() {
  const { t } = useI18n();
  const { can, canAny } = useAuth();
  const [direction, setDirection] = useState("");
  const [page, setPage] = useState(1);
  const list = useGetPage<Payment>("/payments", { direction: direction || undefined, page });
  const [voiding, setVoiding] = useState<Payment | null>(null);
  const [reason, setReason] = useState("");
  const voidPayment = useWrite<{ reason: string }, unknown>(
    "POST",
    () => `/payments/${voiding?.id}/void`,
    {
      idempotent: true,
      invalidate: ["/payments", "/money", "/transactions", "/customers", "/sales"],
      success: t.finance.paymentVoidSubmitted,
      onSuccess: () => {
        setVoiding(null);
        setReason("");
      },
    },
  );
  const canVoid = canAny("finance.payment.void", "finance.payment.view_all");
  return (
    <>
      <FilterBar>
        <SegmentedChips
          ariaLabel={t.finance.direction}
          value={direction}
          onChange={(v) => {
            setDirection(v);
            setPage(1);
          }}
          options={[
            { value: "", label: t.finance.allPayments },
            { value: "in", label: t.finance.moneyIn2 },
            { value: "out", label: t.finance.moneyOut2 },
          ]}
        />
      </FilterBar>
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(p) => p.id}
        empty={t.common.noPaymentsYet}
        columns={[
          {
            header: t.common.number,
            cell: (p) => <span className="od-mono font-medium">{p.number}</span>,
          },
          {
            header: t.common.date,
            cell: (p) => (
              <span className="text-[var(--od-ink-secondary)]">{formatDate(p.payment_date)}</span>
            ),
          },
          {
            header: t.common.kind,
            cell: (p) => <span className="capitalize">{p.kind.replaceAll("_", " ")}</span>,
          },
          { header: t.finance.party, cell: (p) => p.party_name ?? "—" },
          { header: t.common.account, cell: (p) => p.account_name },
          {
            header: t.common.amount,
            align: "right",
            cell: (p) => (
              <span
                className={
                  p.direction === "out"
                    ? "font-semibold text-[var(--od-bad)]"
                    : "font-semibold text-[var(--od-good)]"
                }
              >
                {p.direction === "out" ? "−" : "+"}
                {formatMoney(p.amount)}
              </span>
            ),
          },
          { header: t.common.status, cell: (p) => <StatusBadge value={p.status} /> },
          {
            header: "",
            className: "text-right",
            cell: (p) =>
              canVoid &&
              p.status !== "voided" && (
                <Button
                  size="sm"
                  variant="ghost"
                  className="text-[var(--od-bad)] hover:text-[var(--od-bad)]"
                  onClick={() => setVoiding(p)}
                >
                  {can("finance.payment.void") ? t.common.void : t.common.requestVoid}
                </Button>
              ),
          },
        ]}
      />
      <Pager meta={list.data?.meta} onPage={setPage} />
      <FormDialog
        open={voiding !== null}
        onOpenChange={(o) => !o && setVoiding(null)}
        title={fmt(t.finance.void, { number: voiding?.number ?? t.finance.payment })}
        submitLabel={can("finance.payment.void") ? t.common.voidPayment : t.common.requestVoid}
        busy={voidPayment.isPending}
        onSubmit={() => voidPayment.mutate({ reason })}
      >
        <Field label={t.common.reason}>
          <Input required value={reason} onChange={(e) => setReason(e.target.value)} />
        </Field>
      </FormDialog>
    </>
  );
}
