import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  DataGrid,
  Field,
  FormDialog,
  NativeSelect,
  Pager,
  StatusBadge,
} from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { useGetPage, useWrite } from "@/hooks/useApi";
import { formatDate, formatMoney } from "@/lib/formatters";

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
      success: "Payment void submitted",
      onSuccess: () => {
        setVoiding(null);
        setReason("");
      },
    },
  );
  const canVoid = canAny("finance.payment.void", "finance.payment.view_all");
  return (
    <>
      <NativeSelect
        aria-label="Direction"
        className="mb-4 max-w-[12rem]"
        value={direction}
        onChange={(e) => {
          setDirection(e.target.value);
          setPage(1);
        }}
      >
        <option value="">Money in and out</option>
        <option value="in">Money in</option>
        <option value="out">Money out</option>
      </NativeSelect>
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(p) => p.id}
        empty="No payments yet"
        columns={[
          { header: "Number", cell: (p) => <span className="font-medium">{p.number}</span> },
          { header: "Date", cell: (p) => formatDate(p.payment_date) },
          { header: "Kind", cell: (p) => p.kind.replaceAll("_", " ") },
          { header: "Party", cell: (p) => p.party_name ?? "-" },
          { header: "Account", cell: (p) => p.account_name },
          {
            header: "Amount",
            align: "right",
            cell: (p) => (
              <span className={p.direction === "out" ? "text-destructive" : ""}>
                {p.direction === "out" ? "-" : ""}
                {formatMoney(p.amount)}
              </span>
            ),
          },
          { header: "Status", cell: (p) => <StatusBadge value={p.status} /> },
          {
            header: "",
            cell: (p) =>
              canVoid &&
              p.status !== "voided" && (
                <Button size="sm" variant="ghost" onClick={() => setVoiding(p)}>
                  {can("finance.payment.void") ? "Void" : "Request void"}
                </Button>
              ),
          },
        ]}
      />
      <Pager meta={list.data?.meta} onPage={setPage} />
      <FormDialog
        open={voiding !== null}
        onOpenChange={(o) => !o && setVoiding(null)}
        title={`Void ${voiding?.number ?? "payment"}`}
        submitLabel={can("finance.payment.void") ? "Void payment" : "Request void"}
        busy={voidPayment.isPending}
        onSubmit={() => voidPayment.mutate({ reason })}
      >
        <Field label="Reason">
          <Input required value={reason} onChange={(e) => setReason(e.target.value)} />
        </Field>
      </FormDialog>
    </>
  );
}
