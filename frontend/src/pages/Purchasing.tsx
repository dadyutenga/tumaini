import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Plus, Search, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  DataGrid,
  Field,
  FilterBar,
  FormDialog,
  NativeSelect,
  PageHeader,
  Pager,
  SegmentedChips,
  StatusBadge,
} from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { PdfButton } from "@/features/services/shared";
import { useGet, useGetPage, useWrite } from "@/hooks/useApi";
import { formatDate, formatMoney, toNumber } from "@/lib/formatters";
import type { MoneyAccount, Product } from "@/types";

export interface Supplier {
  id: string;
  name: string;
  phone?: string | null;
  contact_person?: string | null;
  payment_terms_days: number;
  balance: string;
  is_active: boolean;
}
interface Order {
  id: string;
  number: string;
  supplier: string;
  order_date: string;
  status: string;
  total: string;
  qty_ordered: string;
  qty_received: string;
}
interface Receipt {
  id: string;
  number: string;
  supplier?: string;
  supplier_id?: string;
  received_date?: string;
  total_value?: string;
}

function OrdersTab({ suppliers }: { suppliers: Supplier[] }) {
  const { can } = useAuth();
  const navigate = useNavigate();
  const [status, setStatus] = useState("");
  const [page, setPage] = useState(1);
  const list = useGetPage<Order>("/purchase-orders", { status: status || undefined, page });
  const products = useGet<Product[]>("/products", { per_page: 100 });
  const [open, setOpen] = useState(false);
  const [supplierId, setSupplierId] = useState("");
  const [lines, setLines] = useState([{ product_id: "", qty: "1", unit_cost: "0" }]);
  const create = useWrite<unknown, { id: string }>("POST", "/purchase-orders", {
    invalidate: ["/purchase-orders"],
    success: "Order saved as a draft",
    onSuccess: (o) => {
      setOpen(false);
      navigate(`/purchasing/${o.id}`);
    },
  });
  const setLine = (i: number, patch: Partial<(typeof lines)[number]>) =>
    setLines((all) => all.map((l, n) => (n === i ? { ...l, ...patch } : l)));

  return (
    <>
      <FilterBar className="justify-between">
        <SegmentedChips
          ariaLabel="Filter by status"
          value={status}
          onChange={(v) => {
            setStatus(v);
            setPage(1);
          }}
          options={[
            { value: "", label: "All" },
            { value: "draft", label: "Draft" },
            { value: "ordered", label: "Ordered" },
            { value: "partially_received", label: "Partial" },
            { value: "received", label: "Received" },
            { value: "cancelled", label: "Cancelled" },
          ]}
        />
        {can("shop.purchase.manage") && (
          <Button size="sm" onClick={() => setOpen(true)}>
            <Plus size={14} /> New order
          </Button>
        )}
      </FilterBar>

      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(o) => o.id}
        empty="No purchase orders yet"
        onRowClick={(o) => navigate(`/purchasing/${o.id}`)}
        columns={[
          {
            header: "Number",
            sortValue: (o) => o.number,
            cell: (o) => <span className="od-mono font-medium">{o.number}</span>,
          },
          { header: "Supplier", sortValue: (o) => o.supplier, cell: (o) => o.supplier },
          {
            header: "Date",
            sortValue: (o) => o.order_date,
            cell: (o) => (
              <span className="text-[var(--od-ink-secondary)]">{formatDate(o.order_date)}</span>
            ),
          },
          {
            header: "Total",
            align: "right",
            sortValue: (o) => toNumber(o.total),
            cell: (o) => <span className="font-medium">{formatMoney(o.total)}</span>,
          },
          {
            header: "Received",
            align: "right",
            cell: (o) => `${toNumber(o.qty_received)} / ${toNumber(o.qty_ordered)}`,
          },
          { header: "Status", cell: (o) => <StatusBadge value={o.status} /> },
        ]}
      />
      <Pager meta={list.data?.meta} onPage={setPage} />

      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title="New purchase order"
        busy={create.isPending}
        wide
        onSubmit={() =>
          create.mutate({
            supplier_id: supplierId,
            items: lines.filter((l) => l.product_id),
          })
        }
      >
        <Field label="Supplier">
          <NativeSelect required value={supplierId} onChange={(e) => setSupplierId(e.target.value)}>
            <option value="">Choose…</option>
            {suppliers
              .filter((s) => s.is_active)
              .map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
          </NativeSelect>
        </Field>
        <div className="space-y-2">
          {lines.map((l, i) => (
            <div key={i} className="grid grid-cols-[1fr_5rem_7rem_2.25rem] items-end gap-2">
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
              <Field label={i === 0 ? "Unit cost" : ""}>
                <Input
                  inputMode="decimal"
                  value={l.unit_cost}
                  onChange={(e) => setLine(i, { unit_cost: e.target.value })}
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
                <Trash2 size={15} />
              </Button>
            </div>
          ))}
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() =>
              setLines((all) => [...all, { product_id: "", qty: "1", unit_cost: "0" }])
            }
          >
            <Plus size={13} /> Add line
          </Button>
        </div>
      </FormDialog>
    </>
  );
}

