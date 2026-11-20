import { useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
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
import { useGet, useGetPage, useWrite } from "@/hooks/useApi";
import { api } from "@/lib/api";
import { formatDateTime, formatMoney, formatQty, toNumber } from "@/lib/formatters";
import type { Product } from "@/types";

interface Branch {
  id: string;
  name: string;
  is_active: boolean;
}
interface Transfer {
  id: string;
  number: string;
  from_branch: string;
  to_branch: string;
  status: string;
  created_at: string;
}
interface TransferDetail extends Transfer {
  items: { product_id: string; product_name: string; qty_sent: string }[];
}
interface Adjustment {
  id: string;
  number: string;
  reason: string;
  status: string;
  total_value: string;
  is_stock_take: boolean;
  created_at: string;
}
interface AdjustmentDetail extends Adjustment {
  items: { product_id: string; product_name: string; qty_system: string; qty_counted: string }[];
}
interface Movement {
  id: string;
  created_at: string;
  product_name: string;
  type?: string;
  qty: string;
  qty_after?: string;
}

function TransfersTab() {
  const { can } = useAuth();
  const [page, setPage] = useState(1);
  const list = useGetPage<Transfer>("/stock-transfers", { page });
  const branches = useGet<Branch[]>("/branches");
  const products = useGet<Product[]>("/products", { per_page: 100 });
  const [open, setOpen] = useState(false);
  const [to, setTo] = useState("");
  const [lines, setLines] = useState([{ product_id: "", qty: "1" }]);
  const [receiving, setReceiving] = useState<TransferDetail | null>(null);
  const [got, setGot] = useState<Record<string, string>>({});
  const refresh = ["/stock-transfers", "/stock", "/products"];
  const create = useWrite("POST", "/stock-transfers", {
    invalidate: refresh,
    success: "Transfer drafted",
    onSuccess: () => {
      setOpen(false);
      setLines([{ product_id: "", qty: "1" }]);
    },
  });
  const act = useWrite<{ id: string; action: string }, unknown>(
    "POST",
    (b) => `/stock-transfers/${b.id}/${b.action}`,
    { idempotent: true, invalidate: refresh, success: "Transfer updated", toBody: () => ({}) },
  );
  const receive = useWrite<unknown, unknown>(
    "POST",
    () => `/stock-transfers/${receiving?.id}/receive`,
    {
      idempotent: true,
      invalidate: refresh,
      success: "Transfer received",
      onSuccess: () => setReceiving(null),
    },
  );
  const setLine = (i: number, patch: Partial<(typeof lines)[number]>) =>
    setLines((all) => all.map((l, n) => (n === i ? { ...l, ...patch } : l)));

  async function startReceive(t: Transfer) {
    const detail = await api.get<TransferDetail>(`/stock-transfers/${t.id}`);
    setGot(Object.fromEntries(detail.items.map((i) => [i.product_id, i.qty_sent])));
    setReceiving(detail);
  }

  const manage = can("shop.stock.manage");

  return (
    <>
      <div className="mb-4 flex justify-end">
        {manage && (
          <Button onClick={() => setOpen(true)}>
            <Plus size={16} /> New transfer
          </Button>
        )}
      </div>
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(t) => t.id}
        empty="No transfers yet"
        columns={[
          { header: "Number", cell: (t) => <span className="font-medium">{t.number}</span> },
          { header: "From", cell: (t) => t.from_branch },
          { header: "To", cell: (t) => t.to_branch },
          { header: "Created", cell: (t) => formatDateTime(t.created_at) },
          { header: "Status", cell: (t) => <StatusBadge value={t.status} /> },
          {
            header: "",
            cell: (t) => (
              <div className="flex gap-2">
                {t.status === "draft" && manage && (
                  <Button size="sm" onClick={() => act.mutate({ id: t.id, action: "dispatch" })}>
                    Dispatch
                  </Button>
                )}
                {t.status === "draft" && manage && (
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => act.mutate({ id: t.id, action: "cancel" })}
                  >
                    Cancel
                  </Button>
                )}
                {t.status === "dispatched" && manage && (
                  <Button size="sm" onClick={() => void startReceive(t)}>
                    Receive
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
        title="New stock transfer"
        busy={create.isPending}
        wide
        onSubmit={() =>
          create.mutate({ to_branch_id: to, items: lines.filter((l) => l.product_id) })
        }
      >
        <Field label="Send to location">
          <NativeSelect required value={to} onChange={(e) => setTo(e.target.value)}>
            <option value="">Choose…</option>
            {branches.data
              ?.filter((b) => b.is_active)
              .map((b) => (
                <option key={b.id} value={b.id}>
                  {b.name}
                </option>
              ))}
          </NativeSelect>
        </Field>
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
        open={receiving !== null}
        onOpenChange={(o) => !o && setReceiving(null)}
        title={`Receive ${receiving?.number ?? ""}`}
        submitLabel="Receive into stock"
        busy={receive.isPending}
        onSubmit={() =>
          receive.mutate({
            items: receiving?.items.map((i) => ({
              product_id: i.product_id,
              qty_received: got[i.product_id] ?? "0",
            })),
          })
        }
      >
        <p className="text-sm text-muted-foreground">
          Enter what actually arrived. Any difference is recorded against the transfer.
        </p>
        {receiving?.items.map((i) => (
          <Field key={i.product_id} label={`${i.product_name} (sent ${formatQty(i.qty_sent)})`}>
            <Input
              inputMode="decimal"
              value={got[i.product_id] ?? ""}
              onChange={(e) => setGot((g) => ({ ...g, [i.product_id]: e.target.value }))}
            />
          </Field>
        ))}
      </FormDialog>
    </>
  );
}

function CountsTab() {
  const { can } = useAuth();
  const [page, setPage] = useState(1);
  const list = useGetPage<Adjustment>("/stock-adjustments", { page });
  const products = useGet<Product[]>("/products", { per_page: 100 });
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState("");
  const [lines, setLines] = useState([{ product_id: "", qty_counted: "0" }]);
  const [editing, setEditing] = useState<AdjustmentDetail | null>(null);
  const [counts, setCounts] = useState<Record<string, string>>({});
  const refresh = ["/stock-adjustments", "/stock", "/products", "/approvals"];
  const create = useWrite("POST", "/stock-adjustments", {
    invalidate: refresh,
    success: "Adjustment drafted",
    onSuccess: () => {
      setOpen(false);
      setReason("");
      setLines([{ product_id: "", qty_counted: "0" }]);
    },
  });
  const sheet = useWrite("POST", "/stock-adjustments/count-sheet", {
    invalidate: refresh,
    success: "Count sheet created. Open it to enter the counted quantities.",
  });
  const act = useWrite<{ id: string; action: string }, unknown>(
    "POST",
    (b) => `/stock-adjustments/${b.id}/${b.action}`,
    { idempotent: true, invalidate: refresh, success: "Adjustment updated", toBody: () => ({}) },
  );
  const save = useWrite<unknown, unknown>("PUT", () => `/stock-adjustments/${editing?.id}`, {
    invalidate: refresh,
    success: "Counts saved",
    onSuccess: () => setEditing(null),
  });
  const setLine = (i: number, patch: Partial<(typeof lines)[number]>) =>
    setLines((all) => all.map((l, n) => (n === i ? { ...l, ...patch } : l)));

  async function edit(a: Adjustment) {
    const detail = await api.get<AdjustmentDetail>(`/stock-adjustments/${a.id}`);
    setCounts(Object.fromEntries(detail.items.map((i) => [i.product_id, i.qty_counted])));
    setEditing(detail);
  }

  const manage = can("shop.stock.manage");

  return (
    <>
      <div className="mb-4 flex flex-wrap justify-end gap-2">
        {manage && (
          <>
            <Button
              variant="outline"
              disabled={sheet.isPending}
              onClick={() => sheet.mutate({ reason: "Stock take" })}
            >
              Start a stock take
            </Button>
            <Button onClick={() => setOpen(true)}>
              <Plus size={16} /> New adjustment
            </Button>
          </>
        )}
      </div>
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(a) => a.id}
        empty="No adjustments yet"
        columns={[
          { header: "Number", cell: (a) => <span className="font-medium">{a.number}</span> },
          {
            header: "Reason",
            cell: (a) => (a.is_stock_take ? `Stock take · ${a.reason}` : a.reason),
          },
          { header: "Created", cell: (a) => formatDateTime(a.created_at) },
          {
            header: "Value",
            align: "right",
            cell: (a) => (
              <span className={toNumber(a.total_value) < 0 ? "text-destructive" : ""}>
                {formatMoney(a.total_value)}
              </span>
            ),
          },
          { header: "Status", cell: (a) => <StatusBadge value={a.status} /> },
          {
            header: "",
            cell: (a) =>
              a.status === "draft" &&
              manage && (
                <div className="flex gap-2">
                  <Button size="sm" variant="outline" onClick={() => void edit(a)}>
                    Counts
                  </Button>
                  <Button size="sm" onClick={() => act.mutate({ id: a.id, action: "post" })}>
                    Post
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() => act.mutate({ id: a.id, action: "cancel" })}
                  >
                    Cancel
                  </Button>
                </div>
              ),
          },
        ]}
      />
      <Pager meta={list.data?.meta} onPage={setPage} />
      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title="New stock adjustment"
        busy={create.isPending}
        wide
        onSubmit={() => create.mutate({ reason, items: lines.filter((l) => l.product_id) })}
      >
        <Field label="Reason">
          <Input required value={reason} onChange={(e) => setReason(e.target.value)} />
        </Field>
        {lines.map((l, i) => (
          <div key={i} className="grid grid-cols-[1fr_7rem_2rem] items-end gap-2">
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
            <Field label={i === 0 ? "Counted qty" : ""}>
              <Input
                inputMode="decimal"
                value={l.qty_counted}
                onChange={(e) => setLine(i, { qty_counted: e.target.value })}
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
          onClick={() => setLines((all) => [...all, { product_id: "", qty_counted: "0" }])}
        >
          Add line
        </Button>
      </FormDialog>
      <FormDialog
        open={editing !== null}
        onOpenChange={(o) => !o && setEditing(null)}
        title={`Counts for ${editing?.number ?? ""}`}
        submitLabel="Save counts"
        busy={save.isPending}
        wide
        onSubmit={() =>
          save.mutate({
            reason: editing?.reason,
            items: editing?.items.map((i) => ({
              product_id: i.product_id,
              qty_counted: counts[i.product_id] ?? i.qty_counted,
            })),
          })
        }
      >
        {editing?.items.map((i) => (
          <Field key={i.product_id} label={`${i.product_name} (system ${formatQty(i.qty_system)})`}>
            <Input
              inputMode="decimal"
              value={counts[i.product_id] ?? ""}
              onChange={(e) => setCounts((c) => ({ ...c, [i.product_id]: e.target.value }))}
            />
          </Field>
        ))}
      </FormDialog>
    </>
  );
}

function MovementsTab() {
  const [page, setPage] = useState(1);
  const list = useGetPage<Movement>("/stock/movements", { page });
  return (
    <>
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(m) => m.id}
        empty="No stock movements yet"
        columns={[
          { header: "When", cell: (m) => formatDateTime(m.created_at) },
          { header: "Product", cell: (m) => m.product_name },
          { header: "Type", cell: (m) => (m.type ?? "-").replaceAll("_", " ") },
          { header: "Qty", align: "right", cell: (m) => formatQty(m.qty) },
          {
            header: "After",
            align: "right",
            cell: (m) => (m.qty_after ? formatQty(m.qty_after) : "-"),
          },
        ]}
      />
      <Pager meta={list.data?.meta} onPage={setPage} />
    </>
  );
}

export default function StockOps() {
  const { canAny } = useAuth();
  const canView = canAny("shop.stock.view", "shop.stock.manage");
  return (
    <>
      <PageHeader
        title="Transfers and counts"
        description="Move stock between locations, count what is on the shelf and correct differences."
      />
      <Tabs defaultValue="transfers">
        <TabsList className="mb-4">
          {canView && <TabsTrigger value="transfers">Transfers</TabsTrigger>}
          <TabsTrigger value="counts">Counts and adjustments</TabsTrigger>
          <TabsTrigger value="movements">Movements</TabsTrigger>
        </TabsList>
        {canView && (
          <TabsContent value="transfers">
            <TransfersTab />
          </TabsContent>
        )}
        <TabsContent value="counts">
          <CountsTab />
        </TabsContent>
        <TabsContent value="movements">
          <MovementsTab />
        </TabsContent>
      </Tabs>
    </>
  );
}
