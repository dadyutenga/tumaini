import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  CloudOff,
  Download,
  Minus,
  Package,
  Plus,
  RefreshCw,
  Search,
  ShoppingBag,
  Trash2,
  UserPlus,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Field, FormDialog, NativeSelect, PageHeader } from "@/components/shared/kit";
import { ProductImage } from "@/components/shared/ProductImage";
import { SendOutboundButton } from "@/components/shared/SendOutbound";
import { useAuth } from "@/features/auth/auth";
import { useDebounce } from "@/hooks/useDebounce";
import { errorMessage, useGet, useWrite } from "@/hooks/useApi";
import { api, download, get, isPendingApproval, newIdempotencyKey } from "@/lib/api";
import { formatMoney, toNumber } from "@/lib/formatters";
import type { Category, Customer, MoneyAccount, Product, Sale } from "@/types";

interface CartLine {
  product: Product;
  qty: string;
  serial_number?: string;
}
type Method = "cash" | "mobile_money" | "bank";
interface PaymentRow {
  id: string;
  method: Method;
  money_account_id: string;
  amount: string;
  reference: string;
}
interface HeldCart {
  id: string;
  number: string;
  note: string | null;
  item_count: number;
  customer_name: string | null;
  created_at: string;
  cart: { customer_id: string | null; items: { product_id: string; qty: string }[] };
}
interface OfflineSale {
  idempotencyKey: string;
  createdAt: string;
  total: number;
  payload: Record<string, unknown>;
}

const DRAFT_KEY = "bms-pos-draft-v1";
const CACHE_KEY = "bms-pos-products-v1";
const QUEUE_KEY = "bms-pos-offline-queue-v1";

function readStorage<T>(key: string, fallback: T): T {
  try {
    const raw = localStorage.getItem(key);
    return raw ? (JSON.parse(raw) as T) : fallback;
  } catch {
    return fallback;
  }
}

function writeStorage(key: string, value: unknown) {
  try {
    localStorage.setItem(key, JSON.stringify(value));
  } catch {
    /* ignore quota errors */
  }
}

