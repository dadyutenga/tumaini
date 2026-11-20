import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import CustomerDetail from "./CustomerDetail";
import MyWork from "./MyWork";
import People from "./People";
import ProductDetail from "./ProductDetail";
import Expenses from "./Expenses";
import SaleDetail from "./SaleDetail";
import PayrollRun from "./PayrollRun";

const authState = {
  perms: new Set<string>(),
  userId: "u-current",
  membershipId: "m-current",
  employeeId: "emp-current" as string | null,
};

vi.mock("@/features/auth/auth", () => ({
  useAuth: () => ({
    can: (k: string) => authState.perms.has(k),
    canAny: (...ks: string[]) => ks.some((k) => authState.perms.has(k)),
    limit: () => null,
    user: {
      id: authState.userId,
      membership_id: authState.membershipId,
      employee_id: authState.employeeId,
      name: "Current User",
    },
  }),
}));

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const ok = (data: unknown, status = 200) => json({ data }, status);
const page = (data: unknown[]) =>
  json({ data, meta: { page: 1, per_page: 25, total: data.length } });

function clickTab(name: string) {
  const tab = screen.getByRole("tab", { name });
  fireEvent.mouseDown(tab, { button: 0, ctrlKey: false });
  fireEvent.click(tab);
}

function fieldControl(label: string | RegExp) {
  const el = screen.getByText(label, { selector: "label" });
  const ctrl = el.parentElement?.querySelector("input, select, textarea");
  if (!ctrl) throw new Error(`No control found for label ${label}`);
  return ctrl as HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement;
}

function mountRoute(path: string, routePath: string, element: React.ReactNode) {
  return render(
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path={routePath} element={element} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
  authState.perms.clear();
  authState.userId = "u-current";
  authState.membershipId = "m-current";
  authState.employeeId = "emp-current";
});

