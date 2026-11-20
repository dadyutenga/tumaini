import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import Approvals from "./Approvals";

const authState = {
  perms: new Set<string>(),
  userId: "u-approver",
  membershipId: "m-approver",
};

vi.mock("@/features/auth/auth", () => ({
  useAuth: () => ({
    can: (k: string) => authState.perms.has(k),
    canAny: (...ks: string[]) => ks.some((k) => authState.perms.has(k)),
    limit: () => null,
    user: { id: authState.userId, membership_id: authState.membershipId },
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
        <Approvals />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
  authState.perms.clear();
});

describe("Approvals page BMS permission gating", () => {
  it("shows approve/reject only for kinds the user holds the BMS approval permission for and never for own requests", async () => {
    authState.perms = new Set(["shop.discount.approve"]);
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        json({
          data: [
            {
              id: "a1",
              kind: "discount",
              subject_type: "sale",
              subject_id: "s1",
              amount: "15000",
              summary: "15% discount on Sale #1",
              status: "pending",
              requested_by: "u-cashier",
              created_at: "2026-11-20T09:00:00Z",
            },
            {
              id: "a2",
              kind: "expense",
              subject_type: "expense",
              subject_id: "e1",
              amount: "50000",
              summary: "Fuel expense",
              status: "pending",
              requested_by: "u-cashier",
              created_at: "2026-11-20T09:05:00Z",
            },
            {
              id: "a3",
              kind: "discount",
              subject_type: "sale",
              subject_id: "s2",
              amount: "10000",
              summary: "Self-requested discount",
              status: "pending",
              requested_by: "u-approver",
              created_at: "2026-11-20T09:10:00Z",
            },
          ],
        }),
      ),
    );

    mount();
    expect(await screen.findByText("15% discount on Sale #1")).toBeInTheDocument();
    expect(screen.getByText("Fuel expense")).toBeInTheDocument();
    expect(screen.getByText("Self-requested discount")).toBeInTheDocument();

    // Only a1 (discount by another user) gets Approve/Reject buttons.
    expect(screen.getAllByRole("button", { name: "Approve" })).toHaveLength(1);
    expect(screen.getAllByRole("button", { name: "Reject" })).toHaveLength(1);
    expect(screen.getByText("Your request")).toBeInTheDocument();
  });

  it("gates BMS job_price_change and day_reopen approval types with services.price.approve and finance.day.reopen", async () => {
    authState.perms = new Set(["services.price.approve", "finance.day.reopen"]);
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        json({
          data: [
            {
              id: "a4",
              kind: "job_price_change",
              subject_type: "service_job",
              subject_id: "j1",
              amount: "45000",
              summary: "Price change on Job #12",
              status: "pending",
              requested_by: "u-tech",
              created_at: "2026-11-20T09:15:00Z",
            },
            {
              id: "a5",
              kind: "day_reopen",
              subject_type: "day_closing",
              subject_id: "dc1",
              amount: null,
              summary: "Reopen 2026-11-19",
              status: "pending",
              requested_by: "u-manager",
              created_at: "2026-11-20T09:20:00Z",
            },
            {
              id: "a6",
              kind: "day_reopen",
              subject_type: "day_closing",
              subject_id: "dc2",
              amount: null,
              summary: "Own reopen request",
              status: "pending",
              requested_by: "u-approver",
              created_at: "2026-11-20T09:25:00Z",
            },
          ],
        }),
      ),
    );

    mount();
    expect(await screen.findByText("Price change on Job #12")).toBeInTheDocument();
    expect(screen.getByText("Reopen 2026-11-19")).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "Approve" })).toHaveLength(2);
    expect(screen.getAllByRole("button", { name: "Reject" })).toHaveLength(2);
    expect(screen.getByText("Your request")).toBeInTheDocument();
  });
});