export default function Pos() {
  const { can, canAny, limit } = useAuth();
  const [params, setParams] = useSearchParams();
  const queryClient = useQueryClient();
  const accounts = useGet<MoneyAccount[]>("/money-accounts", { is_active: true });
  const canViewCustomers = canAny("shop.customer.view_all", "shop.customer.manage");
  const customers = useGet<Customer[]>(
    "/customers",
    { per_page: 200 },
    { enabled: canViewCustomers },
  );
  const categories = useGet<Category[]>("/categories");

  const [online, setOnline] = useState(() =>
    typeof navigator === "undefined" ? true : navigator.onLine,
  );
  const [offlineQueue, setOfflineQueue] = useState<OfflineSale[]>(() => readStorage(QUEUE_KEY, []));
  const [syncing, setSyncing] = useState(false);

  const [cart, setCart] = useState<CartLine[]>(() => readStorage<CartLine[]>(DRAFT_KEY, []));
  const [search, setSearch] = useState("");
  const [categoryId, setCategoryId] = useState("");
  const debounced = useDebounce(search, 200);
  const [customerId, setCustomerId] = useState("");
  const [discountType, setDiscountType] = useState<"amount" | "percent">("amount");
  const [discountValue, setDiscountValue] = useState("");
  const [dueDate, setDueDate] = useState("");
  const [payments, setPayments] = useState<PaymentRow[]>([
    { id: "p1", method: "cash", money_account_id: "", amount: "", reference: "" },
  ]);
  const [receipt, setReceipt] = useState<Sale | null>(null);
  const [heldOpen, setHeldOpen] = useState(false);
  const [quickCustOpen, setQuickCustOpen] = useState(false);
  const [quickCust, setQuickCust] = useState({ name: "", phone: "" });
  const [heldId, setHeldId] = useState<string | null>(null);
  const held = useGet<HeldCart[]>("/sales/held", undefined, {
    enabled: can("shop.sale.create") && online,
  });
  const idempotencyKey = useRef(newIdempotencyKey());
  const [busy, setBusy] = useState(false);

  const addQuickCustomer = useWrite<typeof quickCust, Customer>("POST", "/customers", {
    invalidate: ["/customers"],
    success: "Customer added",
    onSuccess: (c) => {
      setCustomerId(c.id);
      setQuickCust({ name: "", phone: "" });
      setQuickCustOpen(false);
    },
  });

  // Save draft cart to localStorage so reload or session timeout never loses a sale.
  useEffect(() => {
    writeStorage(DRAFT_KEY, cart);
  }, [cart]);

  // Monitor online/offline state and auto-sync queued offline sales.
  const syncOfflineSales = useCallback(async () => {
    const queue = readStorage<OfflineSale[]>(QUEUE_KEY, []);
    if (queue.length === 0 || !navigator.onLine) return;
    setSyncing(true);
    const remaining: OfflineSale[] = [];
    let synced = 0;
    for (const item of queue) {
      try {
        await api.post("/sales", item.payload, { idempotencyKey: item.idempotencyKey });
        synced++;
      } catch {
        remaining.push(item);
      }
    }
    writeStorage(QUEUE_KEY, remaining);
    setOfflineQueue(remaining);
    setSyncing(false);
    if (synced > 0) {
      toast.success(`Synced ${synced} offline sale${synced === 1 ? "" : "s"}`);
      void queryClient.invalidateQueries({
        predicate: (q) => /^\/(sales|products|stock|customers|money)/.test(String(q.queryKey[0])),
      });
    }
  }, [queryClient]);

  useEffect(() => {
    const onOnline = () => {
      setOnline(true);
      void syncOfflineSales();
    };
    const onOffline = () => setOnline(false);
    window.addEventListener("online", onOnline);
    window.addEventListener("offline", onOffline);
    return () => {
      window.removeEventListener("online", onOnline);
      window.removeEventListener("offline", onOffline);
    };
  }, [syncOfflineSales]);

  // Default product tiles + cached offline fallback.
  const results = useQuery({
    queryKey: ["/products", "pos", debounced, categoryId],
    enabled: online,
    queryFn: async () => {
      const data = (
        await api.getPage<Product>("/products", {
          q: debounced || undefined,
          category_id: categoryId || undefined,
          per_page: 24,
          is_active: true,
        })
      ).data;
      if (!debounced && !categoryId) writeStorage(CACHE_KEY, data);
      return data;
    },
  });

  const tiles = useMemo(() => {
    if (online && results.data) return results.data;
    const cached = readStorage<Product[]>(CACHE_KEY, []);
    return cached.filter(
      (p) =>
        (!categoryId || p.category_id === categoryId) &&
        (!debounced ||
          p.name.toLowerCase().includes(debounced.toLowerCase()) ||
          p.sku.toLowerCase().includes(debounced.toLowerCase()) ||
          (p.barcode ?? "").includes(debounced)),
    );
  }, [online, results.data, categoryId, debounced]);

  // Support `/pos?add=:productId` from Staff Products catalog [Sell] button.
  const addParam = params.get("add");
  useEffect(() => {
    if (!addParam || !online) return;
    void api
      .get<{ product: Product }>(`/products/${addParam}`)
      .then((res) => {
        if (res.product) add(res.product);
      })
      .catch(() => {})
      .finally(() => {
        params.delete("add");
        setParams(params, { replace: true });
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [addParam]);

  const items = useMemo(
    () =>
      cart.map((l) => ({
        product_id: l.product.id,
        qty: l.qty,
        serial_number: l.serial_number?.trim() || undefined,
      })),
    [cart],
  );
  const discountPayload =
    toNumber(discountValue) > 0 ? { type: discountType, value: discountValue } : undefined;

  const preview = useQuery({
    queryKey: ["/sales/preview", items, customerId, discountPayload],
    enabled: online && items.length > 0 && items.every((i) => toNumber(i.qty) > 0),
    queryFn: () =>
      api.post<Sale>("/sales/preview", {
        items,
        customer_id: customerId || null,
        discount: discountPayload,
      }),
    placeholderData: (prev) => prev,
  });

  useEffect(() => {
    idempotencyKey.current = newIdempotencyKey();
  }, [cart, customerId, discountType, discountValue, dueDate, payments]);

  const activeCashAccounts = useMemo(
    () => (accounts.data ?? []).filter((a) => a.type === "cash" && a.is_active),
    [accounts.data],
  );
  const defaultCashAccountId = activeCashAccounts[0]?.id ?? "";

  const offlineSubtotal = cart.reduce(
    (s, l) => s + toNumber(l.qty) * toNumber(l.product.sell_price),
    0,
  );
  const total = online ? toNumber(preview.data?.total) : offlineSubtotal;
  const selectedCustomer = (customers.data ?? []).find((c) => c.id === customerId);

  const paymentTotal = payments.reduce((sum, p, idx) => {
    if (payments.length === 1 && idx === 0 && p.amount === "") return total;
    return sum + toNumber(p.amount);
  }, 0);

  function add(product: Product) {
    if (product.qty_on_hand !== undefined && toNumber(product.qty_on_hand) <= 0) {
      toast.error(`${product.name} is out of stock.`);
      return;
    }
    setCart((c) => {
      const existing = c.find((l) => l.product.id === product.id);
      if (existing) {
        return c.map((l) => (l === existing ? { ...l, qty: String(toNumber(l.qty) + 1) } : l));
      }
      return [...c, { product, qty: "1", serial_number: "" }];
    });
    setSearch("");
  }
  const setQty = (id: string, qty: string) =>
    setCart((c) => c.map((l) => (l.product.id === id ? { ...l, qty } : l)));
  const decrementOrRemove = (id: string) =>
    setCart((c) =>
      c.flatMap((l) => {
        if (l.product.id !== id) return [l];
        const next = toNumber(l.qty) - 1;
        return next <= 0 ? [] : [{ ...l, qty: String(next) }];
      }),
    );
  const setSerial = (id: string, serial_number: string) =>
    setCart((c) => c.map((l) => (l.product.id === id ? { ...l, serial_number } : l)));

  const cartQtyById = useMemo(
    () => new Map(cart.map((l) => [l.product.id, toNumber(l.qty)])),
    [cart],
  );
  const totalCartItems = useMemo(() => cart.reduce((sum, l) => sum + toNumber(l.qty), 0), [cart]);
  const activeCategories = useMemo(
    () => (categories.data ?? []).filter((c) => c.is_active),
    [categories.data],
  );

  async function holdCart() {
    try {
      await api.post("/sales/hold", { items, customer_id: customerId || null });
      toast.success("Cart held. Resume it from Held carts.");
      setCart([]);
      setCustomerId("");
      setDiscountValue("");
      setHeldId(null);
      void queryClient.invalidateQueries({ queryKey: ["/sales/held"] });
    } catch (error) {
      toast.error(errorMessage(error));
    }
  }
  async function resume(h: HeldCart) {
    try {
      const lines = await Promise.all(
        h.cart.items.map(async (i) => ({
          product: (await api.get<{ product: Product }>(`/products/${i.product_id}`)).product,
          qty: i.qty,
        })),
      );
      setCart(lines);
      setCustomerId(h.cart.customer_id ?? "");
      setHeldId(h.id);
      setHeldOpen(false);
    } catch (error) {
      toast.error(errorMessage(error));
    }
  }
  async function discard(h: HeldCart) {
    try {
      await api.del(`/sales/held/${h.id}`);
      if (heldId === h.id) setHeldId(null);
      void queryClient.invalidateQueries({ queryKey: ["/sales/held"] });
    } catch (error) {
      toast.error(errorMessage(error));
    }
  }

  function resolveAccountId(p: PaymentRow) {
    if (p.money_account_id) return p.money_account_id;
    return p.method === "cash" ? defaultCashAccountId : "";
  }

  function buildPaymentPayload() {
    return payments
      .map((p, idx) => {
        const amt =
          payments.length === 1 && idx === 0 && p.amount === "" ? total : toNumber(p.amount);
        return {
          method: p.method,
          money_account_id: resolveAccountId(p),
          amount: String(amt),
          reference: p.reference,
        };
      })
      .filter((p) => toNumber(p.amount) > 0);
  }

  async function checkout() {
    setBusy(true);
    const payList = buildPaymentPayload();
    const payload: Record<string, unknown> = {
      items,
      customer_id: customerId || null,
      discount: discountPayload,
      payments: payList,
      due_date: dueDate || undefined,
      held_id: heldId ?? undefined,
    };

    if (!online) {
      const next: OfflineSale[] = [
        ...offlineQueue,
        {
          idempotencyKey: idempotencyKey.current,
          createdAt: new Date().toISOString(),
          total,
          payload,
        },
      ];
      writeStorage(QUEUE_KEY, next);
      setOfflineQueue(next);
      setCart([]);
      setCustomerId("");
      setDiscountValue("");
      setDueDate("");
      setPayments([{ id: "p1", method: "cash", money_account_id: "", amount: "", reference: "" }]);
      setBusy(false);
      toast.info("Saved offline. Will sync automatically when internet returns.");
      return;
    }

    try {
      const result = await api.post<Sale | { status: string }>("/sales", payload, {
        idempotencyKey: idempotencyKey.current,
      });
      if (isPendingApproval(result)) {
        toast.info("Waiting for Manager approval before this sale completes.");
        setCart([]);
        setDiscountValue("");
      } else {
        setReceipt(result as Sale);
        setCart([]);
        setCustomerId("");
        setDiscountValue("");
        setDueDate("");
        setHeldId(null);
        setPayments([
          { id: "p1", method: "cash", money_account_id: "", amount: "", reference: "" },
        ]);
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

  const paymentsValid = payments.every((p, idx) => {
    const amt = payments.length === 1 && idx === 0 && p.amount === "" ? total : toNumber(p.amount);
    if (amt <= 0) return true;
    return !online || Boolean(resolveAccountId(p));
  });
  const canSell =
    cart.length > 0 &&
    (online ? Boolean(preview.data) && !preview.isFetching : true) &&
    paymentsValid &&
    (Boolean(customerId) || paymentTotal >= total);

  return (
    <>
      <PageHeader
        title="Point of sale"
        description={
          online ? "Sell items, record payments and print receipts." : "Offline POS mode"
        }
        actions={
          <div className="flex flex-wrap items-center gap-2">
            {!online && (
              <span className="inline-flex items-center gap-1.5 rounded-full bg-amber-500/15 px-3 py-1 text-xs font-semibold text-amber-700 dark:text-amber-400">
                <CloudOff size={14} /> Offline
              </span>
            )}
            {offlineQueue.length > 0 && (
              <Button
                variant="outline"
                size="sm"
                disabled={!online || syncing}
                onClick={() => void syncOfflineSales()}
              >
                <RefreshCw size={14} /> Sync offline ({offlineQueue.length})
              </Button>
            )}
            <Button variant="outline" size="sm" onClick={() => setHeldOpen(true)}>
              Held carts{held.data?.length ? ` (${held.data.length})` : ""}
            </Button>
            {can("finance.day.close") && (
              <Button variant="outline" size="sm" asChild>
                <Link to="/day-closing">Day closing</Link>
              </Button>
            )}
          </div>
        }
      />

      <div className="grid gap-6 lg:grid-cols-[1fr_410px]">
        <section className="space-y-4">
          {/* Search & Barcode Input */}
          <div className="relative">
            <Search
              size={18}
              className="pointer-events-none absolute top-1/2 left-3.5 -translate-y-1/2 text-muted-foreground"
            />
            <Input
              autoFocus
              aria-label="Find a product"
              className="h-11 rounded-xl bg-card pl-10 text-sm shadow-xs"
              placeholder="Scan barcode or search product name / SKU…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              onKeyDown={async (e) => {
                if (e.key !== "Enter" || !search) return;
                try {
                  const product = await get<Product>("/products/lookup", { barcode: search });
                  if (product) add(product);
                } catch {
                  /* keep showing search results */
                }
              }}
            />
          </div>

          {/* Horizontal Category Filter Pills */}
          {activeCategories.length > 0 && (
            <div
              role="tablist"
              aria-label="Product categories"
              className="no-scrollbar flex items-center gap-2 overflow-x-auto pb-1"
            >
              <button
                type="button"
                role="tab"
                aria-selected={categoryId === ""}
                onClick={() => setCategoryId("")}
                className={`shrink-0 rounded-full px-3.5 py-1.5 text-xs font-semibold transition ${
                  categoryId === ""
                    ? "bg-primary text-primary-foreground shadow-xs"
                    : "border bg-card text-muted-foreground hover:border-primary/40 hover:text-foreground"
                }`}
              >
                All items
              </button>
              {activeCategories.map((c) => (
                <button
                  key={c.id}
                  type="button"
                  role="tab"
                  aria-selected={categoryId === c.id}
                  onClick={() => setCategoryId(c.id)}
                  className={`shrink-0 rounded-full px-3.5 py-1.5 text-xs font-semibold transition ${
                    categoryId === c.id
                      ? "bg-primary text-primary-foreground shadow-xs"
                      : "border bg-card text-muted-foreground hover:border-primary/40 hover:text-foreground"
                  }`}
                >
                  {c.name}
                </button>
              ))}
            </div>
          )}

          {/* E-Commerce Product Grid */}
          <ul className="grid grid-cols-2 gap-3 sm:grid-cols-3 xl:grid-cols-4">
            {tiles.slice(0, 24).map((p) => {
              const qtyOnHand = p.qty_on_hand !== undefined ? toNumber(p.qty_on_hand) : null;
              const outOfStock = qtyOnHand !== null && qtyOnHand <= 0;
              const isLow =
                !outOfStock && qtyOnHand !== null && qtyOnHand <= toNumber(p.reorder_level);
              const inCartQty = cartQtyById.get(p.id) ?? 0;

              return (
                <li key={p.id}>
                  <div
                    className={`group flex h-full flex-col overflow-hidden rounded-2xl border bg-card shadow-xs transition duration-200 ${
                      outOfStock
                        ? "opacity-55"
                        : inCartQty > 0
                          ? "border-primary ring-2 ring-primary/25 shadow-sm"
                          : "hover:-translate-y-0.5 hover:border-primary/45 hover:shadow-md"
                    }`}
                  >
                    {/* Clickable Product Image + Badges */}
                    <button
                      type="button"
                      disabled={outOfStock}
                      onClick={() => add(p)}
                      className={`relative aspect-[4/3] w-full overflow-hidden bg-muted text-left focus:outline-none ${
                        outOfStock ? "cursor-not-allowed" : "cursor-pointer"
                      }`}
                    >
                      <ProductImage fileId={p.photo_file} name={p.name} />
                      {inCartQty > 0 && (
                        <span className="absolute top-2 left-2 inline-flex items-center gap-1 rounded-full bg-primary px-2 py-0.5 text-[11px] font-bold text-primary-foreground shadow-sm">
                          ×{inCartQty}
                        </span>
                      )}
                      {qtyOnHand !== null && (
                        <span
                          className={`absolute top-2 right-2 rounded-full px-2 py-0.5 text-[10px] font-semibold shadow-xs backdrop-blur ${
                            outOfStock
                              ? "bg-destructive text-destructive-foreground"
                              : isLow
                                ? "bg-amber-500 text-white"
                                : "bg-background/90 text-foreground"
                          }`}
                        >
                          {outOfStock ? "Out" : `${qtyOnHand} left`}
                        </span>
                      )}
                    </button>

                    {/* Title, Price & Quick Add / Stepper */}
                    <div className="flex flex-1 flex-col justify-between p-3">
                      <button
                        type="button"
                        disabled={outOfStock}
                        onClick={() => add(p)}
                        className="text-left focus:outline-none"
                      >
                        <p className="line-clamp-2 text-sm font-semibold leading-snug text-foreground group-hover:text-primary">
                          {p.name}
                        </p>
                        <p className="mt-0.5 truncate text-[11px] text-muted-foreground">{p.sku}</p>
                      </button>

                      <div className="mt-2.5 space-y-2">
                        <p className="text-sm font-bold tracking-tight text-foreground tabular-nums">
                          {formatMoney(p.sell_price)}
                        </p>

                        {inCartQty > 0 ? (
                          <div className="flex items-center justify-between gap-1 rounded-xl border border-primary/30 bg-primary/8 p-0.5">
                            <Button
                              type="button"
                              size="icon"
                              variant="ghost"
                              className="h-7 w-7 rounded-lg text-primary hover:bg-primary/15"
                              aria-label={`Remove one ${p.name}`}
                              onClick={() => decrementOrRemove(p.id)}
                            >
                              <Minus size={13} />
                            </Button>
                            <span className="text-xs font-bold tabular-nums text-primary">
                              {inCartQty}
                            </span>
                            <Button
                              type="button"
                              size="icon"
                              variant="ghost"
                              className="h-7 w-7 rounded-lg text-primary hover:bg-primary/15"
                              aria-label={`Add one more ${p.name}`}
                              onClick={() => add(p)}
                            >
                              <Plus size={13} />
                            </Button>
                          </div>
                        ) : (
                          <Button
                            type="button"
                            size="sm"
                            variant={outOfStock ? "outline" : "default"}
                            disabled={outOfStock}
                            onClick={() => add(p)}
                            className="h-8 w-full rounded-xl text-xs font-semibold"
                          >
                            <Plus size={13} /> {outOfStock ? "Out of stock" : "Add"}
                          </Button>
                        )}
                      </div>
                    </div>
                  </div>
                </li>
              );
            })}
            {tiles.length === 0 && (
              <li className="col-span-full flex flex-col items-center justify-center rounded-2xl border bg-card p-10 text-center text-sm text-muted-foreground">
                <Package size={24} className="mb-2 text-primary/60" />
                No matching products.
              </li>
            )}
          </ul>

          {/* Cart */}
          <div className="overflow-hidden rounded-2xl border bg-card shadow-xs">
            <div className="flex items-center justify-between border-b bg-muted/30 px-4 py-3">
              <div className="flex items-center gap-2">
                <ShoppingBag size={16} className="text-primary" />
                <h2 className="text-sm font-semibold">
                  Selected items{totalCartItems > 0 ? ` (${totalCartItems})` : ""}
                </h2>
              </div>
              {cart.length > 0 && (
                <button
                  type="button"
                  onClick={() => {
                    setCart([]);
                    setDiscountValue("");
                  }}
                  className="text-xs font-medium text-muted-foreground hover:text-destructive"
                >
                  Clear cart
                </button>
              )}
            </div>
            {cart.length === 0 ? (
              <p className="p-8 text-center text-sm text-muted-foreground">
                The cart is empty. Tap any product card above or scan a barcode to start.
              </p>
            ) : (
              <ul className="divide-y">
                {cart.map((l) => {
                  const line = preview.data?.items.find((i) => i.product_id === l.product.id);
                  return (
                    <li key={l.product.id} className="space-y-2 p-3">
                      <div className="flex items-center gap-2.5">
                        <div className="h-11 w-11 shrink-0 overflow-hidden rounded-xl border bg-muted">
                          <ProductImage
                            fileId={l.product.photo_file}
                            name={l.product.name}
                            size="thumb"
                          />
                        </div>
                        <div className="min-w-0 flex-1">
                          <p className="truncate font-medium">{l.product.name}</p>
                          <p className="text-xs text-muted-foreground">
                            {formatMoney(l.product.sell_price)} each
                            {l.product.warranty_months
                              ? ` · ${l.product.warranty_months}m warranty`
                              : ""}
                          </p>
                        </div>
                        <Button
                          variant="outline"
                          size="icon"
                          className="h-8 w-8 rounded-lg"
                          aria-label="Decrease quantity"
                          onClick={() =>
                            setQty(l.product.id, String(Math.max(1, toNumber(l.qty) - 1)))
                          }
                        >
                          <Minus size={14} />
                        </Button>
                        <Input
                          aria-label={`Quantity of ${l.product.name}`}
                          className="h-8 w-14 text-center"
                          inputMode="decimal"
                          value={l.qty}
                          onChange={(e) => setQty(l.product.id, e.target.value)}
                        />
                        <Button
                          variant="outline"
                          size="icon"
                          className="h-8 w-8 rounded-lg"
                          aria-label="Increase quantity"
                          onClick={() => setQty(l.product.id, String(toNumber(l.qty) + 1))}
                        >
                          <Plus size={14} />
                        </Button>
                        <span className="w-24 text-right text-sm font-semibold tabular-nums">
                          {line
                            ? formatMoney(line.line_total)
                            : formatMoney(toNumber(l.qty) * toNumber(l.product.sell_price))}
                        </span>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="h-8 w-8 text-muted-foreground hover:text-destructive"
                          aria-label={`Remove ${l.product.name}`}
                          onClick={() => setCart((c) => c.filter((x) => x !== l))}
                        >
                          <Trash2 size={15} />
                        </Button>
                      </div>
                      {l.product.track_serial && (
                        <Input
                          aria-label={`Serial or IMEI for ${l.product.name}`}
                          className="h-8 text-xs"
                          placeholder="Enter or scan Serial No / IMEI…"
                          value={l.serial_number ?? ""}
                          onChange={(e) => setSerial(l.product.id, e.target.value)}
                        />
                      )}
                    </li>
                  );
                })}
              </ul>
            )}
          </div>
        </section>

        {/* Checkout Sidebar */}
        <aside className="h-fit space-y-4 rounded-2xl border bg-card p-5 shadow-sm lg:sticky lg:top-20">
          {canViewCustomers && (
            <Field label="Customer">
              <div className="flex gap-2">
                <NativeSelect
                  className="flex-1"
                  value={customerId}
                  onChange={(e) => setCustomerId(e.target.value)}
                >
                  <option value="">Walk-in</option>
                  {customers.data
                    ?.filter((c) => !c.is_walk_in && c.is_active)
                    .map((c) => (
                      <option key={c.id} value={c.id}>
                        {c.name}
                      </option>
                    ))}
                </NativeSelect>
                {can("shop.customer.manage") && (
                  <Button
                    type="button"
                    variant="outline"
                    size="icon"
                    title="Quick add customer"
                    onClick={() => setQuickCustOpen(true)}
                  >
                    <UserPlus size={16} />
                  </Button>
                )}
              </div>
              {selectedCustomer && toNumber(selectedCustomer.balance) > 0 && (
                <p className="mt-1 text-xs font-medium text-amber-600">
                  Current debt: {formatMoney(selectedCustomer.balance)}
                </p>
              )}
            </Field>
          )}

          {/* Discount */}
          {can("shop.discount.apply") && (
            <Field
              label={
                limit("sale_discount_pct")
                  ? `Discount (up to ${limit("sale_discount_pct")}% without approval)`
                  : "Discount"
              }
            >
              <div className="flex gap-2">
                <NativeSelect
                  className="w-24"
                  value={discountType}
                  onChange={(e) => setDiscountType(e.target.value as "amount" | "percent")}
                >
                  <option value="amount">TZS</option>
                  <option value="percent">%</option>
                </NativeSelect>
                <Input
                  inputMode="decimal"
                  placeholder="0"
                  value={discountValue}
                  onChange={(e) => setDiscountValue(e.target.value)}
                />
              </div>
            </Field>
          )}

          {/* Totals */}
          <dl className="space-y-1 text-sm">
            <Row label="Subtotal" value={online ? preview.data?.subtotal : offlineSubtotal} />
            <Row label="Discount" value={preview.data?.discount_total ?? "0"} />
            <Row label="Tax included" value={preview.data?.tax_total ?? "0"} />
            <div className="flex justify-between border-t pt-2 text-lg font-semibold">
              <dt>Total</dt>
              <dd className="tabular-nums">{formatMoney(total)}</dd>
            </div>
          </dl>

          {/* Split Payments */}
          <div className="space-y-2 border-t pt-3">
            <div className="flex items-center justify-between">
              <span className="text-sm font-medium">Payment</span>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="h-7 px-2 text-xs"
                onClick={() =>
                  setPayments((ps) => [
                    ...ps,
                    {
                      id: `p${Date.now()}`,
                      method: "mobile_money",
                      money_account_id: "",
                      amount: String(Math.max(0, total - paymentTotal)),
                      reference: "",
                    },
                  ])
                }
              >
                <Plus size={12} /> Split payment
              </Button>
            </div>
            {payments.map((p, idx) => {
              const methodAccounts = (accounts.data ?? []).filter(
                (a) => a.type === p.method && a.is_active,
              );
              const showAccountPicker = p.method !== "cash" || methodAccounts.length > 1;
              return (
                <div key={p.id} className="space-y-2 rounded-xl border bg-muted/20 p-2.5">
                  <div className="flex gap-2">
                    <NativeSelect
                      className="flex-1"
                      value={p.method}
                      onChange={(e) =>
                        setPayments((ps) =>
                          ps.map((x) =>
                            x.id === p.id
                              ? { ...x, method: e.target.value as Method, money_account_id: "" }
                              : x,
                          ),
                        )
                      }
                    >
                      <option value="cash">Cash</option>
                      <option value="mobile_money">Mobile money</option>
                      <option value="bank">Bank</option>
                    </NativeSelect>
                    <Input
                      className="w-32"
                      inputMode="decimal"
                      placeholder={
                        payments.length === 1 && idx === 0 ? String(total || "") : "Amount"
                      }
                      value={p.amount}
                      onChange={(e) =>
                        setPayments((ps) =>
                          ps.map((x) => (x.id === p.id ? { ...x, amount: e.target.value } : x)),
                        )
                      }
                    />
                    {payments.length > 1 && (
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        onClick={() => setPayments((ps) => ps.filter((x) => x.id !== p.id))}
                      >
                        <Trash2 size={14} />
                      </Button>
                    )}
                  </div>
                  {showAccountPicker && (
                    <div className="grid gap-2 sm:grid-cols-2">
                      <NativeSelect
                        value={
                          p.money_account_id || (p.method === "cash" ? defaultCashAccountId : "")
                        }
                        onChange={(e) =>
                          setPayments((ps) =>
                            ps.map((x) =>
                              x.id === p.id ? { ...x, money_account_id: e.target.value } : x,
                            ),
                          )
                        }
                      >
                        <option value="">Account…</option>
                        {methodAccounts.map((a) => (
                          <option key={a.id} value={a.id}>
                            {a.name}
                          </option>
                        ))}
                      </NativeSelect>
                      {p.method !== "cash" && (
                        <Input
                          placeholder="Ref (optional)"
                          value={p.reference}
                          onChange={(e) =>
                            setPayments((ps) =>
                              ps.map((x) =>
                                x.id === p.id ? { ...x, reference: e.target.value } : x,
                              ),
                            )
                          }
                        />
                      )}
                    </div>
                  )}
                </div>
              );
            })}
          </div>

          {paymentTotal > total && total > 0 && (
            <p className="text-sm font-medium text-emerald-700 dark:text-emerald-400">
              Change: {formatMoney(paymentTotal - total)}
            </p>
          )}
          {customerId && paymentTotal < total && (
            <Field label="Credit due date">
              <Input type="date" value={dueDate} onChange={(e) => setDueDate(e.target.value)} />
            </Field>
          )}
          {!customerId && paymentTotal < total && (
            <p className="text-sm text-destructive">
              Walk-in sales must be paid in full. Pick a customer to sell on credit.
            </p>
          )}

          <div className="flex gap-2">
            <Button
              type="button"
              variant="outline"
              disabled={cart.length === 0 || busy}
              onClick={() => {
                setCart([]);
                setDiscountValue("");
              }}
            >
              Cancel
            </Button>
            <Button
              className="flex-1"
              size="lg"
              disabled={!canSell || busy}
              onClick={() => void checkout()}
            >
              {busy
                ? "Completing…"
                : online
                  ? `Complete ${total ? formatMoney(total) : ""}`
                  : `Save offline ${total ? formatMoney(total) : ""}`}
            </Button>
          </div>
          {online && (
            <Button
              className="w-full"
              variant="outline"
              disabled={cart.length === 0 || busy}
              onClick={() => void holdCart()}
            >
              Hold cart for later
            </Button>
          )}
        </aside>
      </div>

      {/* Quick Add Customer Dialog */}
      <FormDialog
        open={quickCustOpen}
        onOpenChange={setQuickCustOpen}
        title="Quick add customer"
        busy={addQuickCustomer.isPending}
        onSubmit={() => addQuickCustomer.mutate(quickCust)}
      >
        <Field label="Name *">
          <Input
            required
            value={quickCust.name}
            onChange={(e) => setQuickCust({ ...quickCust, name: e.target.value })}
          />
        </Field>
        <Field label="Phone">
          <Input
            value={quickCust.phone}
            onChange={(e) => setQuickCust({ ...quickCust, phone: e.target.value })}
          />
        </Field>
      </FormDialog>

      <FormDialog
        open={heldOpen}
        onOpenChange={setHeldOpen}
        title="Held carts"
        submitLabel="Close"
        onSubmit={() => setHeldOpen(false)}
      >
        {held.data?.length ? (
          <ul className="space-y-2">
            {held.data.map((h) => (
              <li key={h.id} className="flex items-center gap-2 rounded-lg border p-3 text-sm">
                <div className="mr-auto">
                  <p className="font-medium">
                    {h.item_count} item{h.item_count === 1 ? "" : "s"}
                    {h.customer_name ? ` · ${h.customer_name}` : ""}
                  </p>
                  <p className="text-muted-foreground">
                    {new Date(h.created_at).toLocaleTimeString()}
                    {h.note ? ` · ${h.note}` : ""}
                  </p>
                </div>
                <Button size="sm" type="button" onClick={() => void resume(h)}>
                  Resume
                </Button>
                <Button size="sm" type="button" variant="ghost" onClick={() => void discard(h)}>
                  Discard
                </Button>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-sm text-muted-foreground">No held carts.</p>
        )}
      </FormDialog>

      {receipt && <ReceiptDialog sale={receipt} onClose={() => setReceipt(null)} />}
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

function ReceiptDialog({ sale, onClose }: { sale: Sale; onClose: () => void }) {
  const [isCopy, setIsCopy] = useState(false);
  const [printing, setPrinting] = useState(false);
  const summaryText = `Receipt ${sale.number ?? ""}\nTotal: ${formatMoney(sale.total)}\nPaid: ${formatMoney(sale.paid_total)}${
    toNumber(sale.balance) > 0 ? `\nBalance: ${formatMoney(sale.balance)}` : ""
  }\nThank you for your business!`;

  async function printReceipt() {
    if (!sale.id) {
      window.print();
      return;
    }
    setPrinting(true);
    try {
      const res = await api.post<{ copy: boolean; receipt_prints: number }>(
        `/sales/${sale.id}/receipt/printed`,
        {},
      );
      setIsCopy(Boolean(res?.copy));
      await new Promise<void>((r) => requestAnimationFrame(() => requestAnimationFrame(() => r())));
      window.print();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setPrinting(false);
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={`Sale ${sale.number ?? ""} completed`}
      submitLabel="New sale"
      onSubmit={onClose}
    >
      <div id="receipt" className="print-document space-y-2 text-sm">
        {isCopy && (
          <p className="rounded-md border border-dashed py-1 text-center text-xs font-bold tracking-widest uppercase">
            *** COPY ***
          </p>
        )}
        <div className="flex justify-between text-xs text-muted-foreground">
          <span>Receipt {sale.number ?? ""}</span>
          <span>{sale.customer_name || "Walk-in"}</span>
        </div>
        <ul className="divide-y">
          {sale.items.map((i) => (
            <li
              key={`${i.product_id}-${i.serial_number ?? ""}`}
              className="flex justify-between py-1"
            >
              <span>
                {toNumber(i.qty)} × {i.product_name}
                {i.serial_number ? ` [SN: ${i.serial_number}]` : ""}
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
          <Link
            className="text-primary underline print:hidden"
            to={`/sales/${sale.id}`}
            onClick={onClose}
          >
            Open sale
          </Link>
        )}
      </div>
      <div className="flex flex-wrap gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={printing}
          onClick={() => void printReceipt()}
        >
          {printing ? "Printing…" : "Print"}
        </Button>
        {sale.id && (
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() =>
              download(
                `/sales/${sale.id}/receipt`,
                { format: "pdf" },
                `receipt-${sale.number ?? sale.id}.pdf`,
              ).catch((e) => toast.error(errorMessage(e)))
            }
          >
            <Download size={14} /> Receipt PDF
          </Button>
        )}
        {sale.id && (
          <SendOutboundButton
            label="Send receipt"
            title="Send receipt"
            path={`/sales/${sale.id}/send-receipt`}
            successMessage="Receipt queued"
            phone={sale.customer_phone}
            whatsappText={summaryText}
            size="sm"
          />
        )}
      </div>
    </FormDialog>
  );
}
