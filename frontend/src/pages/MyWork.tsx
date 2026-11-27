import { useState } from "react";
import { FileText } from "lucide-react";
import { Link } from "react-router-dom";
import { toast } from "sonner";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { DataGrid, PageHeader, Pager, StatusBadge } from "@/components/shared/kit";
import { EmptyState } from "@/components/shared/Display";
import { errorMessage, useGet, useGetPage } from "@/hooks/useApi";
import { useAuth } from "@/features/auth/auth";
import { ApiError, download } from "@/lib/api";
import { formatDate, formatMoney } from "@/lib/formatters";

const time = (v: string | null) =>
  v ? new Date(v).toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit" }) : "-";

interface Day {
  id: string;
  day: string;
  status: string;
  first_in: string | null;
  last_out: string | null;
  worked_minutes: number | null;
  late_minutes: number | null;
  overtime_minutes: number | null;
}
interface Slip {
  id: string;
  number: string;
  period_year?: number;
  period_month?: number;
  period_start?: string;
  period_end?: string;
  gross: string;
  total_deductions: string;
  net: string;
  run_status?: string;
}
interface MyLoan {
  id: string;
  kind: string;
  start_period?: string;
  principal: string;
  balance: string;
  monthly_deduction: string;
  status: string;
}
interface MyPayment {
  id: string;
  number: string;
  method?: string;
  reference?: string | null;
  amount: string;
  payment_date: string;
  status: string;
}

const hours = (m: number | null) => (m ? `${Math.floor(m / 60)}h ${m % 60}m` : "-");

function formatPeriod(s: Slip) {
  if (s.period_year && s.period_month) {
    return `${s.period_year}-${String(s.period_month).padStart(2, "0")}`;
  }
  if (s.period_start && s.period_end) {
    return `${formatDate(s.period_start)} – ${formatDate(s.period_end)}`;
  }
  return "-";
}

function Attendance() {
  const [page, setPage] = useState(1);
  const list = useGetPage<Day>("/me/attendance", { page });
  return (
    <div className="space-y-4">
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(d) => d.id}
        empty="No attendance recorded yet"
        columns={[
          { header: "Date", cell: (d) => formatDate(d.day) },
          {
            header: "In",
            cell: (d) => <span className="od-mono text-xs">{time(d.first_in)}</span>,
          },
          {
            header: "Out",
            cell: (d) => <span className="od-mono text-xs">{time(d.last_out)}</span>,
          },
          {
            header: "Worked",
            align: "right",
            cell: (d) => <span className="od-tabular">{hours(d.worked_minutes)}</span>,
          },
          {
            header: "Late",
            align: "right",
            cell: (d) =>
              d.late_minutes ? (
                <span className="od-tabular font-medium text-[var(--od-warning)]">
                  {d.late_minutes} min
                </span>
              ) : (
                <span className="text-[var(--od-ink-muted)]">-</span>
              ),
          },
          { header: "Status", cell: (d) => <StatusBadge value={d.status} /> },
        ]}
      />
      <Pager meta={list.data?.meta} onPage={setPage} />
    </div>
  );
}

function Payslips() {
  const [page, setPage] = useState(1);
  const list = useGetPage<Slip>("/me/payslips", { page });
  return (
    <div className="space-y-4">
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(s) => s.id}
        empty="No payslips yet"
        columns={[
          {
            header: "Payslip",
            cell: (s) => (
              <span className="od-mono font-medium text-[var(--od-ink)]">{s.number}</span>
            ),
          },
          { header: "Period", cell: (s) => <span className="od-mono">{formatPeriod(s)}</span> },
          {
            header: "Gross",
            align: "right",
            cell: (s) => <span className="od-tabular">{formatMoney(s.gross)}</span>,
          },
          {
            header: "Deductions",
            align: "right",
            cell: (s) => <span className="od-tabular">{formatMoney(s.total_deductions)}</span>,
          },
          {
            header: "Net",
            align: "right",
            cell: (s) => (
              <span className="od-tabular font-semibold text-[var(--od-ink)]">
                {formatMoney(s.net)}
              </span>
            ),
          },
          {
            header: "",
            cell: (s) => (
              <div className="flex justify-end">
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() =>
                    download(
                      `/payslips/${s.id}`,
                      { format: "pdf" },
                      `${s.number || s.id}.pdf`,
                    ).catch((e) => toast.error(errorMessage(e)))
                  }
                >
                  <FileText size={14} /> PDF
                </Button>
              </div>
            ),
          },
        ]}
      />
      <Pager meta={list.data?.meta} onPage={setPage} />
    </div>
  );
}

