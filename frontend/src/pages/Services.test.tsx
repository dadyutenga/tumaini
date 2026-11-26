import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import ClientApproval from "./ClientApproval";
import DesignJobs from "./DesignJobs";
import ProjectDetail from "./ProjectDetail";
import ServicesHome from "./ServicesHome";

vi.mock("@/features/auth/auth", () => ({
  useAuth: () => ({ can: () => true, canAny: () => true, limit: () => null }),
}));

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

/** Routes a path to a fixture, shaped like the real API responses. */
function serve(routes: Record<string, { body: unknown; status?: number }>) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      const path = new URL(url, "http://x").pathname.replace("/api/v1", "");
      const hit = routes[path];
      return hit ? json(hit.body, hit.status) : json({ data: [] });
    }),
  );
}

function mount(path: string, route: string, element: React.ReactNode) {
  render(
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path={route} element={element} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

afterEach(() => vi.unstubAllGlobals());

const stage = (position: number, name: string, status: string) => ({
  id: `s${position}`,
  position,
  name,
  status,
  owner_id: null,
  owner_name: "Demo Owner",
  deadline: "2026-12-10",
  note: null,
  checklist: [],
  documents_missing: position === 1 ? 1 : 0,
  est_days: 2,
  completed_at: null,
});

describe("Services screens", () => {
  it("shows a project with its stages, money and documents", async () => {
    serve({
      "/projects/p1": {
        body: {
          data: {
            id: "p1",
            number: "MAIN-PRJ-000001",
            name: "Business registration - Mama Amina Shop",
            client_id: "c1",
            client_name: "Mama Amina Shop",
            owner_name: "Demo Owner",
            status: "in_progress",
            current_stage: "TIN",
            stages_done: 2,
            stages_total: 4,
            total_price: "300000.00",
            paid: "100000.00",
            balance: "200000.00",
            costs: "20000.00",
            profit: "80000.00",
            start_date: "2026-11-19",
            notes: null,
            stages: [stage(1, "Client intake", "done"), stage(3, "TIN", "in_progress")],
            documents: [
              {
                id: "d1",
                name: "ID copy",
                doc_type: "required",
                status: "missing",
                file_id: null,
                received_at: null,
              },
            ],
            payments: [],
            costs_list: [],
            notes_list: [],
            invoices: [],
            design_jobs: [],
          },
        },
      },
      "/projects/p1/notes": { body: { data: [] } },
    });
    mount("/projects/p1", "/projects/:id", <ProjectDetail />);
    expect(await screen.findByText("Business registration - Mama Amina Shop")).toBeInTheDocument();
    expect(screen.getByText("2 of 4 stages done")).toBeInTheDocument();
    expect(screen.getByText("3. TIN")).toBeInTheDocument();
    expect(screen.getByText("1 missing")).toBeInTheDocument();
    expect(screen.getByText("TZS 200,000.00")).toBeInTheDocument();
  });

  it("shows every design step on the board", async () => {
    const columns = [
      "new_brief",
      "in_design",
      "internal_review",
      "with_client",
      "approved",
      "printing",
      "delivered",
    ].map((status) => ({ status, jobs: [] as unknown[] }));
    columns[0].jobs.push({
      id: "j1",
      title: "Logo for Mama Amina",
      client_name: "Mama Amina Shop",
      status: "new_brief",
      priority: "normal",
      designer_name: null,
      deadline: null,
      overdue: false,
      revisions_used: 0,
      revisions_included: 3,
      revisions_over: false,
      printing_needed: false,
    });
    serve({ "/design-jobs/board": { body: { data: columns } } });
    mount("/design-jobs", "/design-jobs", <DesignJobs />);
    expect(await screen.findByText("Logo for Mama Amina")).toBeInTheDocument();
    expect(screen.getByText("With client")).toBeInTheDocument();
    expect(screen.getByText("Delivered")).toBeInTheDocument();
    expect(screen.getByText("Rev 0/3")).toBeInTheDocument();
  });

  it("shows only the dashboard parts the API sent", async () => {
    serve({
      "/services/dashboard": {
        body: {
          data: {
            late_projects: 2,
            projects_by_stage: [{ stage: "TIN", projects: 3 }],
            renewals_due: [],
          },
        },
      },
    });
    mount("/services", "/services", <ServicesHome />);
    expect(await screen.findByText("Late projects")).toBeInTheDocument();
    expect(screen.getByText("Projects by stage")).toBeInTheDocument();
    expect(screen.queryByText("Clients owe us")).not.toBeInTheDocument();
  });

  it("lets a client approve from the private link", async () => {
    serve({
      "/public/design-approvals/tok": {
        body: {
          data: {
            title: "Logo",
            job_type: "logo",
            client_name: "Mama Amina Shop",
            business_name: "Demo Duka",
            revisions_included: 3,
            revisions_used: 0,
            version_no: 2,
            status: "sent",
            expires_at: "2026-12-03T00:00:00Z",
            decided_at: null,
            has_file: true,
            mime_type: "image/png",
            link: null,
            comments: [],
          },
        },
      },
    });
    mount("/approve/tok", "/approve/:token", <ClientApproval />);
    expect(await screen.findByRole("button", { name: "Approve" })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: /Logo, version 2/ })).toBeInTheDocument();
    // Changes need a comment first.
    expect(screen.getByRole("button", { name: "Request changes" })).toBeDisabled();
  });

  it("tells the client when the link has expired", async () => {
    serve({
      "/public/design-approvals/old": {
        status: 410,
        body: { error: { code: "link_expired", message: "This approval link has expired" } },
      },
    });
    mount("/approve/old", "/approve/:token", <ClientApproval />);
    expect(await screen.findByText("This link has expired")).toBeInTheDocument();
  });
});
