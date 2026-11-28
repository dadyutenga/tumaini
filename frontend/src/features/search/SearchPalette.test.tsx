import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, useLocation } from "react-router-dom";
import { I18nProvider } from "@/features/i18n/i18n";
import { SearchPalette } from "./SearchPalette";

const account = vi.hoisted(() => ({
  isOwner: true,
  permissions: [] as string[],
  branchId: "b1" as string | null,
}));
vi.mock("@/features/auth/auth", () => {
  const can = (permission: string) => account.isOwner || account.permissions.includes(permission);
  return {
    useAuth: () => ({
      can,
      canAny: (...permissions: string[]) => permissions.some(can),
      user: { id: "u1", role_slug: account.isOwner ? "admin" : "staff" },
      me: { is_owner: account.isOwner, branches: account.isOwner ? [] : ["b1"] },
      branchId: account.branchId,
    }),
  };
});

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const SALE = { kind: "sale", id: "s1", title: "R-0001", subtitle: "Asha", link: "/sales/s1" };
const SUGAR = {
  kind: "product",
  id: "p1",
  title: "Sugar 1kg",
  subtitle: "6001234567890",
  link: "/products/p1",
};

let searches: { url: URL; branch: string | null }[] = [];
let failing = false;

async function fakeFetch(input: string, init?: RequestInit) {
  const url = new URL(input, "http://app.test");
  if (url.pathname.endsWith("/branches"))
    return json({
      data: [
        { id: "b1", name: "Main shop", is_active: true },
        { id: "b2", name: "Second shop", is_active: true },
      ],
    });
  if (url.pathname.endsWith("/products/lookup"))
    return url.searchParams.get("barcode") === SUGAR.subtitle
      ? json({ data: { id: "p1", name: SUGAR.title, sku: "SUG", barcode: SUGAR.subtitle } })
      : json({ error: { code: "not_found", message: "Product not found" } }, 404);
  if (url.pathname.endsWith("/search")) {
    searches.push({ url, branch: new Headers(init?.headers).get("X-Branch-Id") });
    if (failing) return json({ error: { code: "internal_error", message: "x" } }, 500);
    const q = (url.searchParams.get("q") ?? "").toLowerCase();
    const found = [SALE, SUGAR].filter((h) => `${h.title} ${h.subtitle}`.toLowerCase().includes(q));
    return json({
      data: {
        best: found.find((h) => [h.title, h.subtitle].some((v) => v.toLowerCase() === q)) ?? null,
        groups: found.map((h) => ({ kind: h.kind, total: 1, hits: [h] })),
      },
    });
  }
  return json({ data: [] });
}

function Where() {
  const { pathname, search } = useLocation();
  return <output data-testid="where">{pathname + search}</output>;
}

function mount(at = "/") {
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <I18nProvider>
        <MemoryRouter initialEntries={[at]}>
          <input aria-label="somewhere else" />
          <SearchPalette />
          <Where />
        </MemoryRouter>
      </I18nProvider>
    </QueryClientProvider>,
  );
}

const press = (key: string, extra: Partial<KeyboardEventInit> = {}, target?: Element) =>
  fireEvent.keyDown(target ?? document.activeElement ?? document.body, { key, ...extra });
const openPalette = () => press("k", { ctrlKey: true }, document.body);
const box = () => screen.getByRole("combobox");
const type = (value: string) => fireEvent.change(box(), { target: { value } });
const where = () => screen.getByTestId("where").textContent;
const optionNames = () => screen.queryAllByRole("option").map((o) => o.textContent ?? "");
const selected = () =>
  screen.getAllByRole("option").find((o) => o.getAttribute("aria-selected") === "true");

beforeEach(() => {
  account.isOwner = true;
  account.permissions = [];
  account.branchId = "b1";
  searches = [];
  failing = false;
  vi.stubGlobal("fetch", vi.fn(fakeFetch));
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  );
  Element.prototype.scrollIntoView = vi.fn();
});
afterEach(() => vi.unstubAllGlobals());

