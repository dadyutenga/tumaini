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
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";

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
  const { t } = useI18n();
  const [page, setPage] = useState(1);
  const list = useGetPage<Day>("/me/attendance", { page });
  return (
    <div className="space-y-4">
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(d) => d.id}
        empty={t.hr.noAttendanceRecordedYet}
        columns={[
          { header: t.common.date, cell: (d) => formatDate(d.day) },
          {
            header: t.hr.in,
            cell: (d) => <span className="od-mono text-xs">{time(d.first_in)}</span>,
          },
          {
            header: t.common.out,
            cell: (d) => <span className="od-mono text-xs">{time(d.last_out)}</span>,
          },
          {
            header: t.hr.worked,
            align: "right",
            cell: (d) => <span className="od-tabular">{hours(d.worked_minutes)}</span>,
          },
          {
            header: t.common.late,
            align: "right",
            cell: (d) =>
              d.late_minutes ? (
                <span className="od-tabular font-medium text-[var(--od-warning)]">
                  {fmt(t.hr.min, { late_minutes: d.late_minutes })}
                </span>
              ) : (
                <span className="text-[var(--od-ink-muted)]">-</span>
              ),
          },
          { header: t.common.status, cell: (d) => <StatusBadge value={d.status} /> },
        ]}
      />
      <Pager meta={list.data?.meta} onPage={setPage} />
    </div>
  );
}

function Payslips() {
  const { t } = useI18n();
  const [page, setPage] = useState(1);
  const list = useGetPage<Slip>("/me/payslips", { page });
  return (
    <div className="space-y-4">
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(s) => s.id}
        empty={t.hr.noPayslipsYet}
        columns={[
          {
            header: t.hr.payslip,
            cell: (s) => (
              <span className="od-mono font-medium text-[var(--od-ink)]">{s.number}</span>
            ),
          },
          {
            header: t.common.period,
            cell: (s) => <span className="od-mono">{formatPeriod(s)}</span>,
          },
          {
            header: t.common.gross,
            align: "right",
            cell: (s) => <span className="od-tabular">{formatMoney(s.gross)}</span>,
          },
          {
            header: t.common.deductions,
            align: "right",
            cell: (s) => <span className="od-tabular">{formatMoney(s.total_deductions)}</span>,
          },
          {
            header: t.common.net,
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
  const { t } = useI18n();
  const list = useGet<MyLoan[]>("/me/loans");
  return (
    <DataGrid
      rows={list.data}
      loading={list.isLoading}
      rowKey={(l) => l.id}
      empty={t.hr.noLoansOrAdvances}
      columns={[
        {
          header: t.common.type,
          cell: (l) => (
            <Badge variant="secondary" className="capitalize">
              {l.kind}
            </Badge>
          ),
        },
        {
          header: t.common.issued,
          cell: (l) => (l.start_period ? formatDate(l.start_period) : "-"),
        },
        {
          header: t.hr.borrowed,
          align: "right",
          cell: (l) => <span className="od-tabular">{formatMoney(l.principal)}</span>,
        },
        {
          header: t.common.monthly,
          align: "right",
          cell: (l) => <span className="od-tabular">{formatMoney(l.monthly_deduction)}</span>,
        },
        {
          header: t.common.balance,
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
        { header: t.common.status, cell: (l) => <StatusBadge value={l.status} /> },
      ]}
    />
  );
}

function Payments() {
  const { t } = useI18n();
  const [page, setPage] = useState(1);
  const list = useGetPage<MyPayment>("/me/payments", { page });
  return (
    <div className="space-y-4">
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(p) => p.id}
        empty={t.common.noPaymentsYet}
        columns={[
          {
            header: t.common.number,
            cell: (p) => (
              <span className="od-mono font-medium text-[var(--od-ink)]">{p.number}</span>
            ),
          },
          { header: t.common.date, cell: (p) => formatDate(p.payment_date) },
          {
            header: t.common.method,
            cell: (p) => (
              <span className="capitalize">
                {(p.method ?? "payment").replaceAll("_", " ")}
                {p.reference ? ` · ${p.reference}` : ""}
              </span>
            ),
          },
          {
            header: t.common.amount,
            align: "right",
            cell: (p) => (
              <span className="od-tabular font-semibold text-[var(--od-ink)]">
                {formatMoney(p.amount)}
              </span>
            ),
          },
          { header: t.common.status, cell: (p) => <StatusBadge value={p.status} /> },
        ]}
      />
      <Pager meta={list.data?.meta} onPage={setPage} />
    </div>
  );
}

/** Shown when the signed-in account has no employee record: what that blocks and who can fix it. */
function NotLinked() {
  const { t } = useI18n();
  const { can, user } = useAuth();
  const canLink = can("security.user.manage");
  const signIn = [user?.name, user?.phone ?? user?.email].filter(Boolean).join(", ");
  return (
    <EmptyState
      title={t.hr.noEmployeeProfileLinked}
      description={t.hr.youCanSignInBut}
      action={
        canLink ? (
          <div className="max-w-md space-y-3 text-left text-[12px] leading-[1.5] text-[var(--od-ink-secondary)]">
            <p className="font-semibold text-[var(--od-ink)]">{t.hr.howToLinkIt}</p>
            <ol className="list-decimal space-y-1 pl-4">
              <li>{t.hr.ifThisPersonHasNo}</li>
              <li>{t.hr.inTeamAndRolesChoose}</li>
            </ol>
            <p>{t.hr.anOwnerWhoDoesNot}</p>
            <div className="flex flex-wrap justify-center gap-2">
              {can("hr.employee.manage") && (
                <Button asChild size="sm" variant="outline">
                  <Link to="/people?new=1">{t.hr.addEmployee}</Link>
                </Button>
              )}
              <Button asChild size="sm">
                <Link to="/team">{t.hr.openTeamAndRoles}</Link>
              </Button>
            </div>
          </div>
        ) : (
          <p className="max-w-sm text-[12px] leading-[1.5] text-[var(--od-ink-secondary)]">
            {t.hr.askAnAdminToLink}{" "}
            <span className="font-semibold text-[var(--od-ink)]">{signIn}</span>
          </p>
        )
      }
    />
  );
}

export default function MyWork() {
  const { t } = useI18n();
  const me = useGet<{ name: string; employee_no?: string }>("/me/employee");
  return (
    <div className="space-y-6">
      <PageHeader
        title={t.common.myWork}
        description={
          me.data
            ? `${me.data.name}${me.data.employee_no ? ` · ${me.data.employee_no}` : ""}`
            : t.hr.yourAttendancePayslipsAndLoans
        }
      />
      {me.error instanceof ApiError && me.error.code === "employee_required" ? (
        <NotLinked />
      ) : me.isError ? (
        <EmptyState title={t.hr.couldNotLoadYourWork} description={errorMessage(me.error)} />
      ) : (
        <Tabs defaultValue="payslips">
          <TabsList className="mb-4">
            <TabsTrigger value="payslips">{t.hr.payslips}</TabsTrigger>
            <TabsTrigger value="attendance">{t.common.attendance}</TabsTrigger>
            <TabsTrigger value="loans">{t.hr.loans}</TabsTrigger>
            <TabsTrigger value="payments">{t.common.payments}</TabsTrigger>
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
