import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import Pos from "./Pos";

const authMock = {
  perms: new Set<string>(["shop.sale.create", "shop.discount.apply"]),
  limits: { sale_discount_pct: "10" } as Record<string, string>,
};

vi.mock("@/features/auth/auth", () => ({
  useAuth: () => ({
    can: (k: string) => authMock.perms.has(k),
    canAny: (...ks: string[]) => ks.some((k) => authMock.perms.has(k)),
    limit: (code: string) => authMock.limits[code] ?? null,
    user: { id: "u1", name: "Cashier" },
  }),
}));

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

function mount() {
  render(
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      <MemoryRouter>
        <Pos />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
  authMock.perms = new Set(["shop.sale.create", "shop.discount.apply"]);
  authMock.limits = { sale_discount_pct: "10" };
});

describe("POS page without legacy till gate", () => {
  it("loads products and money accounts without calling removed /tills/current and displays authority discount limit", async () => {
    const requestedPaths: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        const path = new URL(url, "http://x").pathname.replace("/api/v1", "");
        requestedPaths.push(path);
        if (path === "/products") {
          return json({
            data: [
              {
                id: "p1",
                name: "A4 Ream Paper",
                sku: "PAP-001",
                barcode: "123456",
                category_id: null,
                unit: "pcs",
                tax_rate_id: null,
                sell_price: "15000",
                reorder_level: "5",
                is_active: true,
                qty_on_hand: "20",
              },
            ],
            meta: { page: 1, per_page: 200, total: 1 },
          });
        }
        if (path === "/money-accounts") {
          return json({
            data: [
              {
                id: "acc1",
                name: "Main Cash",
                type: "cash",
                branch_id: "b1",
                provider: null,
                account_number: null,
                allow_overdraft: false,
                is_active: true,
              },
            ],
          });
        }
        if (path === "/customers")
          return json({ data: [], meta: { page: 1, per_page: 100, total: 0 } });
        if (path === "/sales/held") return json({ data: [] });
        return json({ data: [] });
      }),
    );

    mount();
    expect(await screen.findByText("A4 Ream Paper")).toBeInTheDocument();
    expect(screen.getByText(/up to 10% without approval/i)).toBeInTheDocument();
    expect(screen.getByPlaceholderText("0")).toBeInTheDocument();
    expect(screen.queryByText(/open a till/i)).not.toBeInTheDocument();
    expect(requestedPaths.some((p) => p.includes("/tills"))).toBe(false);
  });

  it("hides discount input when user lacks shop.discount.apply", async () => {
    authMock.perms = new Set(["shop.sale.create"]);
    authMock.limits = {};
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        const path = new URL(url, "http://x").pathname.replace("/api/v1", "");
        if (path === "/products")
          return json({ data: [], meta: { page: 1, per_page: 200, total: 0 } });
        return json({ data: [] });
      }),
    );

    mount();
    expect(await screen.findByRole("button", { name: /Hold cart for later/i })).toBeInTheDocument();
    expect(screen.queryByPlaceholderText("0")).not.toBeInTheDocument();
  });
});
