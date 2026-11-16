import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { AuthProvider, ProtectedRoute } from "./auth";

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

const me = (permissions: Record<string, unknown>, isOwner = false) => ({
  data: {
    user: { id: "u1", name: "Amina Owner", role_name: "Staff", branch_ids: ["b1"] },
    permissions,
    branches: ["b1"],
    is_owner: isOwner,
  },
});

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
          : json(me({ "sales.view": { scope: "own", limit: null } })),
      ),
    );
    mount("users.manage");
    expect(await screen.findByText("Denied")).toBeInTheDocument();
  });

  it("lets the owner through every permission check", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) =>
        url.endsWith("/auth/refresh") ? json({ data: { access_token: "t" } }) : json(me({}, true)),
      ),
    );
    mount("users.manage");
    expect(await screen.findByText("Private")).toBeInTheDocument();
  });
});
