import { Suspense, type ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { routes } from "@/app/routes";
import DashboardLayout from "@/components/layout/DashboardLayout";
import { AuthProvider } from "@/features/auth/auth";
import AuthPage from "@/pages/Auth";
import ClientApproval from "@/pages/ClientApproval";
import ForgotPassword from "@/pages/ForgotPassword";
import Status from "@/pages/Status";
import { I18nProvider } from "./i18n";

/** Common English words that must not survive on a Swahili screen. */
const ENGLISH = ["Save", "Cancel", "Search", "Total", "Status", "Date", "New", "Delete"];

/** Everything a person can read or hear: text, placeholders, labels and tooltips. */
function readable(root: HTMLElement) {
  const attributes = ["placeholder", "aria-label", "title", "alt"];
  const spoken = [...root.querySelectorAll("*")].flatMap((el) =>
    attributes.map((name) => el.getAttribute(name) ?? ""),
  );
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  const text: string[] = [];
  while (walker.nextNode()) text.push(walker.currentNode.textContent ?? "");
  return [...text, ...spoken].join("\n");
}
const englishIn = (text: string) =>
  ENGLISH.filter((word) => new RegExp(`\\b${word}\\b`, "i").test(text));

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

/** An owner is signed in; every other request fails, which shows each page's own wording. */
const fakeFetch = async (url: string) => {
  if (url.endsWith("/auth/refresh")) return json({ data: { access_token: "t" } });
  if (url.endsWith("/api/v1/me"))
    return json({
      data: {
        user: { id: "u1", name: "Amina", role_name: "Msimamizi", branch_ids: [] },
        permissions: {},
        branches: [],
        is_owner: true,
      },
    });
  return json({ error: { code: "not_found", message: "…" } }, 404);
};

beforeEach(() => {
  localStorage.clear();
  localStorage.setItem("bms-locale", "sw");
  vi.stubGlobal("fetch", vi.fn(fakeFetch));
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  );
  vi.stubGlobal("matchMedia", () => ({
    matches: false,
    addEventListener() {},
    removeEventListener() {},
  }));
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

async function show(pattern: string, element: ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <I18nProvider>
        <MemoryRouter initialEntries={[pattern.replace(/:\w+/g, "x1")]}>
          <AuthProvider>
            <Suspense fallback={<div data-testid="loading" />}>
              <Routes>
                <Route path={pattern} element={element} />
              </Routes>
            </Suspense>
          </AuthProvider>
        </MemoryRouter>
      </I18nProvider>
    </QueryClientProvider>,
  );
  await waitFor(() => expect(screen.queryByTestId("loading")).not.toBeInTheDocument(), {
    timeout: 10_000,
  });
  await waitFor(() => expect(client.isFetching()).toBe(0));
  // Let the signed-in user arrive and the page react to it.
  await new Promise((resolve) => setTimeout(resolve, 20));
  await waitFor(() => expect(client.isFetching()).toBe(0));
  expect(document.documentElement.lang).toBe("sw");
  expect(document.body.textContent?.trim()).toBeTruthy();
}

describe("every screen in Swahili", () => {
  it("would notice English", () => {
    expect(englishIn("Hifadhi\nSave changes\nJumla")).toEqual(["Save"]);
  });

  it.each(routes.map((r) => [r.path, r.component] as const))(
    "%s",
    async (path, Component) => {
      await show(path, <Component />);
      expect(englishIn(readable(document.body))).toEqual([]);
    },
    20_000,
  );

  it.each([
    ["/login", <AuthPage mode="login" />],
    ["/register", <AuthPage mode="register" />],
    ["/forgot-password", <ForgotPassword />],
    ["/approve/:token", <ClientApproval />],
    ["/403", <Status code="403" />],
    ["/404", <Status code="404" />],
    ["/500", <Status code="500" />],
  ] as const)("%s", async (path, element) => {
    await show(path, element);
    expect(englishIn(readable(document.body))).toEqual([]);
  });

  it("the shell around the pages", async () => {
    await show("/", <DashboardLayout />);
    await screen.findAllByText("Muhtasari");
    expect(englishIn(readable(document.body))).toEqual([]);
  });
});