describe("BMS backend DTO alignment regression tests", () => {
  it("CustomerDetail renders BMS statement lines (date/number/type/debit/credit/balance) without crashing on missing kind", async () => {
    authState.perms = new Set(["finance.debt.view", "finance.payment.receive"]);
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.endsWith("/customers/c1")) {
          return ok({
            id: "c1",
            name: "Acme Hardware",
            phone: "0712000111",
            email: "acme@example.com",
            address: "Dodoma",
            tax_number: null,
            credit_limit: "500000",
            is_walk_in: false,
            is_active: true,
            balance: "120000",
            notes: null,
          });
        }
        if (url.includes("/customers/c1/statement")) {
          return ok({
            balance: "120000",
            lines: [
              {
                date: "2026-11-19",
                created_at: "2026-11-19T10:00:00Z",
                number: "SAL-2026-0001",
                type: "sale_credit",
                description: "Credit sale",
                debit: "150000",
                credit: "0",
                balance: "150000",
              },
              {
                date: "2026-11-20",
                created_at: "2026-11-20T09:00:00Z",
                number: "PAY-2026-0002",
                type: "payment",
                description: "Cash payment",
                debit: "0",
                credit: "30000",
                balance: "120000",
              },
            ],
          });
        }
        return ok([]);
      }),
    );

    mountRoute("/customers/c1", "/customers/:id", <CustomerDetail />);
    expect(await screen.findByText("Acme Hardware")).toBeInTheDocument();
    expect(await screen.findByText("SAL-2026-0001")).toBeInTheDocument();
    expect(screen.getByText("sale credit")).toBeInTheDocument();
    expect(screen.getByText("Available credit")).toBeInTheDocument();
    expect(screen.queryByText("Payment terms")).not.toBeInTheDocument();
  });

  it("MyWork renders BMS /me/payslips (period_year/period_month), /me/payments (method/reference), and /me/loans (kind)", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes("/me/employee")) {
          return ok({ name: "Juma Ali", employee_no: "EMP-001" });
        }
        if (url.includes("/me/attendance")) return page([]);
        if (url.includes("/me/payslips")) {
          return page([
            {
              id: "ps1",
              number: "PS-2026-10",
              period_year: 2026,
              period_month: 10,
              gross: "800000",
              total_deductions: "50000",
              net: "750000",
            },
          ]);
        }
        if (url.includes("/me/payments")) {
          return page([
            {
              id: "pm1",
              number: "SP-001",
              payment_date: "2026-10-31",
              method: "mobile_money",
              reference: "MPESA-991",
              amount: "750000",
              status: "completed",
            },
          ]);
        }
        if (url.includes("/me/loans")) {
          return ok([
            {
              id: "ln1",
              kind: "advance",
              principal: "100000",
              balance: "50000",
              monthly_deduction: "25000",
              start_period: "2026-10-01",
              status: "active",
            },
          ]);
        }
        return ok([]);
      }),
    );

    mountRoute("/my-work", "/my-work", <MyWork />);
    expect(await screen.findByText("My work")).toBeInTheDocument();
    expect(await screen.findByText("2026-10")).toBeInTheDocument();

    clickTab("Payments");
    expect(await screen.findByText(/mobile money/)).toBeInTheDocument();
    expect(screen.getByText(/MPESA-991/)).toBeInTheDocument();

    clickTab("Loans");
    expect(await screen.findByText("advance")).toBeInTheDocument();
  });

  it("People handles non-manager /me/leave object shape without crashing", async () => {
    authState.perms = new Set([]);
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes("/me/leave")) {
          return ok({
            requests: [
              {
                id: "lr1",
                employee_id: "emp-current",
                leave_type: "Annual",
                start_date: "2026-12-01",
                end_date: "2026-12-03",
                days: "3",
                status: "requested",
              },
            ],
            balance: [],
          });
        }
        return ok([]);
      }),
    );

    mountRoute("/people", "/people", <People />);
    expect(await screen.findByText("People")).toBeInTheDocument();
    clickTab("Leave");
    expect(await screen.findByText("Annual")).toBeInTheDocument();
  });

  it("People omits base_pay and pay_type when HR staff lacks hr.compensation.manage", async () => {
    authState.perms = new Set(["hr.employee.view", "hr.employee.manage"]);
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "POST" && url.endsWith("/employees")) {
        return ok({ id: "e-new" }, 201);
      }
      if (url.includes("/employees")) {
        return page([]);
      }
      return ok([]);
    });
    vi.stubGlobal("fetch", fetchMock);

    mountRoute("/people", "/people", <People />);
    expect(await screen.findByText("People")).toBeInTheDocument();
    fireEvent.click(await screen.findByRole("button", { name: /New employee/i }));
    expect(screen.queryByText(/Monthly basic salary/i)).not.toBeInTheDocument();

    fireEvent.change(fieldControl("Full name"), { target: { value: "Asha Juma" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => {
      const postCall = fetchMock.mock.calls.find(
        ([u, init]) => String(u).endsWith("/employees") && init?.method === "POST",
      );
      expect(postCall).toBeTruthy();
      const body = JSON.parse(String(postCall?.[1]?.body));
      expect(body.name).toBe("Asha Juma");
      expect(body).not.toHaveProperty("base_pay");
      expect(body).not.toHaveProperty("pay_type");
    });
  });

  it("ProductDetail renders flat BMS /products/:id/stock-card array", async () => {
    authState.perms = new Set(["shop.stock.view"]);
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.endsWith("/products/p1")) {
          return ok({
            product: {
              id: "p1",
              name: "Cement 50kg",
              sku: "CEM-50",
              barcode: null,
              category_id: null,
              unit: "bag",
              tax_rate_id: null,
              kind: "stock",
              sell_price: "22000",
              buy_price: "18000",
              reorder_level: "10",
              is_active: true,
            },
            stock: [{ branch_id: "b1", branch: "Main", qty_on_hand: "45", avg_cost: "18000" }],
          });
        }
        if (url.includes("/products/p1/stock-card")) {
          return ok([
            {
              id: "mv1",
              created_at: "2026-11-20T08:00:00Z",
              type: "purchase_in",
              qty: "20",
              qty_after: "45",
              source_type: "purchase",
            },
          ]);
        }
        return ok([]);
      }),
    );

    mountRoute("/products/p1", "/products/:id", <ProductDetail />);
    expect(await screen.findByText("Cement 50kg")).toBeInTheDocument();
    clickTab("Stock history");
    expect(await screen.findByText("purchase in")).toBeInTheDocument();
    expect(screen.getByText("purchase")).toBeInTheDocument();
  });

  it("Expenses hides Approve/Reject on own non-recurring submitted expense and sends receipt_file_id only when present", async () => {
    authState.perms = new Set([
      "finance.expense.view",
      "finance.expense.submit",
      "finance.expense.approve",
    ]);
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "POST" && url.endsWith("/expenses")) {
        return ok({ id: "ex-new" }, 201);
      }
      if (url.includes("/expenses?")) {
        return page([
          {
            id: "ex-own",
            number: "EXP-001",
            date: "2026-11-20",
            category_id: "cat1",
            category_name: "Transport",
            amount: "15000",
            description: "Own taxi fare",
            status: "submitted",
            submitted_by: "u-current",
            requested_by: "m-current",
            recurring_id: null,
          },
          {
            id: "ex-other",
            number: "EXP-002",
            date: "2026-11-20",
            category_id: "cat1",
            category_name: "Transport",
            amount: "20000",
            description: "Staff delivery fare",
            status: "submitted",
            submitted_by: "u-someone-else",
            requested_by: "m-someone-else",
            recurring_id: null,
          },
        ]);
      }
      if (url.endsWith("/expense-categories")) {
        return ok([{ id: "cat1", name: "Transport", is_active: true }]);
      }
      return ok([]);
    });
    vi.stubGlobal("fetch", fetchMock);

    mountRoute("/expenses", "/expenses", <Expenses />);
    expect(await screen.findByText("Own taxi fare")).toBeInTheDocument();
    expect(screen.getByText("Staff delivery fare")).toBeInTheDocument();

    // Only ex-other gets Approve/Reject; ex-own cannot be self-approved.
    expect(screen.getAllByRole("button", { name: "Approve" })).toHaveLength(1);
    expect(screen.getAllByRole("button", { name: "Reject" })).toHaveLength(1);

    fireEvent.click(screen.getByRole("button", { name: /New expense/i }));
    fireEvent.change(fieldControl("Category"), { target: { value: "cat1" } });
    fireEvent.change(fieldControl("Amount"), { target: { value: "5000" } });
    fireEvent.change(fieldControl("Description"), { target: { value: "Office water" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => {
      const postCall = fetchMock.mock.calls.find(
        ([u, init]) => String(u).endsWith("/expenses") && init?.method === "POST",
      );
      expect(postCall).toBeTruthy();
      const body = JSON.parse(String(postCall?.[1]?.body));
      expect(body).not.toHaveProperty("receipt_file");
      expect(body).not.toHaveProperty("receipt_file_id");
      expect(body).not.toHaveProperty("payee");
    });
  });

  it("SaleDetail renders BMS 3-step returns and submits BMS ReturnIn shape", async () => {
    authState.perms = new Set([
      "shop.sale.view_all",
      "shop.return.create",
      "shop.return.approve",
      "finance.payment.make",
    ]);
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "POST" && url.endsWith("/sales/s1/returns")) {
        return ok({ id: "ret-new", status: "requested" }, 201);
      }
      if (url.endsWith("/sales/s1")) {
        return ok({
          id: "s1",
          number: "SAL-2026-001",
          status: "completed",
          subtotal: "50000",
          discount_total: "0",
          tax_total: "0",
          total: "50000",
          paid_total: "50000",
          balance: "0",
          items: [
            {
              id: "si1",
              product_id: "p1",
              product_name: "Socket Wrench",
              qty: "2",
              unit_price: "25000",
              discount: "0",
              tax_amount: "0",
              line_total: "50000",
              qty_returned: "0",
            },
          ],
        });
      }
      if (url.includes("/sales/s1/returns")) {
        return page([
          {
            id: "ret1",
            number: "RET-2026-001",
            status: "requested",
            resolution: "refund",
            reason: "Defective hinge",
            refund_total: "25000",
            requested_by: "u-cashier",
            created_at: "2026-11-20T10:00:00Z",
          },
          {
            id: "ret2",
            number: "RET-2026-002",
            status: "approved",
            resolution: "refund",
            reason: "Wrong item",
            refund_total: "25000",
            requested_by: "u-cashier",
            created_at: "2026-11-20T11:00:00Z",
          },
        ]);
      }
      if (url.endsWith("/money-accounts")) {
        return ok([{ id: "ma1", name: "Main Till", type: "cash", is_active: true }]);
      }
      return ok([]);
    });
    vi.stubGlobal("fetch", fetchMock);

    mountRoute("/sales/s1", "/sales/:id", <SaleDetail />);
    expect(await screen.findByText("Sale SAL-2026-001")).toBeInTheDocument();
    expect(await screen.findByText("RET-2026-001")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Approve" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Complete" })).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Return items" }));
    fireEvent.change(screen.getByLabelText("Return quantity of Socket Wrench"), {
      target: { value: "1" },
    });
    fireEvent.change(fieldControl(/Reason/i), { target: { value: "Cracked casing" } });
    fireEvent.click(screen.getByRole("button", { name: "Submit return request" }));

    await waitFor(() => {
      const postCall = fetchMock.mock.calls.find(
        ([u, init]) => String(u).endsWith("/sales/s1/returns") && init?.method === "POST",
      );
      expect(postCall).toBeTruthy();
      const body = JSON.parse(String(postCall?.[1]?.body));
      expect(body.resolution).toBe("refund");
      expect(body.reason).toBe("Cracked casing");
      expect(body.items).toEqual([
        { sale_item_id: "si1", qty: "1", condition: "resellable", restock: true },
      ]);
      expect(body).not.toHaveProperty("refund_method");
      expect(body).not.toHaveProperty("money_account_id");
    });
  });

  it("PayrollRun hides reverse button on paid runs and shows Reopen to draft on approved runs", async () => {
    authState.perms = new Set(["hr.payroll.view", "hr.payroll.manage", "hr.payroll.approve"]);
    let status = "paid";
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.endsWith("/payroll-runs/pr1")) {
          return ok({
            id: "pr1",
            number: "PR-2026-11",
            period_start: "2026-11-01",
            period_end: "2026-11-30",
            status,
            gross_total: "1000000",
            deductions_total: "100000",
            net_total: "900000",
            payslips: [],
          });
        }
        return ok([]);
      }),
    );

    const { unmount } = mountRoute("/payroll/pr1", "/payroll/:id", <PayrollRun />);
    expect(await screen.findByText("PR-2026-11")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Reverse/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Reopen to draft/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Cancel draft/i })).not.toBeInTheDocument();
    unmount();

    status = "approved";
    mountRoute("/payroll/pr1", "/payroll/:id", <PayrollRun />);
    expect(await screen.findByText("PR-2026-11")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Reopen to draft" })).toBeInTheDocument();
  });
});
