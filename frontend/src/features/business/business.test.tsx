import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import type { ReactNode } from "react";
import { useVisibleRoutes } from "@/app/useVisibleRoutes";
import Dashboard from "@/pages/Dashboard";
import Team from "@/pages/Team";
import { BranchSwitcher } from "@/components/layout/BranchSwitcher";
import { BusinessSwitcher } from "./BusinessSwitcher";
import { BusinessUnitsPanel } from "./BusinessUnitsPanel";

const account = vi.hoisted(() => ({
  branchId: null as string | null,
  chosenBranch: vi.fn(),
  units: ["A", "B"] as ("A" | "B")[],
  business: "all" as "A" | "B" | "all",
  isOwner: true,
  permissions: [] as string[],
  chosen: vi.fn(),
}));
vi.mock("@/features/auth/auth", () => {
  const can = (permission: string) => account.isOwner || account.permissions.includes(permission);
  return {
    useAuth: () => ({
      can,
      canAny: (...permissions: string[]) => permissions.some(can),
      user: { id: "u1", name: "Amina" },
      me: { is_owner: account.isOwner, business_units: account.units, branches: [] },
      business: account.business,
      setBusiness: account.chosen,
      branchId: account.branchId,
      setBranchId: account.chosenBranch,
    }),
  };
});

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const BRANCHES = [
  { id: "shop", name: "Main shop", business_unit: "A", is_active: true, is_main: true },
  { id: "store", name: "Second shop", business_unit: "A", is_active: true, is_main: false },
  { id: "office", name: "Registry office", business_unit: "B", is_active: true, is_main: false },
  { id: "desk", name: "Design desk", business_unit: "B", is_active: true, is_main: false },
];

let posted: { method: string; path: string; body: Record<string, unknown> }[] = [];

beforeEach(() => {
  account.units = ["A", "B"];
  account.business = "all";
  account.isOwner = true;
  account.permissions = [];
  account.chosen.mockReset();
  account.chosenBranch.mockReset();
  account.branchId = null;
  posted = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const path = new URL(input, "http://app.test").pathname.replace("/api/v1", "");
      if (init?.method && init.method !== "GET") {
        posted.push({ method: init.method, path, body: JSON.parse(String(init.body)) });
        return json({ data: {} }, 201);
      }
      if (path === "/business-units")
        return json({
          data: [
            { id: "a", code: "A", name: "Shop", kind: "retail_shop" },
            { id: "b", code: "B", name: "Services", kind: "professional_services" },
          ],
        });
      if (path === "/branches") return json({ data: BRANCHES });
      if (path === "/roles")
        return json({
          data: [
            { id: "staff", name: "Staff", slug: "staff", is_system: true },
            { id: "admin", name: "Admin", slug: "admin", is_system: true },
          ],
        });
      if (path === "/employees")
        return json({
          data: [
            {
              id: "e1",
              employee_no: "E-1",
              name: "Neema",
              phone: "+255700000001",
              status: "active",
            },
          ],
        });
      if (path === "/users") return json({ data: [], meta: { page: 1, per_page: 25, total: 0 } });
      if (path === "/dashboard/me")
        return json({
          data: {
            attendance: null,
            sales_today: { count: 0, total: "0" },
            my_jobs: [],
            my_tasks: [],
            low_stock: [],
          },
        });
      return json({ data: [] });
    }),
  );
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  );
});
afterEach(() => vi.unstubAllGlobals());

function mount(element: ReactNode) {
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter>{element}</MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("the business switcher", () => {
  it("offers Shop, Services and All to someone with both", () => {
    mount(<BusinessSwitcher />);
    const group = screen.getByRole("radiogroup", { name: "Business shown" });
    expect(within(group).getByRole("radio", { name: "All" })).toBeChecked();
    fireEvent.click(within(group).getByRole("radio", { name: "Services" }));
    expect(account.chosen).toHaveBeenCalledWith("B");
  });

  it("looks at every branch when the Admin chooses both businesses", () => {
    account.business = "A";
    mount(<BusinessSwitcher />);
    fireEvent.click(screen.getByRole("radio", { name: "Shop" }));
    expect(account.chosenBranch).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("radio", { name: "All" }));
    expect(account.chosenBranch).toHaveBeenCalledWith("all");
  });

  it("is hidden from someone with one business", () => {
    account.units = ["A"];
    mount(<BusinessSwitcher />);
    expect(screen.queryByRole("radiogroup")).not.toBeInTheDocument();
  });
});

function Pages() {
  return (
    <ul>
      {useVisibleRoutes().map((route) => (
        <li key={route.path}>{route.path}</li>
      ))}
    </ul>
  );
}

describe("the branch picker", () => {
  it("lists the branches of the business shown", async () => {
    account.business = "B";
    account.branchId = "office";
    mount(<BranchSwitcher />);
    expect(await screen.findByRole("option", { name: "Design desk" })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "Main shop" })).not.toBeInTheDocument();
    expect(account.chosenBranch).not.toHaveBeenCalled();
  });

  it("picks the only branch there is, since selling needs one", async () => {
    account.units = ["A"];
    account.branchId = "all";
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => json({ data: [BRANCHES[0]] })),
    );
    mount(<BranchSwitcher />);
    await waitFor(() => expect(account.chosenBranch).toHaveBeenCalledWith("shop"));
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
  });
});

