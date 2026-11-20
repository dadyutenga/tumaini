import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { AuthProvider, ProtectedRoute, useAuth } from "./auth";

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

function mount(permission?: string) {
  render(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter initialEntries={["/private"]}>
        <AuthProvider>
          <Routes>
            <Route
              path="/private"
              element={
                <ProtectedRoute permission={permission}>
                  <div>Private</div>
                </ProtectedRoute>
              }
            />
            <Route path="/login" element={<div>Sign in</div>} />
            <Route path="/403" element={<div>Denied</div>} />
          </Routes>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

const me = (
  permissions: Record<string, unknown>,
  isOwner = false,
  limits?: Record<string, string>,
) => ({
  data: {
    user: { id: "u1", name: "Amina Owner", role_name: "Staff", branch_ids: ["b1"] },
    permissions,
    limits,
    branches: ["b1"],
    is_owner: isOwner,
  },
});

function Probe() {
  const { can, canAny, limit } = useAuth();
  return (
    <div>
      <span data-testid="can-sale">{String(can("shop.sale.create"))}</span>
      <span data-testid="can-void">{String(can("shop.sale.void"))}</span>
      <span data-testid="can-any">{String(canAny("shop.sale.void", "shop.sale.create"))}</span>
      <span data-testid="limit-discount">{limit("sale_discount_pct") ?? "none"}</span>
      <span data-testid="limit-missing">{limit("refund_approve_amount") ?? "none"}</span>
    </div>
  );
}

afterEach(() => vi.unstubAllGlobals());

describe("ProtectedRoute", () => {
  it("sends visitors without a session to login", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(json({ error: { code: "session_ended", message: "x" } }, 401)),
    );
    mount();
    expect(await screen.findByText("Sign in")).toBeInTheDocument();
  });

  it("lets a signed-in user in", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) =>
        url.endsWith("/auth/refresh") ? json({ data: { access_token: "t" } }) : json(me({})),
      ),
    );
    mount();
    expect(await screen.findByText("Private")).toBeInTheDocument();
  });

  it("denies a user whose role lacks the permission", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) =>
        url.endsWith("/auth/refresh")
          ? json({ data: { access_token: "t" } })
          : json(me({ "shop.sale.view_own": { scope: "all", limit: null } })),
      ),
    );
    mount("security.user.manage");
    expect(await screen.findByText("Denied")).toBeInTheDocument();
  });

  it("lets the owner through every permission check", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) =>
        url.endsWith("/auth/refresh") ? json({ data: { access_token: "t" } }) : json(me({}, true)),
      ),
    );
    mount("security.user.manage");
    expect(await screen.findByText("Private")).toBeInTheDocument();
  });

  it("exposes BMS permissions, canAny(), and authority limits from /me", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) =>
        url.endsWith("/auth/refresh")
          ? json({ data: { access_token: "t" } })
          : json(
              me({ "shop.sale.create": { scope: "all", limit: null } }, false, {
                sale_discount_pct: "10.00",
              }),
            ),
      ),
    );
    render(
      <QueryClientProvider client={new QueryClient()}>
        <MemoryRouter>
          <AuthProvider>
            <Probe />
          </AuthProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );
    expect(await screen.findByTestId("can-sale")).toHaveTextContent("true");
    expect(screen.getByTestId("can-void")).toHaveTextContent("false");
    expect(screen.getByTestId("can-any")).toHaveTextContent("true");
    expect(screen.getByTestId("limit-discount")).toHaveTextContent("10.00");
    expect(screen.getByTestId("limit-missing")).toHaveTextContent("none");
  });
});