function Loans() {
  const list = useGet<MyLoan[]>("/me/loans");
  return (
    <DataGrid
      rows={list.data}
      loading={list.isLoading}
      rowKey={(l) => l.id}
      empty="No loans or advances"
      columns={[
        {
          header: "Type",
          cell: (l) => (
            <Badge variant="secondary" className="capitalize">
              {l.kind}
            </Badge>
          ),
        },
        {
          header: "Issued",
          cell: (l) => (l.start_period ? formatDate(l.start_period) : "-"),
        },
        {
          header: "Borrowed",
          align: "right",
          cell: (l) => <span className="od-tabular">{formatMoney(l.principal)}</span>,
        },
        {
          header: "Monthly",
          align: "right",
          cell: (l) => <span className="od-tabular">{formatMoney(l.monthly_deduction)}</span>,
        },
        {
          header: "Balance",
          align: "right",
          cell: (l) => (
            <span
              className={`od-tabular font-semibold ${
                Number(l.balance) > 0 ? "text-[var(--od-warning)]" : "text-[var(--od-ink)]"
              }`}
            >
              {formatMoney(l.balance)}
            </span>
          ),
        },
        { header: "Status", cell: (l) => <StatusBadge value={l.status} /> },
      ]}
    />
  );
}

function Payments() {
  const [page, setPage] = useState(1);
  const list = useGetPage<MyPayment>("/me/payments", { page });
  return (
    <div className="space-y-4">
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(p) => p.id}
        empty="No payments yet"
        columns={[
          {
            header: "Number",
            cell: (p) => (
              <span className="od-mono font-medium text-[var(--od-ink)]">{p.number}</span>
            ),
          },
          { header: "Date", cell: (p) => formatDate(p.payment_date) },
          {
            header: "Method",
            cell: (p) => (
              <span className="capitalize">
                {(p.method ?? "payment").replaceAll("_", " ")}
                {p.reference ? ` · ${p.reference}` : ""}
              </span>
            ),
          },
          {
            header: "Amount",
            align: "right",
            cell: (p) => (
              <span className="od-tabular font-semibold text-[var(--od-ink)]">
                {formatMoney(p.amount)}
              </span>
            ),
          },
          { header: "Status", cell: (p) => <StatusBadge value={p.status} /> },
        ]}
      />
      <Pager meta={list.data?.meta} onPage={setPage} />
    </div>
  );
}

/** Shown when the signed-in account has no employee record: what that blocks and who can fix it. */
function NotLinked() {
  const { can, user } = useAuth();
  const canLink = can("security.user.manage");
  const signIn = [user?.name, user?.phone ?? user?.email].filter(Boolean).join(", ");
  return (
    <EmptyState
      title="No employee profile linked"
      description="You can sign in, but this account is not connected to an employee record. Payslips, attendance, loans, leave requests and clock-in need that connection."
      action={
        canLink ? (
          <div className="max-w-md space-y-3 text-left text-[12px] leading-[1.5] text-[var(--od-ink-secondary)]">
            <p className="font-semibold text-[var(--od-ink)]">How to link it</p>
            <ol className="list-decimal space-y-1 pl-4">
              <li>If this person has no employee record yet, add one under People.</li>
              <li>
                In Team and roles, choose Edit next to the account, pick the record under “Link to
                employee profile” and save.
              </li>
            </ol>
            <p>An owner who does not need payslips or attendance can leave it unlinked.</p>
            <div className="flex flex-wrap justify-center gap-2">
              {can("hr.employee.manage") && (
                <Button asChild size="sm" variant="outline">
                  <Link to="/people?new=1">Add employee</Link>
                </Button>
              )}
              <Button asChild size="sm">
                <Link to="/team">Open Team and roles</Link>
              </Button>
            </div>
          </div>
        ) : (
          <p className="max-w-sm text-[12px] leading-[1.5] text-[var(--od-ink-secondary)]">
            Ask an Admin to link it. Tell them which account is yours:{" "}
            <span className="font-semibold text-[var(--od-ink)]">{signIn}</span>
          </p>
        )
      }
    />
  );
}

export default function MyWork() {
  const me = useGet<{ name: string; employee_no?: string }>("/me/employee");
  return (
    <div className="space-y-6">
      <PageHeader
        title="My work"
        description={
          me.data
            ? `${me.data.name}${me.data.employee_no ? ` · ${me.data.employee_no}` : ""}`
            : "Your attendance, payslips and loans."
        }
      />
      {me.error instanceof ApiError && me.error.code === "employee_required" ? (
        <NotLinked />
      ) : me.isError ? (
        <EmptyState title="Could not load your work" description={errorMessage(me.error)} />
      ) : (
        <Tabs defaultValue="payslips">
          <TabsList className="mb-4">
            <TabsTrigger value="payslips">Payslips</TabsTrigger>
            <TabsTrigger value="attendance">Attendance</TabsTrigger>
            <TabsTrigger value="loans">Loans</TabsTrigger>
            <TabsTrigger value="payments">Payments</TabsTrigger>
          </TabsList>
          <TabsContent value="payslips">
            <Payslips />
          </TabsContent>
          <TabsContent value="attendance">
            <Attendance />
          </TabsContent>
          <TabsContent value="loans">
            <Loans />
          </TabsContent>
          <TabsContent value="payments">
            <Payments />
          </TabsContent>
        </Tabs>
      )}
    </div>
  );
}