function SuppliersTab() {
  const { can, canAny } = useAuth();
  const [q, setQ] = useState("");
  const [page, setPage] = useState(1);
  const [stmtFrom, setStmtFrom] = useState("");
  const [stmtTo, setStmtTo] = useState("");
  const invalidStmtRange = Boolean(stmtFrom && stmtTo && stmtTo < stmtFrom);
  const list = useGetPage<Supplier>("/suppliers", { q: q || undefined, page });
  const accounts = useGet<MoneyAccount[]>("/money-accounts", undefined, {
    enabled: canAny("finance.account.view", "finance.payment.make", "finance.payment.receive"),
  });
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState({
    name: "",
    phone: "",
    contact_person: "",
    payment_terms_days: "0",
  });
  const [paying, setPaying] = useState<Supplier | null>(null);
  const [pay, setPay] = useState({ amount: "", money_account_id: "", reference: "" });
  const create = useWrite("POST", "/suppliers", {
    invalidate: ["/suppliers"],
    success: "Supplier added",
    onSuccess: () => {
      setOpen(false);
      setDraft({ name: "", phone: "", contact_person: "", payment_terms_days: "0" });
    },
  });
  const payment = useWrite<unknown, unknown>("POST", () => `/suppliers/${paying?.id}/payments`, {
    idempotent: true,
    invalidate: ["/suppliers", "/money", "/purchase-orders"],
    success: "Payment recorded",
    onSuccess: () => {
      setPaying(null);
      setPay({ amount: "", money_account_id: "", reference: "" });
    },
  });

  return (
    <>
      <div className="mb-4 space-y-2">
        <FilterBar className="mb-0 justify-between">
          <div className="flex flex-1 flex-wrap items-center gap-2">
            <div className="relative min-w-[200px] flex-1 sm:max-w-xs">
              <Search
                size={15}
                className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-[var(--od-ink-muted)]"
              />
              <Input
                aria-label="Search suppliers"
                className="pl-9"
                placeholder="Search suppliers…"
                value={q}
                onChange={(e) => {
                  setQ(e.target.value);
                  setPage(1);
                }}
              />
            </div>
            <Input
              type="date"
              aria-label="Statement from date"
              className="w-36"
              value={stmtFrom}
              onChange={(e) => setStmtFrom(e.target.value)}
            />
            <Input
              type="date"
              aria-label="Statement to date"
              className="w-36"
              value={stmtTo}
              onChange={(e) => setStmtTo(e.target.value)}
            />
          </div>
          {can("shop.supplier.manage") && (
            <Button size="sm" onClick={() => setOpen(true)}>
              <Plus size={14} /> New supplier
            </Button>
          )}
        </FilterBar>
        {invalidStmtRange && (
          <p className="text-xs font-medium text-[var(--od-bad)]">to must not be before from</p>
        )}
      </div>

      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(s) => s.id}
        empty="No suppliers yet"
        columns={[
          {
            header: "Name",
            sortValue: (s) => s.name,
            cell: (s) => <span className="font-medium">{s.name}</span>,
          },
          {
            header: "Contact",
            cell: (s) => (
              <span className="text-[var(--od-ink-secondary)]">
                {s.contact_person ?? s.phone ?? "—"}
              </span>
            ),
          },
          {
            header: "Terms",
            cell: (s) => (
              <span className="od-tabular text-[var(--od-ink-secondary)]">
                {s.payment_terms_days} days
              </span>
            ),
          },
          {
            header: "We owe",
            align: "right",
            sortValue: (s) => toNumber(s.balance),
            cell: (s) => (
              <span className={toNumber(s.balance) > 0 ? "font-semibold text-[var(--od-bad)]" : ""}>
                {formatMoney(s.balance)}
              </span>
            ),
          },
          {
            header: "",
            className: "text-right",
            cell: (s) => (
              <div className="flex justify-end gap-2">
                <PdfButton
                  size="sm"
                  disabled={invalidStmtRange}
                  path={`/suppliers/${s.id}/statement`}
                  query={{ from: stmtFrom, to: stmtTo }}
                  filename={`supplier-statement-${s.name}.pdf`}
                >
                  Statement
                </PdfButton>
                {can("finance.payment.make") && toNumber(s.balance) > 0 && (
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => {
                      setPaying(s);
                      setPay((p) => ({ ...p, amount: String(toNumber(s.balance)) }));
                    }}
                  >
                    Pay
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
        title="New supplier"
        busy={create.isPending}
        onSubmit={() =>
          create.mutate({ ...draft, payment_terms_days: Number(draft.payment_terms_days) || 0 })
        }
      >
        <Field label="Name">
          <Input
            required
            value={draft.name}
            onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))}
          />
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label="Phone">
            <Input
              value={draft.phone}
              onChange={(e) => setDraft((d) => ({ ...d, phone: e.target.value }))}
            />
          </Field>
          <Field label="Contact person">
            <Input
              value={draft.contact_person}
              onChange={(e) => setDraft((d) => ({ ...d, contact_person: e.target.value }))}
            />
          </Field>
        </div>
        <Field label="Payment terms (days)">
          <Input
            inputMode="numeric"
            value={draft.payment_terms_days}
            onChange={(e) => setDraft((d) => ({ ...d, payment_terms_days: e.target.value }))}
          />
        </Field>
      </FormDialog>

      <FormDialog
        open={paying !== null}
        onOpenChange={(o) => !o && setPaying(null)}
        title={`Pay ${paying?.name ?? "supplier"}`}
        submitLabel="Pay"
        busy={payment.isPending}
        onSubmit={() => payment.mutate(pay)}
      >
        <Field label="Amount">
          <Input
            required
            inputMode="decimal"
            value={pay.amount}
            onChange={(e) => setPay((p) => ({ ...p, amount: e.target.value }))}
          />
        </Field>
        <Field label="Pay from">
          <NativeSelect
            required
            value={pay.money_account_id}
            onChange={(e) => setPay((p) => ({ ...p, money_account_id: e.target.value }))}
          >
            <option value="">Choose…</option>
            {accounts.data
              ?.filter((a) => a.is_active)
              .map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
          </NativeSelect>
        </Field>
        <Field label="Reference">
          <Input
            value={pay.reference}
            onChange={(e) => setPay((p) => ({ ...p, reference: e.target.value }))}
          />
        </Field>
      </FormDialog>
    </>
  );
}

function ReceiptsTab() {
  const [page, setPage] = useState(1);
  const list = useGetPage<Receipt>("/goods-receipts", { page });
  return (
    <>
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(r) => r.id}
        empty="No goods received yet"
        columns={[
          {
            header: "Number",
            cell: (r) => <span className="od-mono font-medium">{r.number}</span>,
          },
          { header: "Supplier", cell: (r) => r.supplier ?? "—" },
          {
            header: "Received",
            cell: (r) => (r.received_date ? formatDate(r.received_date) : "—"),
          },
          {
            header: "Value",
            align: "right",
            cell: (r) => <span className="font-medium">{formatMoney(r.total_value)}</span>,
          },
        ]}
      />
      <Pager meta={list.data?.meta} onPage={setPage} />
    </>
  );
}

export default function Purchasing() {
  const { can, canAny } = useAuth();
  const canOrders = can("shop.purchase.manage");
  const canSuppliers = canAny(
    "shop.supplier.manage",
    "shop.purchase.manage",
    "finance.debt.view",
    "finance.payment.make",
  );
  const suppliers = useGet<Supplier[]>("/suppliers", { per_page: 100 }, { enabled: canSuppliers });
  return (
    <div className="space-y-6">
      <PageHeader
        title="Purchasing"
        description="Suppliers, purchase orders, goods received and balances owed."
      />
      <Tabs defaultValue={canOrders ? "orders" : "suppliers"}>
        <TabsList>
          {canOrders && <TabsTrigger value="orders">Orders</TabsTrigger>}
          {canSuppliers && <TabsTrigger value="suppliers">Suppliers</TabsTrigger>}
          {canOrders && <TabsTrigger value="receipts">Receipts</TabsTrigger>}
        </TabsList>
        {canOrders && (
          <TabsContent value="orders">
            <OrdersTab suppliers={suppliers.data ?? []} />
          </TabsContent>
        )}
        {canSuppliers && (
          <TabsContent value="suppliers">
            <SuppliersTab />
          </TabsContent>
        )}
        {canOrders && (
          <TabsContent value="receipts">
            <ReceiptsTab />
          </TabsContent>
        )}
      </Tabs>
    </div>
  );
}
