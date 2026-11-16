import { useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Minus, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Field, FormDialog, NativeSelect, PageHeader } from "@/components/shared/kit";
import { PageLoader } from "@/components/shared/Display";
import { useAuth } from "@/features/auth/auth";
import { useDebounce } from "@/hooks/useDebounce";
import { errorMessage, useGet, useWrite } from "@/hooks/useApi";
import { api, get, isPendingApproval, newIdempotencyKey } from "@/lib/api";
import { formatMoney, toNumber } from "@/lib/formatters";
import type { Customer, MoneyAccount, Product, Sale, Till } from "@/types";

interface CartLine {
  product: Product;
  qty: string;
}
type Method = "cash" | "mobile_money" | "bank";

export default function Pos() {
  const { can } = useAuth();
  const queryClient = useQueryClient();
  const till = useGet<Till | null>("/tills/current");
  const accounts = useGet<MoneyAccount[]>("/money-accounts", undefined, {
    enabled: can("accounts.view"),
  });
  const customers = useGet<Customer[]>("/customers", undefined, { enabled: can("customers.view") });

  const [cart, setCart] = useState<CartLine[]>([]);
  const [search, setSearch] = useState("");
  const debounced = useDebounce(search, 250);
  const [customerId, setCustomerId] = useState("");
  const [method, setMethod] = useState<Method>("cash");
  const [accountId, setAccountId] = useState("");
  const [tendered, setTendered] = useState("");
  const [receipt, setReceipt] = useState<Sale | null>(null);
  const [closing, setClosing] = useState(false);
  const idempotencyKey = useRef(newIdempotencyKey());
  const [busy, setBusy] = useState(false);

  const results = useQuery({
    queryKey: ["/products", "pos", debounced],
    enabled: debounced.length > 0,
    queryFn: async () =>
      (await api.getPage<Product>("/products", { q: debounced, per_page: 12, is_active: true }))
        .data,
  });

  const items = useMemo(() => cart.map((l) => ({ product_id: l.product.id, qty: l.qty })), [cart]);
  const preview = useQuery({
    queryKey: ["/sales/preview", items, customerId],
    enabled: items.length > 0 && items.every((i) => toNumber(i.qty) > 0),
    queryFn: () => api.post<Sale>("/sales/preview", { items, customer_id: customerId || null }),
    placeholderData: (prev) => prev,
  });

  // A new cart or a changed cart is a new sale, so it gets its own idempotency key.
  useEffect(() => {
    idempotencyKey.current = newIdempotencyKey();
  }, [cart, customerId, method, accountId, tendered]);

  if (till.isLoading) return <PageLoader />;
  if (!till.data)
    return <OpenTill accounts={accounts.data} canListAccounts={can("accounts.view")} />;

  const total = toNumber(preview.data?.total);
  const paying = tendered === "" ? total : toNumber(tendered);
  const payAccount = method === "cash" ? till.data.money_account_id : accountId;
  const typeFor: Record<Method, string> = {
    cash: "cash",
    mobile_money: "mobile_money",
    bank: "bank",
  };
  const methodAccounts = (accounts.data ?? []).filter(
    (a) => a.type === typeFor[method] && a.is_active,
  );

  function add(product: Product) {
    setCart((c) => {
      const existing = c.find((l) => l.product.id === product.id);
      if (existing)
        return c.map((l) => (l === existing ? { ...l, qty: String(toNumber(l.qty) + 1) } : l));
      return [...c, { product, qty: "1" }];
    });
    setSearch("");
  }
  const setQty = (id: string, qty: string) =>
    setCart((c) => c.map((l) => (l.product.id === id ? { ...l, qty } : l)));

  async function checkout() {
    setBusy(true);
    try {
      const payments =
        paying > 0 ? [{ method, money_account_id: payAccount, amount: String(paying) }] : [];
      const result = await api.post<Sale | { status: string }>(
        "/sales",
        { items, customer_id: customerId || null, payments },
        { idempotencyKey: idempotencyKey.current },
      );
      if (isPendingApproval(result)) {
        toast.info("This sale needs approval before it completes.");
      } else {
        setReceipt(result as Sale);
        setCart([]);
        setTendered("");
        setCustomerId("");
        void queryClient.invalidateQueries({
          predicate: (q) => /^\/(sales|products|stock|customers|money)/.test(String(q.queryKey[0])),
        });
      }
    } catch (error) {
      toast.error(errorMessage(error));
    } finally {
      setBusy(false);
    }
  }

  const canSell =
    cart.length > 0 &&
    preview.data &&
    !preview.isFetching &&
    (method === "cash" || Boolean(accountId) || paying === 0);

  return (
    <>
      <PageHeader
        title="Point of sale"
        description={`Till open since ${new Date(till.data.opened_at).toLocaleTimeString()} · float ${formatMoney(till.data.opening_float)}`}
        actions={
          <Button variant="outline" onClick={() => setClosing(true)}>
            Close till
          </Button>
        }
      />
      <div className="grid gap-6 lg:grid-cols-[1fr_380px]">
        <section>
          <Input
            autoFocus
            aria-label="Find a product"
            placeholder="Search by name, SKU, or scan a barcode…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            onKeyDown={async (e) => {
              if (e.key !== "Enter" || !search) return;
              try {
                const product = await get<Product>("/products/lookup", { barcode: search });
                if (product) add(product);
              } catch {
                /* not an exact match, keep showing search results */
              }
            }}
          />
          {debounced && (
            <ul className="mt-3 grid gap-2 sm:grid-cols-2">
              {results.data?.map((p) => (
                <li key={p.id}>
                  <button
                    type="button"
                    onClick={() => add(p)}
                    className="w-full rounded-xl border bg-card p-3 text-left transition hover:border-primary"
                  >
                    <p className="font-medium">{p.name}</p>
                    <p className="text-sm text-muted-foreground">
                      {formatMoney(p.sell_price)}
                      {p.kind === "stock" && ` · ${toNumber(p.qty_on_hand)} in stock`}
                    </p>
                  </button>
                </li>
              ))}
              {results.data?.length === 0 && (
                <li className="text-sm text-muted-foreground">No product matches.</li>
              )}
            </ul>
          )}
          <div className="mt-6 overflow-hidden rounded-2xl border bg-card">
            {cart.length === 0 ? (
              <p className="p-8 text-center text-sm text-muted-foreground">
                The cart is empty. Search for a product to start.
              </p>
            ) : (
              <ul className="divide-y">
                {cart.map((l) => {
                  const line = preview.data?.items.find((i) => i.product_id === l.product.id);
                  return (
                    <li key={l.product.id} className="flex items-center gap-3 p-3">
                      <div className="min-w-0 flex-1">
                        <p className="truncate font-medium">{l.product.name}</p>
                        <p className="text-sm text-muted-foreground">
                          {formatMoney(l.product.sell_price)} each
                        </p>
                      </div>
                      <Button
                        variant="outline"
                        size="icon"
                        aria-label="Decrease quantity"
                        onClick={() =>
                          setQty(l.product.id, String(Math.max(1, toNumber(l.qty) - 1)))
                        }
                      >
                        <Minus size={14} />
                      </Button>
                      <Input
                        aria-label={`Quantity of ${l.product.name}`}
                        className="w-16 text-center"
                        inputMode="decimal"
                        value={l.qty}
                        onChange={(e) => setQty(l.product.id, e.target.value)}
                      />
                      <Button
                        variant="outline"
                        size="icon"
                        aria-label="Increase quantity"
                        onClick={() => setQty(l.product.id, String(toNumber(l.qty) + 1))}
                      >
                        <Plus size={14} />
                      </Button>
                      <span className="w-28 text-right tabular-nums">
                        {line ? formatMoney(line.line_total) : "…"}
                      </span>
                      <Button
                        variant="ghost"
                        size="icon"
                        aria-label={`Remove ${l.product.name}`}
                        onClick={() => setCart((c) => c.filter((x) => x !== l))}
                      >
                        <Trash2 size={16} />
                      </Button>
                    </li>
                  );
                })}
              </ul>
            )}
          </div>
        </section>

        <aside className="h-fit space-y-4 rounded-2xl border bg-card p-5">
          <dl className="space-y-1 text-sm">
            <Row label="Subtotal" value={preview.data?.subtotal} />
            <Row label="Discount" value={preview.data?.discount_total} />
            <Row label="Tax included" value={preview.data?.tax_total} />
            <div className="flex justify-between border-t pt-2 text-lg font-semibold">
              <dt>Total</dt>
              <dd className="tabular-nums">{formatMoney(preview.data?.total)}</dd>
            </div>
          </dl>
          {can("customers.view") && (
            <Field label="Customer">
              <NativeSelect value={customerId} onChange={(e) => setCustomerId(e.target.value)}>
                <option value="">Walk-in</option>
                {customers.data
                  ?.filter((c) => !c.is_walk_in && c.is_active)
                  .map((c) => (
                    <option key={c.id} value={c.id}>
                      {c.name}
                    </option>
                  ))}
              </NativeSelect>
            </Field>
          )}
          <Field label="Payment">
            <NativeSelect
              value={method}
              onChange={(e) => {
                setMethod(e.target.value as Method);
                setAccountId("");
              }}
            >
              <option value="cash">Cash</option>
              <option value="mobile_money">Mobile money</option>
              <option value="bank">Bank</option>
            </NativeSelect>
          </Field>
          {method !== "cash" && (
            <Field label="Into account">
              <NativeSelect value={accountId} onChange={(e) => setAccountId(e.target.value)}>
                <option value="">Choose…</option>
                {methodAccounts.map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.name}
                  </option>
                ))}
              </NativeSelect>
            </Field>
          )}
          <Field
            label={customerId ? "Amount paid now (the rest goes on credit)" : "Amount received"}
          >
            <Input
              inputMode="decimal"
              placeholder={String(total || "")}
              value={tendered}
              onChange={(e) => setTendered(e.target.value)}
            />
          </Field>
          {method === "cash" && paying > total && total > 0 && (
            <p className="text-sm font-medium">Change: {formatMoney(paying - total)}</p>
          )}
          {!customerId && paying < total && (
            <p className="text-sm text-destructive">
              Walk-in sales must be paid in full. Choose a customer to sell on credit.
            </p>
          )}
          <Button
            className="w-full"
            size="lg"
            disabled={!canSell || busy || (!customerId && paying < total)}
            onClick={() => void checkout()}
          >
            {busy ? "Completing…" : `Complete sale ${total ? formatMoney(total) : ""}`}
          </Button>
        </aside>
      </div>

      {receipt && <ReceiptDialog sale={receipt} onClose={() => setReceipt(null)} />}
      {closing && <CloseTill till={till.data} onClose={() => setClosing(false)} />}
    </>
  );
}

