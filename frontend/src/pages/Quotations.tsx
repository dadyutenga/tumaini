import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Download, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  DataGrid,
  Field,
  FormDialog,
  NativeSelect,
  PageHeader,
  Pager,
  StatusBadge,
} from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { errorMessage, useGet, useGetPage, useWrite } from "@/hooks/useApi";
import { download } from "@/lib/api";
import { formatDate, formatMoney } from "@/lib/formatters";
import type { Customer, MoneyAccount, Product } from "@/types";

interface Quotation {
  id: string;
  number: string;
  customer_name?: string | null;
  status: string;
  total: string;
  valid_until?: string | null;
  created_at: string;
}

export default function Quotations() {
  const { can } = useAuth();
  const navigate = useNavigate();
  const [status, setStatus] = useState("");
  const [page, setPage] = useState(1);
  const list = useGetPage<Quotation>("/quotations", { status: status || undefined, page });
  const products = useGet<Product[]>("/products", { per_page: 100 });
  const customers = useGet<Customer[]>("/customers", undefined, { enabled: can("customers.view") });
  const accounts = useGet<MoneyAccount[]>("/money-accounts", undefined, {
    enabled: can("accounts.view"),
  });
  const [open, setOpen] = useState(false);
  const [customerId, setCustomerId] = useState("");
  const [validUntil, setValidUntil] = useState("");
  const [lines, setLines] = useState([{ product_id: "", qty: "1" }]);
  const [converting, setConverting] = useState<Quotation | null>(null);
  const [account, setAccount] = useState("");

  const create = useWrite<unknown, unknown>("POST", "/quotations", {
    invalidate: ["/quotations"],
    success: "Quotation saved",
    onSuccess: () => {
      setOpen(false);
      setLines([{ product_id: "", qty: "1" }]);
    },
  });
  const convert = useWrite<unknown, { id: string }>(
    "POST",
    () => `/quotations/${converting?.id}/convert`,
    {
      idempotent: true,
      invalidate: ["/quotations", "/sales", "/stock", "/money", "/customers"],
      success: "Sale created from the quotation",
      onSuccess: (sale) => {
        setConverting(null);
        if (sale?.id) navigate(`/sales/${sale.id}`);
      },
    },
  );
  const setLine = (i: number, patch: Partial<(typeof lines)[number]>) =>
    setLines((all) => all.map((l, n) => (n === i ? { ...l, ...patch } : l)));

  return (
    <>
      <PageHeader
        title="Quotations"
        description="Price a job for a customer and turn it into a sale when they accept."
        actions={
          can("quotations.manage") && (
            <Button onClick={() => setOpen(true)}>
              <Plus size={16} /> New quotation
            </Button>
          )
        }
      />
      <NativeSelect
        aria-label="Filter by status"
        className="mb-4 max-w-[12rem]"
        value={status}
        onChange={(e) => {
          setStatus(e.target.value);
          setPage(1);
        }}
      >
        <option value="">All statuses</option>
        {["open", "converted", "expired", "cancelled"].map((v) => (
          <option key={v}>{v}</option>
        ))}
      </NativeSelect>
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(q) => q.id}
        empty="No quotations yet"
        columns={[
          { header: "Number", cell: (q) => <span className="font-medium">{q.number}</span> },
          { header: "Customer", cell: (q) => q.customer_name ?? "-" },
          { header: "Created", cell: (q) => formatDate(q.created_at) },
          { header: "Valid until", cell: (q) => (q.valid_until ? formatDate(q.valid_until) : "-") },
          { header: "Total", align: "right", cell: (q) => formatMoney(q.total) },
          { header: "Status", cell: (q) => <StatusBadge value={q.status} /> },
          {
            header: "",
            className: "text-right",
            cell: (q) => (
              <div className="flex justify-end gap-2">
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() =>
                    download(`/quotations/${q.id}/pdf`, {}, `quotation-${q.number}.pdf`).catch(
                      (e) => toast.error(errorMessage(e)),
                    )
                  }
                >
                  <Download size={14} /> PDF
                </Button>
                {q.status === "open" && can("sales.create") && (
                  <Button size="sm" variant="outline" onClick={() => setConverting(q)}>
                    Convert to sale
                  </Button>
                )}
              </div>
            ),
          },
        ]}
      />
      <Pager meta={list.data?.meta} onPage={setPage} />
      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title="New quotation"
        busy={create.isPending}
        wide
        onSubmit={() =>
          create.mutate({
            customer_id: customerId || null,
            valid_until: validUntil || undefined,
            items: lines.filter((l) => l.product_id),
          })
        }
      >
        <div className="grid grid-cols-2 gap-3">
          <Field label="Customer">
            <NativeSelect value={customerId} onChange={(e) => setCustomerId(e.target.value)}>
              <option value="">Walk-in</option>
              {customers.data
                ?.filter((c) => !c.is_walk_in)
                .map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name}
                  </option>
                ))}
            </NativeSelect>
          </Field>
          <Field label="Valid until">
            <Input type="date" value={validUntil} onChange={(e) => setValidUntil(e.target.value)} />
          </Field>
        </div>
        {lines.map((l, i) => (
          <div key={i} className="grid grid-cols-[1fr_6rem_2rem] items-end gap-2">
            <Field label={i === 0 ? "Product" : ""}>
              <NativeSelect
                value={l.product_id}
                onChange={(e) => setLine(i, { product_id: e.target.value })}
              >
                <option value="">Choose…</option>
                {products.data?.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </NativeSelect>
            </Field>
            <Field label={i === 0 ? "Qty" : ""}>
              <Input
                inputMode="decimal"
                value={l.qty}
                onChange={(e) => setLine(i, { qty: e.target.value })}
              />
            </Field>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label="Remove line"
              disabled={lines.length === 1}
              onClick={() => setLines((all) => all.filter((_, n) => n !== i))}
            >
              <Trash2 size={16} />
            </Button>
          </div>
        ))}
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => setLines((all) => [...all, { product_id: "", qty: "1" }])}
        >
          Add line
        </Button>
      </FormDialog>
      <FormDialog
        open={converting !== null}
        onOpenChange={(o) => !o && setConverting(null)}
        title={`Convert ${converting?.number ?? ""}`}
        submitLabel="Create sale"
        busy={convert.isPending}
        onSubmit={() =>
          convert.mutate({
            payments: account
              ? [{ method: "cash", money_account_id: account, amount: converting?.total }]
              : [],
          })
        }
      >
        <p className="text-sm text-muted-foreground">
          The sale uses the quoted prices. Take payment now, or leave it empty to put the sale on
          the customer's account.
        </p>
        <Field label="Received into">
          <NativeSelect value={account} onChange={(e) => setAccount(e.target.value)}>
            <option value="">Nothing yet (on credit)</option>
            {accounts.data
              ?.filter((a) => a.is_active)
              .map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
          </NativeSelect>
        </Field>
      </FormDialog>
    </>
  );
}