describe("each business's printed details", () => {
  it("saves one business without touching the other", async () => {
    mount(<BusinessUnitsPanel />);
    const services = await screen.findByRole("region", { name: "Services" });
    fireEvent.change(services.querySelector("input")!, { target: { value: "Huduma" } });
    fireEvent.click(within(services).getByRole("button", { name: "Save business" }));
    await waitFor(() => expect(posted).toHaveLength(1));
    expect(posted[0]).toMatchObject({ method: "PATCH", path: "/business-units/B" });
  });
});

describe("the menu", () => {
  it.each([
    [["A"], ["/pos", "/stock", "/money"], ["/clients", "/design-jobs"]],
    [["B"], ["/clients", "/design-jobs", "/money"], ["/pos", "/stock", "/customers"]],
  ] as const)("shows only the pages of business %s", (units, present, absent) => {
    account.units = [...units];
    mount(<Pages />);
    for (const path of present) expect(screen.getByText(path)).toBeInTheDocument();
    for (const path of absent) expect(screen.queryByText(path)).not.toBeInTheDocument();
  });
});

describe("the staff home screen", () => {
  beforeEach(() => {
    account.isOwner = false;
    account.permissions = [
      "shop.sale.create",
      "services.project.manage",
      "services.design.manage",
      "shop.customer.manage",
    ];
  });

  it("gives a shop cashier the shop's buttons and panels", async () => {
    account.units = ["A"];
    mount(<Dashboard />);
    expect(await screen.findByText("New sale (POS)")).toBeInTheDocument();
    expect(screen.getByText("My sales today")).toBeInTheDocument();
    expect(screen.getByText("Products running low")).toBeInTheDocument();
    for (const text of ["New job", "My jobs", "My tasks"])
      expect(screen.queryByText(text)).not.toBeInTheDocument();
  });

  it("gives services staff jobs and tasks", async () => {
    account.units = ["B"];
    mount(<Dashboard />);
    expect(await screen.findByText("New job")).toBeInTheDocument();
    expect(screen.getByText("My jobs")).toBeInTheDocument();
    expect(screen.getByText("My tasks")).toBeInTheDocument();
    for (const text of ["New sale (POS)", "My sales today", "Products running low"])
      expect(screen.queryByText(text)).not.toBeInTheDocument();
  });

  it("gives someone with both all of it", async () => {
    mount(<Dashboard />);
    expect(await screen.findByText("New sale (POS)")).toBeInTheDocument();
    expect(screen.getByText("New job")).toBeInTheDocument();
  });
});

describe("business access in the Team form", () => {
  async function openForm() {
    mount(<Team />);
    fireEvent.click(await screen.findByRole("button", { name: "Grant access" }));
    const dialog = await screen.findByRole("dialog");
    const selectWith = async (option: string | RegExp) =>
      (await within(dialog).findByRole("option", { name: option })).closest("select")!;
    fireEvent.change(await selectWith(/Neema/), { target: { value: "e1" } });
    const role = await selectWith("Staff");
    const password = dialog.querySelector<HTMLInputElement>('input[type="password"]')!;
    fireEvent.change(password, { target: { value: "long-enough-1" } });
    const access = within(dialog).getByRole("group", { name: "Business access" });
    const submit = () => fireEvent.submit(password.closest("form")!);
    return { role, access, submit };
  }

  it("fills in a preset and offers only that business's branches", async () => {
    const { access, submit } = await openForm();
    for (const branch of BRANCHES)
      expect(within(access).getByRole("checkbox", { name: branch.name })).toBeInTheDocument();
    fireEvent.click(within(access).getByRole("button", { name: "Services officer" }));
    expect(within(access).getByRole("checkbox", { name: "Services (B)" })).toBeChecked();
    expect(within(access).getByRole("checkbox", { name: "Shop (A)" })).not.toBeChecked();
    expect(within(access).queryByRole("checkbox", { name: "Main shop" })).not.toBeInTheDocument();
    fireEvent.click(within(access).getByRole("checkbox", { name: "Design desk" }));
    submit();
    await waitFor(() => expect(posted).toHaveLength(1));
    expect(posted[0].path).toBe("/employees/e1/user");
    expect(posted[0].body).toMatchObject({
      role_id: "staff",
      business_units: ["B"],
      primary_business_unit: "B",
      locations: ["desk"],
    });
  });

  it("requires at least one business", async () => {
    const { access, submit } = await openForm();
    fireEvent.click(within(access).getByRole("button", { name: "Shop salesperson" }));
    fireEvent.click(within(access).getByRole("checkbox", { name: "Shop (A)" }));
    expect(within(access).getByRole("status")).toHaveTextContent("Choose at least one business.");
    submit();
    await new Promise((resolve) => setTimeout(resolve, 30));
    expect(posted).toHaveLength(0);
  });

  it("keeps an Admin in both", async () => {
    const { role, access } = await openForm();
    fireEvent.change(role, { target: { value: "admin" } });
    for (const name of ["Shop (A)", "Services (B)"]) {
      expect(within(access).getByRole("checkbox", { name })).toBeChecked();
      expect(within(access).getByRole("checkbox", { name })).toBeDisabled();
    }
  });
});
