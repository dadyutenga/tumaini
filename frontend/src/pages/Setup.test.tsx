import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { AuthProvider, ProtectedRoute } from "@/features/auth/auth";
import SetupPage from "./Setup";

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

afterEach(() => {
  vi.unstubAllGlobals();
  window.history.replaceState({}, "", "/");
});

describe("SetupPage", () => {
  it("creates the first account, starts its session, and opens settings", async () => {
    window.history.replaceState({}, "", "/setup");
    let setupBody: Record<string, unknown> | undefined;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
        const url = String(input);
        if (url.endsWith("/setup/status")) return json({ data: { needs_setup: true } });
        if (url.endsWith("/auth/refresh")) return json({}, 401);
        if (url.endsWith("/setup")) {
          setupBody = JSON.parse(String(init?.body)) as Record<string, unknown>;
          return json({ data: { access_token: "access-token", branch_id: "branch-1" } }, 201);
        }
        if (url.endsWith("/me")) {
          return json({
            data: {
              user: { id: "user-1", name: "Owner", branch_ids: ["branch-1"] },
              permissions: {},
              branches: ["branch-1"],
              is_owner: true,
            },
          });
        }
        throw new Error(`Unexpected request: ${url}`);
      }),
    );

    const { container } = render(
      <QueryClientProvider client={new QueryClient()}>
        <MemoryRouter initialEntries={["/setup"]}>
          <AuthProvider>
            <Routes>
              <Route path="/setup" element={<SetupPage />} />
              <Route
                path="/settings"
                element={
                  <ProtectedRoute>
                    <div>Business settings</div>
                  </ProtectedRoute>
                }
              />
            </Routes>
          </AuthProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    await screen.findByText("Set up your business");
    const textInputs = screen.getAllByRole("textbox");
    ["Tumaini", "Owner", "+255700000000", "owner@example.com"].forEach((value, index) =>
      fireEvent.change(textInputs[index], { target: { value } }),
    );
    const passwords = container.querySelectorAll('input[type="password"]');
    fireEvent.change(passwords[0], { target: { value: "secure-password" } });
    fireEvent.change(passwords[1], { target: { value: "secure-password" } });
    fireEvent.click(screen.getByRole("button", { name: "Create business" }));

    expect(await screen.findByText("Business settings")).toBeInTheDocument();
    await waitFor(() =>
      expect(setupBody).toEqual({
        business_name: "Tumaini",
        owner_name: "Owner",
        phone: "+255700000000",
        email: "owner@example.com",
        password: "secure-password",
      }),
    );
  });
});
