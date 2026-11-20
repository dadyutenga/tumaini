import { useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { ArrowLeft, Eye, KeyRound, Plus, Printer, Trash2, Upload, UserX } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  DataGrid,
  Field,
  FormDialog,
  NativeSelect,
  PageHeader,
  StatusBadge,
} from "@/components/shared/kit";
import { PageLoader } from "@/components/shared/Display";
import { FilePreviewDialog } from "@/components/shared/FilePreview";
import { RecordHistoryTab } from "@/components/shared/RecordHistoryTab";
import { AttachmentsSection } from "@/components/shared/AttachmentsSection";
import { useAuth } from "@/features/auth/auth";
import { errorMessage, useGet, useWrite } from "@/hooks/useApi";
import { session } from "@/lib/api";
import { formatDate, formatMoney, today } from "@/lib/formatters";
import { useQueryClient } from "@tanstack/react-query";

interface EmployeeDetailData {
  id: string;
  employee_no: string;
  name: string;
  phone?: string | null;
  email?: string | null;
  position?: string | null;
  department_name?: string | null;
  position_name?: string | null;
  branch_name?: string | null;
  status: string;
  hire_date?: string | null;
  terminated_on?: string | null;
  base_pay?: string;
  pay_method?: "cash" | "bank" | "mobile_money" | string | null;
  pay_account_last4?: string | null;
  pay_provider?: string | null;
  emergency_contact?: string | null;
  emergency_contact_phone?: string | null;
  user_id?: string | null;
}

interface Option {
  id: string;
  name: string;
}

interface LeaveBal {
  leave_type_id: string;
  name?: string;
  leave_type?: string;
  allowed?: string;
  entitled?: string;
  taken?: string;
  used?: string;
  remaining: string;
}

interface EmpDoc {
  id: string;
  kind: string;
  name: string;
  file_id: string;
  mime_type: string;
  size_bytes: number;
  created_at: string;
}

interface EmpAllowance {
  id?: string;
  name: string;
  amount: string;
  effective_from?: string | null;
}

interface LoanRow {
  id: string;
  kind: string;
  start_period?: string;
  principal?: string;
  amount?: string;
  balance: string;
  status: string;
  created_at: string;
}

interface StatementPayslip {
  id: string;
  number: string;
  period_year: number;
  period_month: number;
  run_status: string;
  gross: string;
  total_deductions: string;
  net: string;
  paid_total: string;
}

interface StatementPayment {
  id: string;
  number: string;
  method?: string;
  amount: string;
  payment_date: string;
  status: string;
  reference?: string | null;
}

interface StatementRes {
  payslips?: StatementPayslip[];
  payments?: StatementPayment[];
  totals?: {
    gross: string;
    deductions: string;
    net: string;
    paid: string;
    unpaid: string;
    loan_balance: string;
  };
}

