import { useState } from "react";
import { FileText } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { DataGrid, PageHeader, Pager, StatusBadge } from "@/components/shared/kit";
import { errorMessage, useGet, useGetPage } from "@/hooks/useApi";
import { download } from "@/lib/api";
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
  period_start: string;
  period_end: string;
  gross: string;
  total_deductions: string;
  net: string;
  status: string;
}
interface MyLoan {
  id: string;
  number: string;
  principal: string;
  balance: string;
  monthly_deduction: string;
  status: string;
}
interface MyPayment {
  id: string;
  number: string;
  kind: string;
  amount: string;
  payment_date: string;
  status: string;
}

const hours = (m: number | null) => (m ? `${Math.floor(m / 60)}h ${m % 60}m` : "-");

function Attendance() {
  const [page, setPage] = useState(1);
  const list = useGetPage<Day>("/me/attendance", { page });
  return (
    <>
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(d) => d.id}
        empty="No attendance recorded yet"
        columns={[
          { header: "Date", cell: (d) => formatDate(d.day) },
          { header: "In", cell: (d) => time(d.first_in) },
          { header: "Out", cell: (d) => time(d.last_out) },
          { header: "Worked", align: "right", cell: (d) => hours(d.worked_minutes) },
          {
            header: "Late",
            align: "right",
            cell: (d) => (d.late_minutes ? `${d.late_minutes} min` : "-"),
          },
          { header: "Status", cell: (d) => <StatusBadge value={d.status} /> },
        ]}
      />
      <Pager meta={list.data?.meta} onPage={setPage} />
    </>
  );
}

function Payslips() {
  const [page, setPage] = useState(1);
  const list = useGetPage<Slip>("/me/payslips", { page });
  return (
    <>
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(s) => s.id}
        empty="No payslips yet"
        columns={[
          { header: "Payslip", cell: (s) => <span className="font-medium">{s.number}</span> },
          {
            header: "Period",
            cell: (s) => `${formatDate(s.period_start)} – ${formatDate(s.period_end)}`,
          },
          { header: "Gross", align: "right", cell: (s) => formatMoney(s.gross) },
          { header: "Deductions", align: "right", cell: (s) => formatMoney(s.total_deductions) },
          { header: "Net", align: "right", cell: (s) => formatMoney(s.net) },
          {
            header: "",
            cell: (s) => (
              <Button
                size="sm"
                variant="ghost"
                onClick={() =>
                  download(`/payslips/${s.id}`, { format: "pdf" }, `${s.number}.pdf`).catch((e) =>
                    toast.error(errorMessage(e)),
                  )
                }
              >
                <FileText size={14} /> PDF
              </Button>
            ),
          },
        ]}
      />
      <Pager meta={list.data?.meta} onPage={setPage} />
    </>
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
        { header: "Number", cell: (l) => <span className="font-medium">{l.number}</span> },
        { header: "Borrowed", align: "right", cell: (l) => formatMoney(l.principal) },
        { header: "Monthly", align: "right", cell: (l) => formatMoney(l.monthly_deduction) },
        { header: "Balance", align: "right", cell: (l) => formatMoney(l.balance) },
        { header: "Status", cell: (l) => <StatusBadge value={l.status} /> },
      ]}
    />
  );
}

function Payments() {
  const [page, setPage] = useState(1);
  const list = useGetPage<MyPayment>("/me/payments", { page });
  return (
    <>
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(p) => p.id}
        empty="No payments yet"
        columns={[
          { header: "Number", cell: (p) => <span className="font-medium">{p.number}</span> },
          { header: "Date", cell: (p) => formatDate(p.payment_date) },
          { header: "Kind", cell: (p) => p.kind.replaceAll("_", " ") },
          { header: "Amount", align: "right", cell: (p) => formatMoney(p.amount) },
          { header: "Status", cell: (p) => <StatusBadge value={p.status} /> },
        ]}
      />
      <Pager meta={list.data?.meta} onPage={setPage} />
    </>
  );
}

export default function MyWork() {
  const me = useGet<{ name: string; employee_no?: string }>("/me/employee");
  return (
    <>
      <PageHeader
        title="My work"
        description={
          me.data
            ? `${me.data.name}${me.data.employee_no ? ` · ${me.data.employee_no}` : ""}`
            : "Your attendance, payslips and loans."
        }
      />
      {me.isError ? (
        <p className="text-sm text-muted-foreground">
          Your login is not linked to an employee record yet. Ask a manager to link it.
        </p>
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
    </>
  );
}
