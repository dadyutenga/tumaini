import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import Team from "./Team";
import { ErrorBoundary } from "@/components/shared/RouteErrors";

vi.mock("@/features/auth/auth", () => ({ useAuth: () => ({ can: () => true }) }));
vi.mock("@/hooks/useApi", () => ({
  useGetPage: () => ({
    data: {
      data: [{ id: "u1", name: "Demo Owner", branch_ids: null, is_active: true }],
      meta: { page: 1, per_page: 25, total: 1 },
    },
    isLoading: false,
  }),
  useGet: () => ({ data: [], isLoading: false }),
  useWrite: () => ({ mutate: vi.fn(), isPending: false }),
}));

describe("Team", () => {
  it("renders users when the API returns null branch_ids", () => {
    const router = createMemoryRouter(
      [{ path: "/team", element: <Team />, errorElement: <ErrorBoundary /> }],
      { initialEntries: ["/team"] },
    );
    render(<RouterProvider router={router} />);
    expect(screen.queryByText("500")).not.toBeInTheDocument();
    expect(screen.getByText("Demo Owner")).toBeInTheDocument();
  });
});