export default function EmployeeDetail() {
  const { id } = useParams();
  const { can, canAny } = useAuth();
  const qc = useQueryClient();

  const canManage = can("hr.employee.manage");
  const canViewComp = canAny("hr.compensation.view", "hr.compensation.manage");
  const canManageComp = can("hr.compensation.manage");
  const canManageAdj = canAny("hr.compensation.manage", "hr.payroll.manage");
  const canViewLoans = canAny("hr.compensation.view", "hr.compensation.manage", "hr.payroll.view");
  const canViewStatement = canAny("hr.compensation.view", "hr.payroll.view");

  const emp = useGet<EmployeeDetailData>(`/employees/${id}`, undefined, { enabled: Boolean(id) });
  const roles = useGet<Option[]>("/roles", undefined, { enabled: can("security.user.manage") });
  const leaveBal = useGet<LeaveBal[]>(`/employees/${id}/leave-balance`, undefined, {
    enabled: Boolean(id) && canAny("hr.employee.view", "hr.employee.manage", "hr.leave.approve"),
  });
  const docs = useGet<EmpDoc[]>(`/employees/${id}/documents`, undefined, {
    enabled: Boolean(id) && canManage,
  });
  const allowancesRes = useGet<EmpAllowance[]>(`/employees/${id}/components`, undefined, {
    enabled: Boolean(id) && canViewComp,
  });
  const loans = useGet<LoanRow[]>(
    "/loans",
    { employee_id: id, per_page: 50 },
    { enabled: Boolean(id) && canViewLoans },
  );
  const statement = useGet<StatementRes>(`/employees/${id}/statement`, undefined, {
    enabled: Boolean(id) && canViewStatement,
  });

  const [editing, setEditing] = useState(false);
  const [updateComp, setUpdateComp] = useState(false);
  const [form, setForm] = useState({
    name: "",
    phone: "",
    email: "",
    position: "",
    emergency_contact: "",
    emergency_contact_phone: "",
    base_pay: "",
    pay_method: "cash",
    pay_provider: "",
    pay_account: "",
    effective_from: today(),
    compensation_reason: "",
  });

  const [loginOpen, setLoginOpen] = useState(false);
  const [loginForm, setLoginForm] = useState({
    phone: "",
    email: "",
    password: "",
    role_id: "",
    language: "en",
  });

  const [termOpen, setTermOpen] = useState(false);
  const [termForm, setTermForm] = useState({ date: today(), reason: "" });

  const [adjOpen, setAdjOpen] = useState(false);
  const [adjForm, setAdjForm] = useState({
    period: today(),
    kind: "bonus",
    amount: "",
    reason: "",
  });

  const [docKind, setDocKind] = useState("contract");
  const [docName, setDocName] = useState("");
  const [uploadingDoc, setUploadingDoc] = useState(false);
  const [preview, setPreview] = useState<{ id: string; name: string } | null>(null);

  const [allowances, setAllowances] = useState<
    Array<{ name: string; amount: string; effective_from: string }>
  >([]);

  useEffect(() => {
    if (emp.data) {
      setForm({
        name: emp.data.name ?? "",
        phone: emp.data.phone ?? "",
        email: emp.data.email ?? "",
        position: emp.data.position ?? emp.data.position_name ?? "",
        emergency_contact: emp.data.emergency_contact ?? "",
        emergency_contact_phone: emp.data.emergency_contact_phone ?? "",
        base_pay: emp.data.base_pay ?? "",
        pay_method: emp.data.pay_method ?? "cash",
        pay_provider: emp.data.pay_provider ?? "",
        pay_account: "",
        effective_from: today(),
        compensation_reason: "",
      });
      setUpdateComp(false);
      setLoginForm((l) => ({
        ...l,
        phone: emp.data?.phone ?? "",
        email: emp.data?.email ?? "",
      }));
    }
  }, [emp.data]);

  useEffect(() => {
    if (allowancesRes.data) {
      setAllowances(
        allowancesRes.data.map((a) => ({
          name: a.name ?? "",
          amount: a.amount ?? "",
          effective_from: a.effective_from ? a.effective_from.slice(0, 10) : today(),
        })),
      );
    }
  }, [allowancesRes.data]);

  const updateEmp = useWrite<Record<string, unknown>, unknown>("PUT", `/employees/${id}`, {
    invalidate: [`/employees/${id}`, "/employees"],
    success: "Employee updated",
    onSuccess: () => {
      setEditing(false);
      setUpdateComp(false);
    },
  });

  const createLogin = useWrite<typeof loginForm, unknown>("POST", `/employees/${id}/user`, {
    invalidate: [`/employees/${id}`, "/users"],
    success: "User login created and linked",
    onSuccess: () => {
      setLoginOpen(false);
      setLoginForm((l) => ({ ...l, password: "" }));
    },
  });

  const terminate = useWrite<typeof termForm, unknown>("POST", `/employees/${id}/terminate`, {
    invalidate: [`/employees/${id}`, "/employees"],
    success: "Employee terminated",
    onSuccess: () => setTermOpen(false),
  });

  const addAdj = useWrite<unknown, unknown>("POST", "/pay-adjustments", {
    invalidate: ["/pay-adjustments"],
    success: "Bonus / deduction recorded",
    onSuccess: () => {
      setAdjOpen(false);
      setAdjForm({ period: today(), kind: "bonus", amount: "", reason: "" });
    },
  });

  const saveAllowances = useWrite<unknown, unknown>("PUT", `/employees/${id}/components`, {
    invalidate: [`/employees/${id}/components`],
    success: "Employee allowances updated",
  });

  const deleteDoc = useWrite<string, unknown>(
    "DELETE",
    (docId) => `/employees/${id}/documents/${docId}`,
    {
      invalidate: [`/employees/${id}/documents`],
      success: "Document deleted",
    },
  );

  async function uploadEmployeeDoc(file: File | undefined) {
    if (!file || !id) return;
    setUploadingDoc(true);
    try {
      const fd = new FormData();
      fd.append("file", file);
      fd.append("kind", docKind);
      fd.append("name", docName.trim() || file.name);
      const headers: Record<string, string> = {};
      const tok = session.getToken();
      if (tok) headers.Authorization = `Bearer ${tok}`;
      const res = await fetch(`/api/v1/employees/${id}/documents`, {
        method: "POST",
        headers,
        credentials: "include",
        body: fd,
      });
      if (!res.ok) {
        const j = (await res.json().catch(() => null)) as { error?: { message?: string } } | null;
        throw new Error(j?.error?.message ?? "Upload failed");
      }
      setDocName("");
      toast.success("Document uploaded");
      void qc.invalidateQueries({ queryKey: [`/employees/${id}/documents`] });
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setUploadingDoc(false);
    }
  }

  if (emp.isLoading || !emp.data) return <PageLoader />;
  const e = emp.data;

  return (
    <>
      <Link
        to="/people"
        className="mb-3 inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft size={14} /> People
      </Link>

      <PageHeader
        title={`${e.name} (${e.employee_no})`}
        description={`${e.position || e.position_name || "No position"}${e.branch_name ? ` · ${e.branch_name}` : ""}`}
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <StatusBadge value={e.status} />
            {canViewStatement && (
              <Button variant="outline" size="sm" onClick={() => window.print()}>
                <Printer size={14} /> Print statement
              </Button>
            )}
            {can("security.user.manage") && !e.user_id && e.status !== "terminated" && (
              <Button variant="outline" size="sm" onClick={() => setLoginOpen(true)}>
                <KeyRound size={14} /> Create login
              </Button>
            )}
            {canManage && (
              <Button variant="outline" size="sm" onClick={() => setEditing(true)}>
                Edit profile
              </Button>
            )}
            {canManage && e.status !== "terminated" && (
              <Button
                variant="outline"
                size="sm"
                className="text-destructive hover:text-destructive"
                onClick={() => setTermOpen(true)}
              >
                <UserX size={14} /> Terminate
              </Button>
            )}
          </div>
        }
      />

      <Tabs defaultValue="profile">
        <TabsList className="mb-4 flex flex-wrap">
          <TabsTrigger value="profile">Profile &amp; Statement</TabsTrigger>
          {(canViewComp || canManageAdj) && (
            <TabsTrigger value="salary">Allowances &amp; Bonuses</TabsTrigger>
          )}
          <TabsTrigger value="leave">Leave &amp; Advances</TabsTrigger>
          <TabsTrigger value="documents">Documents &amp; Attachments</TabsTrigger>
          <TabsTrigger value="history">History</TabsTrigger>
        </TabsList>

        <TabsContent value="profile" className="space-y-6">
          <div className="grid gap-4 md:grid-cols-2">
            <div className="space-y-2 rounded-2xl border bg-card p-5 text-sm">
              <h3 className="font-semibold">Contact &amp; Employment</h3>
              <div className="grid grid-cols-2 gap-y-2 pt-2">
                <span className="text-muted-foreground">Phone</span>
                <span>{e.phone || "-"}</span>
                <span className="text-muted-foreground">Email</span>
                <span>{e.email || "-"}</span>
                <span className="text-muted-foreground">Position</span>
                <span>{e.position || e.position_name || "-"}</span>
                <span className="text-muted-foreground">Hire date</span>
                <span>{e.hire_date ? formatDate(e.hire_date) : "-"}</span>
                <span className="text-muted-foreground">Emergency contact</span>
                <span>
                  {[e.emergency_contact, e.emergency_contact_phone].filter(Boolean).join(" · ") ||
                    "-"}
                </span>
                <span className="text-muted-foreground">System login</span>
                <span>{e.user_id ? "Linked" : "Not linked"}</span>
              </div>
            </div>

            {canViewComp && (
              <div className="space-y-2 rounded-2xl border bg-card p-5 text-sm">
                <h3 className="font-semibold">Compensation &amp; Payout Details</h3>
                <div className="grid grid-cols-2 gap-y-2 pt-2">
                  <span className="text-muted-foreground">Monthly basic salary</span>
                  <span className="font-semibold">{formatMoney(e.base_pay)}</span>
                  <span className="text-muted-foreground">Pay method</span>
                  <span className="capitalize">
                    {(e.pay_method ?? "cash").replaceAll("_", " ")}
                  </span>
                  <span className="text-muted-foreground">Payout provider</span>
                  <span>{e.pay_provider || "-"}</span>
                  <span className="text-muted-foreground">Payout account</span>
                  <span>{e.pay_account_last4 ? `**** ${e.pay_account_last4}` : "-"}</span>
                  {statement.data?.totals?.loan_balance && (
                    <>
                      <span className="text-muted-foreground">Advance balance</span>
                      <span className="font-semibold text-amber-700">
                        {formatMoney(statement.data.totals.loan_balance)}
                      </span>
                    </>
                  )}
                </div>
              </div>
            )}
          </div>

          {canViewStatement && statement.data && (
            <div className="space-y-4">
              {statement.data.totals && (
                <div className="grid gap-3 sm:grid-cols-4">
                  {(
                    [
                      ["Gross", statement.data.totals.gross],
                      ["Deductions", statement.data.totals.deductions],
                      ["Net pay", statement.data.totals.net],
                      ["Paid", statement.data.totals.paid],
                    ] as const
                  ).map(([label, val]) => (
                    <div key={label} className="rounded-xl border bg-card p-3">
                      <p className="text-xs text-muted-foreground">{label}</p>
                      <p className="mt-1 text-base font-semibold tabular-nums">
                        {formatMoney(val)}
                      </p>
                    </div>
                  ))}
                </div>
              )}

              <div className="space-y-2">
                <h3 className="font-semibold">Payslips</h3>
                <DataGrid
                  rows={statement.data.payslips ?? []}
                  rowKey={(p) => p.id}
                  empty="No payslips in this period"
                  columns={[
                    { header: "Payslip", cell: (p) => p.number || "-" },
                    {
                      header: "Period",
                      cell: (p) => `${p.period_year}-${String(p.period_month).padStart(2, "0")}`,
                    },
                    { header: "Gross", align: "right", cell: (p) => formatMoney(p.gross) },
                    {
                      header: "Deductions",
                      align: "right",
                      cell: (p) => formatMoney(p.total_deductions),
                    },
                    { header: "Net", align: "right", cell: (p) => formatMoney(p.net) },
                    { header: "Status", cell: (p) => <StatusBadge value={p.run_status} /> },
                  ]}
                />
              </div>

              {(statement.data.payments?.length ?? 0) > 0 && (
                <div className="space-y-2">
                  <h3 className="font-semibold">Payments</h3>
                  <DataGrid
                    rows={statement.data.payments ?? []}
                    rowKey={(p) => p.id}
                    columns={[
                      { header: "Number", cell: (p) => p.number },
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
                      { header: "Amount", align: "right", cell: (p) => formatMoney(p.amount) },
                      { header: "Status", cell: (p) => <StatusBadge value={p.status} /> },
                    ]}
                  />
                </div>
              )}
            </div>
          )}
        </TabsContent>

        {(canViewComp || canManageAdj) && (
          <TabsContent value="salary" className="space-y-6">
            {canManageAdj && (
              <div className="flex items-center justify-between rounded-2xl border bg-card p-4">
                <div>
                  <h3 className="font-semibold">One-off Bonus or Deduction</h3>
                  <p className="text-xs text-muted-foreground">
                    Included automatically in the next payroll calculation for that period.
                  </p>
                </div>
                <Button size="sm" onClick={() => setAdjOpen(true)}>
                  <Plus size={14} /> Add bonus / deduction
                </Button>
              </div>
            )}

            {canViewComp && (
              <div className="space-y-3 rounded-2xl border bg-card p-5">
                <div className="flex items-center justify-between gap-2">
                  <div>
                    <h3 className="font-semibold">Recurring Allowances</h3>
                    <p className="text-xs text-muted-foreground">
                      Monthly allowances paid alongside base salary (e.g. Housing, Transport).
                    </p>
                  </div>
                  {canManageComp && (
                    <div className="flex gap-2">
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() =>
                          setAllowances((prev) => [
                            ...prev,
                            { name: "", amount: "", effective_from: today() },
                          ])
                        }
                      >
                        <Plus size={14} /> Add allowance
                      </Button>
                      <Button
                        size="sm"
                        disabled={saveAllowances.isPending}
                        onClick={() => {
                          const payload = allowances
                            .filter((a) => a.name.trim() && Number(a.amount) > 0)
                            .map((a) => ({
                              name: a.name.trim(),
                              amount: a.amount.trim(),
                              effective_from: a.effective_from || today(),
                            }));
                          saveAllowances.mutate({ components: payload });
                        }}
                      >
                        Save allowances
                      </Button>
                    </div>
                  )}
                </div>
                {allowances.length === 0 ? (
                  <p className="text-sm text-muted-foreground">No active allowances configured.</p>
                ) : (
                  <div className="space-y-2">
                    {allowances.map((a, idx) => (
                      <div
                        key={idx}
                        className="grid grid-cols-[1fr_8rem_9rem_2rem] items-center gap-2"
                      >
                        <Input
                          disabled={!canManageComp}
                          placeholder="Allowance name (e.g. Transport)"
                          value={a.name}
                          onChange={(ev) =>
                            setAllowances((prev) =>
                              prev.map((row, i) =>
                                i === idx ? { ...row, name: ev.target.value } : row,
                              ),
                            )
                          }
                        />
                        <Input
                          disabled={!canManageComp}
                          inputMode="decimal"
                          placeholder="Amount"
                          value={a.amount}
                          onChange={(ev) =>
                            setAllowances((prev) =>
                              prev.map((row, i) =>
                                i === idx ? { ...row, amount: ev.target.value } : row,
                              ),
                            )
                          }
                        />
                        <Input
                          disabled={!canManageComp}
                          type="date"
                          value={a.effective_from}
                          onChange={(ev) =>
                            setAllowances((prev) =>
                              prev.map((row, i) =>
                                i === idx ? { ...row, effective_from: ev.target.value } : row,
                              ),
                            )
                          }
                        />
                        {canManageComp && (
                          <Button
                            type="button"
                            size="icon"
                            variant="ghost"
                            className="h-8 w-8 text-destructive"
                            onClick={() =>
                              setAllowances((prev) => prev.filter((_, i) => i !== idx))
                            }
                          >
                            <Trash2 size={14} />
                          </Button>
                        )}
                      </div>
                    ))}
                  </div>
                )}
              </div>
            )}
          </TabsContent>
        )}

        <TabsContent value="leave" className="space-y-6">
          <div className="space-y-2">
            <h3 className="font-semibold">Leave Balance (This Year)</h3>
            <DataGrid
              rows={leaveBal.data}
              loading={leaveBal.isLoading}
              rowKey={(l) => l.leave_type_id}
              empty="No leave types configured"
              columns={[
                {
                  header: "Leave type",
                  cell: (l) => <span className="font-medium">{l.name ?? l.leave_type}</span>,
                },
                { header: "Entitled", align: "right", cell: (l) => l.allowed ?? l.entitled ?? "0" },
                { header: "Used", align: "right", cell: (l) => l.taken ?? l.used ?? "0" },
                {
                  header: "Remaining",
                  align: "right",
                  cell: (l) => <span className="font-semibold">{l.remaining}</span>,
                },
              ]}
            />
          </div>

          {canViewLoans && (
            <div className="space-y-2">
              <h3 className="font-semibold">Advances &amp; Loans</h3>
              <DataGrid
                rows={loans.data}
                loading={loans.isLoading}
                rowKey={(l) => l.id}
                empty="No loans or advances for this employee"
                columns={[
                  {
                    header: "Type",
                    cell: (l) => <span className="font-medium capitalize">{l.kind}</span>,
                  },
                  {
                    header: "Issued",
                    cell: (l) => (l.start_period ? formatDate(l.start_period) : "-"),
                  },
                  {
                    header: "Amount",
                    align: "right",
                    cell: (l) => formatMoney(l.principal ?? l.amount),
                  },
                  { header: "Balance", align: "right", cell: (l) => formatMoney(l.balance) },
                  { header: "Status", cell: (l) => <StatusBadge value={l.status} /> },
                ]}
              />
            </div>
          )}
        </TabsContent>

        <TabsContent value="documents" className="space-y-6">
          {canManage && (
            <div className="space-y-3 rounded-2xl border bg-card p-4">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <h3 className="font-semibold">Official HR Documents</h3>
                <div className="flex flex-wrap items-center gap-2">
                  <NativeSelect
                    className="h-8 w-36 text-xs"
                    value={docKind}
                    onChange={(ev) => setDocKind(ev.target.value)}
                  >
                    <option value="contract">Contract</option>
                    <option value="id_copy">ID Copy</option>
                    <option value="certificate">Certificate</option>
                    <option value="other">Other</option>
                  </NativeSelect>
                  <Input
                    className="h-8 w-40 text-xs"
                    placeholder="Document title…"
                    value={docName}
                    onChange={(ev) => setDocName(ev.target.value)}
                  />
                  <label className="inline-flex h-8 cursor-pointer items-center gap-1.5 rounded-md border bg-background px-3 text-xs font-medium shadow-sm hover:bg-accent">
                    <Upload size={13} />
                    <span>{uploadingDoc ? "Uploading…" : "Upload"}</span>
                    <input
                      type="file"
                      className="hidden"
                      disabled={uploadingDoc}
                      onChange={(ev) => {
                        void uploadEmployeeDoc(ev.target.files?.[0]);
                        ev.target.value = "";
                      }}
                    />
                  </label>
                </div>
              </div>
              <DataGrid
                rows={docs.data}
                loading={docs.isLoading}
                rowKey={(d) => d.id}
                empty="No official documents uploaded yet"
                columns={[
                  {
                    header: "Type",
                    cell: (d) => <span className="capitalize">{d.kind.replace("_", " ")}</span>,
                  },
                  { header: "Name", cell: (d) => <span className="font-medium">{d.name}</span> },
                  { header: "Uploaded", cell: (d) => formatDate(d.created_at) },
                  {
                    header: "",
                    cell: (d) => (
                      <div className="flex justify-end gap-1">
                        <Button
                          size="sm"
                          variant="ghost"
                          onClick={() => setPreview({ id: d.file_id, name: d.name })}
                        >
                          <Eye size={13} /> Preview
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          className="text-destructive"
                          onClick={() => deleteDoc.mutate(d.id)}
                        >
                          <Trash2 size={13} />
                        </Button>
                      </div>
                    ),
                  },
                ]}
              />
            </div>
          )}

          {id && <AttachmentsSection entityType="employee" entityId={id} />}
        </TabsContent>

        <TabsContent value="history">
          {id && <RecordHistoryTab entityType="employee" entityId={id} />}
        </TabsContent>
      </Tabs>

      <FormDialog
        open={editing}
        onOpenChange={setEditing}
        title="Edit employee profile"
        busy={updateEmp.isPending}
        onSubmit={() => {
          const payload: Record<string, unknown> = {
            name: form.name,
            phone: form.phone,
            email: form.email,
            position: form.position,
            emergency_contact: form.emergency_contact,
            emergency_contact_phone: form.emergency_contact_phone,
          };
          if (canManageComp && updateComp && form.base_pay.trim() !== "") {
            payload.base_pay = form.base_pay.trim();
            payload.pay_method = form.pay_method;
            if (form.pay_method !== "cash") {
              payload.pay_provider = form.pay_provider;
              payload.pay_account = form.pay_account;
            }
            payload.effective_from = form.effective_from || today();
            if (form.compensation_reason.trim()) {
              payload.compensation_reason = form.compensation_reason.trim();
            }
          }
          updateEmp.mutate(payload);
        }}
      >
        <Field label="Full name">
          <Input
            required
            value={form.name}
            onChange={(ev) => setForm((f) => ({ ...f, name: ev.target.value }))}
          />
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label="Phone">
            <Input
              value={form.phone}
              onChange={(ev) => setForm((f) => ({ ...f, phone: ev.target.value }))}
            />
          </Field>
          <Field label="Email">
            <Input
              type="email"
              value={form.email}
              onChange={(ev) => setForm((f) => ({ ...f, email: ev.target.value }))}
            />
          </Field>
          <Field label="Position / job title">
            <Input
              value={form.position}
              onChange={(ev) => setForm((f) => ({ ...f, position: ev.target.value }))}
            />
          </Field>
          <Field label="Emergency contact name">
            <Input
              value={form.emergency_contact}
              onChange={(ev) => setForm((f) => ({ ...f, emergency_contact: ev.target.value }))}
            />
          </Field>
          <Field label="Emergency contact phone" className="col-span-2">
            <Input
              value={form.emergency_contact_phone}
              onChange={(ev) =>
                setForm((f) => ({ ...f, emergency_contact_phone: ev.target.value }))
              }
            />
          </Field>
        </div>
        {canManageComp && (
          <div className="space-y-3 rounded-xl border p-3">
            <label className="flex items-center gap-2 text-sm font-medium">
              <input
                type="checkbox"
                checked={updateComp}
                onChange={(ev) => setUpdateComp(ev.target.checked)}
              />
              Record a compensation / payout change
            </label>
            {updateComp && (
              <div className="grid grid-cols-2 gap-3">
                <Field label="Monthly basic salary">
                  <Input
                    required
                    inputMode="decimal"
                    value={form.base_pay}
                    onChange={(ev) => setForm((f) => ({ ...f, base_pay: ev.target.value }))}
                  />
                </Field>
                <Field label="Pay method">
                  <NativeSelect
                    value={form.pay_method}
                    onChange={(ev) => setForm((f) => ({ ...f, pay_method: ev.target.value }))}
                  >
                    <option value="cash">Cash</option>
                    <option value="bank">Bank</option>
                    <option value="mobile_money">Mobile money</option>
                  </NativeSelect>
                </Field>
                {form.pay_method !== "cash" && (
                  <>
                    <Field label="Provider (bank / network)">
                      <Input
                        value={form.pay_provider}
                        onChange={(ev) => setForm((f) => ({ ...f, pay_provider: ev.target.value }))}
                      />
                    </Field>
                    <Field label="Account / mobile number *">
                      <Input
                        required
                        value={form.pay_account}
                        onChange={(ev) => setForm((f) => ({ ...f, pay_account: ev.target.value }))}
                      />
                    </Field>
                  </>
                )}
                <Field label="Effective from">
                  <Input
                    required
                    type="date"
                    value={form.effective_from}
                    onChange={(ev) => setForm((f) => ({ ...f, effective_from: ev.target.value }))}
                  />
                </Field>
                <Field label="Reason">
                  <Input
                    placeholder="e.g. Annual salary review"
                    value={form.compensation_reason}
                    onChange={(ev) =>
                      setForm((f) => ({ ...f, compensation_reason: ev.target.value }))
                    }
                  />
                </Field>
              </div>
            )}
          </div>
        )}
      </FormDialog>

      <FormDialog
        open={loginOpen}
        onOpenChange={setLoginOpen}
        title={`Create login for ${e.name}`}
        submitLabel="Create login"
        busy={createLogin.isPending}
        onSubmit={() => createLogin.mutate(loginForm)}
      >
        <div className="grid grid-cols-2 gap-3">
          <Field label="Phone">
            <Input
              required
              value={loginForm.phone}
              onChange={(ev) => setLoginForm((l) => ({ ...l, phone: ev.target.value }))}
            />
          </Field>
          <Field label="Email (optional)">
            <Input
              type="email"
              value={loginForm.email}
              onChange={(ev) => setLoginForm((l) => ({ ...l, email: ev.target.value }))}
            />
          </Field>
        </div>
        <Field label="Role">
          <NativeSelect
            required
            value={loginForm.role_id}
            onChange={(ev) => setLoginForm((l) => ({ ...l, role_id: ev.target.value }))}
          >
            <option value="">Choose role…</option>
            {roles.data?.map((r) => (
              <option key={r.id} value={r.id}>
                {r.name}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field label="Temporary password">
          <Input
            required
            type="password"
            value={loginForm.password}
            onChange={(ev) => setLoginForm((l) => ({ ...l, password: ev.target.value }))}
          />
        </Field>
      </FormDialog>

      <FormDialog
        open={termOpen}
        onOpenChange={setTermOpen}
        title={`Terminate ${e.name}`}
        submitLabel="Confirm termination"
        busy={terminate.isPending}
        onSubmit={() => terminate.mutate(termForm)}
      >
        <Field label="Termination date">
          <Input
            required
            type="date"
            value={termForm.date}
            onChange={(ev) => setTermForm((t) => ({ ...t, date: ev.target.value }))}
          />
        </Field>
        <Field label="Reason">
          <Input
            required
            value={termForm.reason}
            onChange={(ev) => setTermForm((t) => ({ ...t, reason: ev.target.value }))}
            placeholder="Resignation, end of contract, etc."
          />
        </Field>
      </FormDialog>

      <FormDialog
        open={adjOpen}
        onOpenChange={setAdjOpen}
        title="Add bonus or deduction"
        busy={addAdj.isPending}
        onSubmit={() => addAdj.mutate({ ...adjForm, employee_id: id })}
      >
        <div className="grid grid-cols-3 gap-3">
          <Field label="Kind">
            <NativeSelect
              value={adjForm.kind}
              onChange={(ev) => setAdjForm((a) => ({ ...a, kind: ev.target.value }))}
            >
              <option value="bonus">Bonus</option>
              <option value="deduction">Deduction</option>
            </NativeSelect>
          </Field>
          <Field label="Amount">
            <Input
              required
              inputMode="decimal"
              value={adjForm.amount}
              onChange={(ev) => setAdjForm((a) => ({ ...a, amount: ev.target.value }))}
            />
          </Field>
          <Field label="Period date">
            <Input
              required
              type="date"
              value={adjForm.period}
              onChange={(ev) => setAdjForm((a) => ({ ...a, period: ev.target.value }))}
            />
          </Field>
        </div>
        <Field label="Reason">
          <Input
            required
            value={adjForm.reason}
            onChange={(ev) => setAdjForm((a) => ({ ...a, reason: ev.target.value }))}
          />
        </Field>
      </FormDialog>

      <FilePreviewDialog
        fileId={preview?.id ?? null}
        fileName={preview?.name}
        onClose={() => setPreview(null)}
      />
    </>
  );
}
