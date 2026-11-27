import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { AuthProvider } from "@/features/auth/auth";
import MyWork from "./MyWork";

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

/** Signs in a user and answers /me/employee with the given error. */
function mount(isOwner: boolean, code: string, message: string, status: number) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      if (url.endsWith("/auth/refresh")) return json({ data: { access_token: "t" } });
      if (url.includes("/me/employee")) return json({ error: { code, message } }, status);
      if (url.endsWith("/me"))
        return json({
          data: {
            user: { id: "u1", name: "Amina Test", phone: "+255700000001", branch_ids: [] },
            permissions: {},
            branches: [],
            is_owner: isOwner,
          },
        });
      return json({ data: [] });
    }),
  );
  render(
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      <MemoryRouter>
        <AuthProvider>
          <MyWork />
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

afterEach(() => vi.unstubAllGlobals());

describe("My work without an employee record", () => {
  it("tells staff to ask an Admin and which account is theirs", async () => {
    mount(false, "employee_required", "not linked", 404);
    expect(await screen.findByText("No employee profile linked")).toBeInTheDocument();
    expect(await screen.findByText("Amina Test, +255700000001")).toBeInTheDocument();
    expect(screen.getByText(/Ask an Admin to link it/)).toBeInTheDocument();
    expect(screen.queryByText("Open Team and roles")).not.toBeInTheDocument();
  });

  it("gives an Admin the steps and the pages to use", async () => {
    mount(true, "employee_required", "not linked", 404);
    expect(await screen.findByRole("link", { name: "Open Team and roles" })).toHaveAttribute(
      "href",
      "/team",
    );
    expect(screen.getByRole("link", { name: "Add employee" })).toHaveAttribute(
      "href",
      "/people?new=1",
    );
  });

  it("does not blame a missing link for other errors", async () => {
    mount(false, "internal_error", "Something went wrong.", 500);
    expect(await screen.findByText("Could not load your work")).toBeInTheDocument();
    expect(screen.queryByText("No employee profile linked")).not.toBeInTheDocument();
  });
});