describe("opening", () => {
  it("opens with Ctrl+K, with ⌘K and with /", () => {
    mount();
    for (const open of [
      openPalette,
      () => press("k", { metaKey: true }, document.body),
      () => press("/", {}, document.body),
    ]) {
      open();
      expect(box()).toHaveFocus();
      press("Escape");
      expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
    }
  });

  it("leaves / alone while the user is typing somewhere", () => {
    mount();
    press("/", {}, screen.getByLabelText("somewhere else"));
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
  });

  it("gives focus back to the search button on close", async () => {
    mount();
    const trigger = screen.getByRole("button", { name: "Search (Ctrl K)" });
    fireEvent.click(trigger);
    press("Escape");
    await waitFor(() => expect(trigger).toHaveFocus());
  });

  it("is announced as a labelled combobox with a list of options", () => {
    mount();
    openPalette();
    expect(screen.getByRole("combobox", { name: "Search" })).toHaveAttribute("aria-controls");
    expect(screen.getByRole("listbox")).toBeInTheDocument();
    expect(selected()).toBeDefined();
  });

  it("fills the screen on a phone and can be closed by touch", () => {
    mount();
    openPalette();
    expect(screen.getByRole("dialog").className).toContain("max-sm:h-dvh");
    fireEvent.click(screen.getByRole("button", { name: "Close search" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });
});

describe("before and while typing", () => {
  it("offers quick actions and at most six pages", () => {
    mount();
    openPalette();
    expect(screen.getByText("Quick actions")).toBeInTheDocument();
    expect(screen.getByText("Clock in / out")).toBeInTheDocument();
    const goTo = screen.getByText("Go to").closest("[cmdk-group]") as HTMLElement;
    expect(within(goTo).getAllByRole("option")).toHaveLength(6);
  });

  it("moves with the arrow keys and opens with Enter", async () => {
    mount();
    openPalette();
    await waitFor(() => expect(selected()).toBeDefined());
    const first = selected()!.textContent;
    press("ArrowDown", {}, box());
    expect(selected()!.textContent).not.toBe(first);
    press("ArrowUp", {}, box());
    expect(selected()!.textContent).toBe(first);
    press("Enter", {}, box());
    expect(where()).toBe("/pos");
  });

  it("filters pages and actions from the first letter without asking the server", () => {
    mount();
    openPalette();
    type("p");
    expect(screen.getByText("Pages", { selector: "[cmdk-group-heading]" })).toBeInTheDocument();
    expect(searches).toHaveLength(0);
  });

  it("shows only actions after >", () => {
    mount();
    openPalette();
    type(">new");
    expect(optionNames().length).toBeGreaterThan(0);
    expect(optionNames().every((name) => name.startsWith("New"))).toBe(true);
    expect(searches).toHaveLength(0);
  });

  it("only offers the chips and actions the person may use", () => {
    account.isOwner = false;
    account.permissions = ["shop.sale.create", "shop.product.view"];
    mount();
    openPalette();
    const chips = screen.getByRole("group", { name: "Search in" });
    expect(
      within(chips)
        .getAllByRole("button")
        .map((b) => b.textContent),
    ).toEqual(["All", "Products", "Sales", "Pages"]);
    expect(screen.queryByText("New client")).not.toBeInTheDocument();
  });

  it("steps through the chips with Tab and narrows the search", async () => {
    mount();
    openPalette();
    press("Tab", {}, box());
    expect(screen.getByRole("button", { name: "Products" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    type("sugar");
    await waitFor(() => expect(searches.at(-1)?.url.searchParams.get("scope")).toBe("product"));
  });

  it("sends a phone number in one form", async () => {
    mount();
    openPalette();
    type("0712 345 678");
    await waitFor(() => expect(searches.at(-1)?.url.searchParams.get("q")).toBe("+255712345678"));
  });

  it("can widen the search to every branch", async () => {
    mount();
    openPalette();
    type("sugar");
    expect(await screen.findByText(/Searching in: Main shop/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Search everywhere" }));
    await waitFor(() => expect(searches.at(-1)?.branch).toBe("all"));
    expect(screen.getByText(/Searching in: All locations/)).toBeInTheDocument();
  });
});

describe("results", () => {
  it("puts an exact receipt number first, and Enter opens it", async () => {
    mount();
    openPalette();
    type("R-0001");
    expect(await screen.findByText("Best match")).toBeInTheDocument();
    await waitFor(() => expect(selected()?.textContent).toContain("R-0001"));
    press("Enter", {}, box());
    expect(where()).toBe("/sales/s1");
  });

  it("opens a receipt when Enter comes before the results", async () => {
    mount();
    openPalette();
    type("R-0001");
    press("Enter", {}, box());
    await waitFor(() => expect(where()).toBe("/sales/s1"));
  });

  it("adds a scanned product to the cart on the till", async () => {
    mount("/pos");
    openPalette();
    type(SUGAR.subtitle);
    // A scanner presses Enter at once.
    press("Enter", {}, box());
    await waitFor(() => expect(where()).toBe("/pos?add=p1"));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("opens a scanned product elsewhere", async () => {
    mount("/");
    openPalette();
    type(SUGAR.subtitle);
    press("Enter", {}, box());
    await waitFor(() => expect(where()).toBe("/products/p1"));
  });

  it("opens in a new tab with Ctrl+Enter", async () => {
    const opened = vi.fn();
    vi.stubGlobal("open", opened);
    mount();
    openPalette();
    type("R-0001");
    await screen.findByText("Best match");
    await waitFor(() => expect(selected()?.textContent).toContain("R-0001"));
    press("Enter", { ctrlKey: true }, box());
    expect(opened).toHaveBeenCalledWith("/sales/s1", "_blank", "noopener");
    expect(where()).toBe("/");
  });

  it("remembers what was opened and lets it be removed", async () => {
    mount();
    openPalette();
    type("R-0001");
    await screen.findByText("Best match");
    await waitFor(() => expect(selected()?.textContent).toContain("R-0001"));
    press("Enter", {}, box());
    expect(localStorage.getItem("bms-search-recent:u1")).toContain("/sales/s1");

    openPalette();
    expect(screen.getByText("Recent")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Remove R-0001 from recent" }));
    expect(screen.queryByText("Recent")).not.toBeInTheDocument();
    expect(localStorage.getItem("bms-search-recent:u1")).toBe("[]");
    // Removing does not also open it.
    expect(where()).toBe("/sales/s1");
  });

  it("offers to create what was not found", async () => {
    mount();
    openPalette();
    type("Zawadi");
    expect(await screen.findByText("Nothing matches “Zawadi”.")).toBeInTheDocument();
    expect(screen.getByText("Create product “Zawadi”")).toBeInTheDocument();
    expect(screen.getByText("Search in sales history")).toBeInTheDocument();
    fireEvent.click(screen.getByText("Create client “Zawadi”"));
    expect(where()).toBe("/clients?new=1&name=Zawadi");
  });

  it("explains a failure and can try again, keeping pages usable", async () => {
    failing = true;
    mount();
    openPalette();
    type("sales");
    expect(await screen.findByRole("alert")).toHaveTextContent("could not be completed");
    expect(screen.getByText("Pages", { selector: "[cmdk-group-heading]" })).toBeInTheDocument();
    failing = false;
    fireEvent.click(screen.getByText("Try again"));
    await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
  });
});

describe("in Swahili", () => {
  it("leaves no English on screen and still finds pages by their English name", () => {
    localStorage.setItem("bms-locale", "sw");
    mount();
    openPalette();
    const text = () => {
      const dialog = screen.getByRole("dialog");
      const labels = [...dialog.querySelectorAll("[placeholder],[aria-label]")].map(
        (el) => `${el.getAttribute("placeholder")} ${el.getAttribute("aria-label")}`,
      );
      return `${dialog.textContent} ${labels.join(" ")}`;
    };
    const english =
      /\b(Search|Recent|Quick|Actions|Pages|Go to|New|All|Sales|Products|Clients|Close|open|navigate)\b/;
    expect(text()).not.toMatch(english);
    type("sales");
    expect(screen.getByText("Mauzo", { selector: "[cmdk-item] *" })).toBeInTheDocument();
  });
});