function Row({ label, value }: { label: string; value: unknown }) {
  return (
    <div className="flex justify-between">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="tabular-nums">{formatMoney(value)}</dd>
    </div>
  );
}

function OpenTill({
  accounts,
  canListAccounts,
}: {
  accounts?: MoneyAccount[];
  canListAccounts: boolean;
}) {
  const [account, setAccount] = useState("");
  const [float, setFloat] = useState("0");
  const open = useWrite<unknown>("POST", "/tills/open", {
    invalidate: ["/tills"],
    success: "Till opened",
  });
  const cash = (accounts ?? []).filter((a) => a.type === "cash" && a.is_active);
  return (
    <div className="mx-auto max-w-md">
      <PageHeader
        title="Open your till"
        description="Count the cash in the drawer, then start selling."
      />
      <form
        className="space-y-4 rounded-2xl border bg-card p-6"
        onSubmit={(e) => {
          e.preventDefault();
          open.mutate({ money_account_id: account, opening_float: float || "0" });
        }}
      >
        {canListAccounts ? (
          <Field label="Cash account">
            <NativeSelect required value={account} onChange={(e) => setAccount(e.target.value)}>
              <option value="">Choose…</option>
              {cash.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
            </NativeSelect>
          </Field>
        ) : (
          <p className="rounded-md bg-amber-500/10 p-3 text-sm">
            Your role cannot list money accounts, so you cannot pick the till's cash account. Ask a
            manager to open the till or to grant "View money accounts".
          </p>
        )}
        <Field label="Opening float">
          <Input
            required
            inputMode="decimal"
            value={float}
            onChange={(e) => setFloat(e.target.value)}
          />
        </Field>
        <Button type="submit" className="w-full" disabled={open.isPending || !account}>
          Open till
        </Button>
      </form>
    </div>
  );
}

function CloseTill({ till, onClose }: { till: Till; onClose: () => void }) {
  const [counted, setCounted] = useState("");
  const [note, setNote] = useState("");
  const [result, setResult] = useState<Record<string, unknown> | null>(null);
  const close = useWrite<unknown, Record<string, unknown>>("POST", `/tills/${till.id}/close`, {
    invalidate: ["/tills", "/money"],
    onSuccess: (r) => setResult(r),
  });
  if (result) {
    return (
      <FormDialog
        open
        onOpenChange={onClose}
        title="Till closed"
        submitLabel="Done"
        onSubmit={onClose}
      >
        <dl className="space-y-1 text-sm">
          {Object.entries(result)
            .filter(([, v]) => typeof v !== "object")
            .map(([k, v]) => (
              <div key={k} className="flex justify-between">
                <dt className="capitalize text-muted-foreground">{k.replaceAll("_", " ")}</dt>
                <dd className="tabular-nums">
                  {typeof v === "string" && /^-?\d+(\.\d+)?$/.test(v) ? formatMoney(v) : String(v)}
                </dd>
              </div>
            ))}
        </dl>
      </FormDialog>
    );
  }
  return (
    <FormDialog
      open
      onOpenChange={(o) => !o && onClose()}
      title="Close till"
      description="Count the cash in the drawer and enter the total."
      submitLabel="Close till"
      busy={close.isPending}
      onSubmit={() => close.mutate({ counted_cash: counted, note })}
    >
      <Field label="Counted cash">
        <Input
          required
          inputMode="decimal"
          value={counted}
          onChange={(e) => setCounted(e.target.value)}
        />
      </Field>
      <Field label="Note">
        <Input value={note} onChange={(e) => setNote(e.target.value)} />
      </Field>
    </FormDialog>
  );
}

function ReceiptDialog({ sale, onClose }: { sale: Sale; onClose: () => void }) {
  return (
    <FormDialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={`Sale ${sale.number ?? ""} completed`}
      submitLabel="New sale"
      onSubmit={onClose}
    >
      <div id="receipt" className="space-y-2 text-sm">
        <ul className="divide-y">
          {sale.items.map((i) => (
            <li key={i.product_id} className="flex justify-between py-1">
              <span>
                {toNumber(i.qty)} × {i.product_name}
              </span>
              <span className="tabular-nums">{formatMoney(i.line_total)}</span>
            </li>
          ))}
        </ul>
        <div className="flex justify-between font-semibold">
          <span>Total</span>
          <span>{formatMoney(sale.total)}</span>
        </div>
        <div className="flex justify-between">
          <span>Paid</span>
          <span>{formatMoney(sale.paid_total)}</span>
        </div>
        {toNumber(sale.change_due) > 0 && (
          <div className="flex justify-between">
            <span>Change</span>
            <span>{formatMoney(sale.change_due)}</span>
          </div>
        )}
        {toNumber(sale.balance) > 0 && (
          <div className="flex justify-between text-destructive">
            <span>On credit</span>
            <span>{formatMoney(sale.balance)}</span>
          </div>
        )}
        {sale.id && (
          <Link className="text-primary underline" to={`/sales/${sale.id}`} onClick={onClose}>
            Open sale
          </Link>
        )}
      </div>
      <Button type="button" variant="outline" onClick={() => window.print()}>
        Print
      </Button>
    </FormDialog>
  );
}
