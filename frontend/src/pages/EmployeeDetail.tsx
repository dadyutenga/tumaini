import { useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import { Eye, KeyRound, Plus, Printer, Trash2, Upload, UserX } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  DataGrid,
  Field,
  FormDialog,
  MetaGrid,
  NativeSelect,
  PageHeader,
  SectionCard,
  StatusBadge,
} from "@/components/shared/kit";
import { PageLoader, StatCard } from "@/components/shared/Display";
import { FilePreviewDialog } from "@/components/shared/FilePreview";
import { RecordHistoryTab } from "@/components/shared/RecordHistoryTab";
import { AttachmentsSection } from "@/components/shared/AttachmentsSection";
import { useAuth } from "@/features/auth/auth";
import { errorMessage, useGet, useWrite } from "@/hooks/useApi";
import { session } from "@/lib/api";
import { formatDate, formatMoney, today } from "@/lib/formatters";
import { useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";

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
  const { t } = useI18n();
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
    success: t.hr.employeeUpdated,
    onSuccess: () => {
      setEditing(false);
      setUpdateComp(false);
    },
  });

  const createLogin = useWrite<typeof loginForm, unknown>("POST", `/employees/${id}/user`, {
    invalidate: [`/employees/${id}`, "/users"],
    success: t.hr.userLoginCreatedAndLinked,
    onSuccess: () => {
      setLoginOpen(false);
      setLoginForm((l) => ({ ...l, password: "" }));
    },
  });

  const terminate = useWrite<typeof termForm, unknown>("POST", `/employees/${id}/terminate`, {
    invalidate: [`/employees/${id}`, "/employees"],
    success: t.hr.employeeTerminated,
    onSuccess: () => setTermOpen(false),
  });

  const addAdj = useWrite<unknown, unknown>("POST", "/pay-adjustments", {
    invalidate: ["/pay-adjustments"],
    success: t.hr.bonusDeductionRecorded,
    onSuccess: () => {
      setAdjOpen(false);
      setAdjForm({ period: today(), kind: "bonus", amount: "", reason: "" });
    },
  });

  const saveAllowances = useWrite<unknown, unknown>("PUT", `/employees/${id}/components`, {
    invalidate: [`/employees/${id}/components`],
    success: t.hr.employeeAllowancesUpdated,
  });

  const deleteDoc = useWrite<string, unknown>(
    "DELETE",
    (docId) => `/employees/${id}/documents/${docId}`,
    {
      invalidate: [`/employees/${id}/documents`],
      success: t.hr.documentDeleted,
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
        throw new Error(j?.error?.message ?? t.hr.uploadFailed);
      }
      setDocName("");
      toast.success(t.hr.documentUploaded);
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
    <div className="space-y-6">
      <PageHeader
        backTo="/people"
        backLabel={t.common.people}
        title={`${e.name} (${e.employee_no})`}
        description={`${e.position || e.position_name || t.hr.noPosition}${e.branch_name ? ` · ${e.branch_name}` : ""}`}
        badge={<StatusBadge value={e.status} />}
        actions={
          <div className="flex flex-wrap items-center gap-2">
            {canViewStatement && (
              <Button variant="outline" size="sm" onClick={() => window.print()}>
                <Printer size={14} /> {t.common.printStatement}
              </Button>
            )}
            {can("security.user.manage") && !e.user_id && e.status !== "terminated" && (
              <Button variant="outline" size="sm" onClick={() => setLoginOpen(true)}>
                <KeyRound size={14} /> {t.hr.createLogin}
              </Button>
            )}
            {canManage && (
              <Button variant="outline" size="sm" onClick={() => setEditing(true)}>
                {t.hr.editProfile}
              </Button>
            )}
            {canManage && e.status !== "terminated" && (
              <Button
                variant="outline"
                size="sm"
                className="text-[var(--od-bad)] hover:text-[var(--od-bad)]"
                onClick={() => setTermOpen(true)}
              >
                <UserX size={14} /> {t.hr.terminate}
              </Button>
            )}
          </div>
        }
      />

      <Tabs defaultValue="profile">
        <TabsList className="mb-4 flex flex-wrap">
          <TabsTrigger value="profile">{t.hr.profileStatement}</TabsTrigger>
          {(canViewComp || canManageAdj) && (
            <TabsTrigger value="salary">{t.hr.allowancesBonuses}</TabsTrigger>
          )}
          <TabsTrigger value="leave">{t.hr.leaveAdvances}</TabsTrigger>
          <TabsTrigger value="documents">{t.hr.documentsAttachments}</TabsTrigger>
          <TabsTrigger value="history">{t.common.history}</TabsTrigger>
        </TabsList>

        <TabsContent value="profile" className="space-y-6">
          <div className="grid gap-4 md:grid-cols-2">
            <SectionCard title={t.hr.contactEmployment}>
              <MetaGrid
                items={[
                  { label: t.common.phone, value: e.phone || "-" },
                  { label: t.common.email, value: e.email || "-" },
                  { label: t.hr.position, value: e.position || e.position_name || "-" },
                  { label: t.hr.hireDate, value: e.hire_date ? formatDate(e.hire_date) : "-" },
                  {
                    label: t.hr.emergencyContact,
                    value:
                      [e.emergency_contact, e.emergency_contact_phone]
                        .filter(Boolean)
                        .join(" · ") || "-",
                  },
                  { label: t.hr.systemLogin, value: e.user_id ? t.hr.linked : t.common.notLinked },
                ]}
              />
            </SectionCard>

            {canViewComp && (
              <SectionCard title={t.hr.compensationPayoutDetails}>
                <MetaGrid
                  items={[
                    {
                      label: t.hr.monthlyBasicSalary,
                      value: (
                        <span className="od-tabular font-semibold text-[var(--od-ink)]">
                          {formatMoney(e.base_pay)}
                        </span>
                      ),
                    },
                    {
                      label: t.hr.payMethod,
                      value: (
                        <span className="capitalize">
                          {(e.pay_method ?? "cash").replaceAll("_", " ")}
                        </span>
                      ),
                    },
                    { label: t.hr.payoutProvider, value: e.pay_provider || "-" },
                    {
                      label: t.hr.payoutAccount,
                      value: e.pay_account_last4 ? (
                        <span className="od-mono">**** {e.pay_account_last4}</span>
                      ) : (
                        "-"
                      ),
                    },
                    ...(statement.data?.totals?.loan_balance
                      ? [
                          {
                            label: t.hr.advanceBalance,
                            value: (
                              <span className="od-tabular font-semibold text-[var(--od-warning)]">
                                {formatMoney(statement.data.totals.loan_balance)}
                              </span>
                            ),
                          },
                        ]
                      : []),
                  ]}
                />
              </SectionCard>
            )}
          </div>

          {canViewStatement && statement.data && (
            <div className="space-y-5">
              {statement.data.totals && (
                <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
                  <StatCard
                    label={t.common.gross}
                    value={formatMoney(statement.data.totals.gross)}
                  />
                  <StatCard
                    label={t.common.deductions}
                    value={formatMoney(statement.data.totals.deductions)}
                  />
                  <StatCard
                    label={t.hr.netPay}
                    value={formatMoney(statement.data.totals.net)}
                    tone="good"
                  />
                  <StatCard label={t.common.paid} value={formatMoney(statement.data.totals.paid)} />
                </div>
              )}

              <div className="space-y-2.5">
                <h3 className="od-section-title">{t.hr.payslips}</h3>
                <DataGrid
                  rows={statement.data.payslips ?? []}
                  rowKey={(p) => p.id}
                  empty={t.hr.noPayslipsInThisPeriod}
                  columns={[
                    {
                      header: t.hr.payslip,
                      cell: (p) => <span className="od-mono">{p.number || "-"}</span>,
                    },
                    {
                      header: t.common.period,
                      cell: (p) => (
                        <span className="od-mono">
                          {p.period_year}-{String(p.period_month).padStart(2, "0")}
                        </span>
                      ),
                    },
                    {
                      header: t.common.gross,
                      align: "right",
                      cell: (p) => <span className="od-tabular">{formatMoney(p.gross)}</span>,
                    },
                    {
                      header: t.common.deductions,
                      align: "right",
                      cell: (p) => (
                        <span className="od-tabular">{formatMoney(p.total_deductions)}</span>
                      ),
                    },
                    {
                      header: t.common.net,
                      align: "right",
                      cell: (p) => (
                        <span className="od-tabular font-semibold text-[var(--od-ink)]">
                          {formatMoney(p.net)}
                        </span>
                      ),
                    },
                    { header: t.common.status, cell: (p) => <StatusBadge value={p.run_status} /> },
                  ]}
                />
              </div>

              {(statement.data.payments?.length ?? 0) > 0 && (
                <div className="space-y-2.5">
                  <h3 className="od-section-title">{t.common.payments}</h3>
                  <DataGrid
                    rows={statement.data.payments ?? []}
                    rowKey={(p) => p.id}
                    columns={[
                      {
                        header: t.common.number,
                        cell: (p) => <span className="od-mono">{p.number}</span>,
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
                          <span className="od-tabular font-medium text-[var(--od-ink)]">
                            {formatMoney(p.amount)}
                          </span>
                        ),
                      },
                      { header: t.common.status, cell: (p) => <StatusBadge value={p.status} /> },
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
              <SectionCard
                title={t.hr.oneOffBonusOrDeduction}
                subtitle={t.hr.includedAutomaticallyInTheNext}
                actions={
                  <Button size="sm" onClick={() => setAdjOpen(true)}>
                    <Plus size={14} /> {t.hr.addBonusDeduction}
                  </Button>
                }
              >
                <p className="od-caption">{t.hr.recordAdHocPerformanceBonuses}</p>
              </SectionCard>
            )}

            {canViewComp && (
              <SectionCard
                title={t.hr.recurringAllowances}
                subtitle={t.hr.monthlyAllowancesPaidAlongsideBase}
                actions={
                  canManageComp ? (
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
                        <Plus size={14} /> {t.hr.addAllowance}
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
                        {t.hr.saveAllowances}
                      </Button>
                    </div>
                  ) : undefined
                }
              >
                {allowances.length === 0 ? (
                  <p className="text-sm text-[var(--od-ink-muted)]">
                    {t.hr.noActiveAllowancesConfigured}
                  </p>
                ) : (
                  <div className="space-y-2.5">
                    {allowances.map((a, idx) => (
                      <div
                        key={idx}
                        className="grid grid-cols-1 items-center gap-2 sm:grid-cols-[1fr_8rem_9rem_2.25rem]"
                      >
                        <Input
                          disabled={!canManageComp}
                          placeholder={t.hr.allowanceNameEGTransport}
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
                          className="od-tabular"
                          placeholder={t.common.amount}
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
                            className="h-8 w-8 text-[var(--od-bad)] hover:text-[var(--od-bad)]"
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
              </SectionCard>
            )}
          </TabsContent>
        )}

        <TabsContent value="leave" className="space-y-6">
          <div className="space-y-2.5">
            <h3 className="od-section-title">{t.hr.leaveBalanceThisYear}</h3>
            <DataGrid
              rows={leaveBal.data}
              loading={leaveBal.isLoading}
              rowKey={(l) => l.leave_type_id}
              empty={t.hr.noLeaveTypesConfigured}
              columns={[
                {
                  header: t.hr.leaveType,
                  cell: (l) => (
                    <span className="font-medium text-[var(--od-ink)]">
                      {l.name ?? l.leave_type}
                    </span>
                  ),
                },
                {
                  header: t.hr.entitled,
                  align: "right",
                  cell: (l) => <span className="od-tabular">{l.allowed ?? l.entitled ?? "0"}</span>,
                },
                {
                  header: t.hr.used,
                  align: "right",
                  cell: (l) => <span className="od-tabular">{l.taken ?? l.used ?? "0"}</span>,
                },
                {
                  header: t.hr.remaining,
                  align: "right",
                  cell: (l) => (
                    <span className="od-tabular font-semibold text-[var(--od-ink)]">
                      {l.remaining}
                    </span>
                  ),
                },
              ]}
            />
          </div>

          {canViewLoans && (
            <div className="space-y-2.5">
              <h3 className="od-section-title">{t.hr.advancesLoans}</h3>
              <DataGrid
                rows={loans.data}
                loading={loans.isLoading}
                rowKey={(l) => l.id}
                empty={t.hr.noLoansOrAdvancesFor}
                columns={[
                  {
                    header: t.common.type,
                    cell: (l) => (
                      <span className="font-medium capitalize text-[var(--od-ink)]">{l.kind}</span>
                    ),
                  },
                  {
                    header: t.common.issued,
                    cell: (l) => (l.start_period ? formatDate(l.start_period) : "-"),
                  },
                  {
                    header: t.common.amount,
                    align: "right",
                    cell: (l) => (
                      <span className="od-tabular">{formatMoney(l.principal ?? l.amount)}</span>
                    ),
                  },
                  {
                    header: t.common.balance,
                    align: "right",
                    cell: (l) => (
                      <span className="od-tabular font-semibold text-[var(--od-ink)]">
                        {formatMoney(l.balance)}
                      </span>
                    ),
                  },
                  { header: t.common.status, cell: (l) => <StatusBadge value={l.status} /> },
                ]}
              />
            </div>
          )}
        </TabsContent>

        <TabsContent value="documents" className="space-y-6">
          {canManage && (
            <SectionCard
              title={t.hr.officialHrDocuments}
              subtitle={t.hr.contractsIdCopiesCertificatesAnd}
              actions={
                <div className="flex flex-wrap items-center gap-2">
                  <NativeSelect
                    className="h-8 w-36 text-xs"
                    value={docKind}
                    onChange={(ev) => setDocKind(ev.target.value)}
                  >
                    <option value="contract">{t.hr.contract}</option>
                    <option value="id_copy">{t.hr.idCopy}</option>
                    <option value="certificate">{t.hr.certificate}</option>
                    <option value="other">{t.common.other}</option>
                  </NativeSelect>
                  <Input
                    className="h-8 w-40 text-xs"
                    placeholder={t.hr.documentTitle}
                    value={docName}
                    onChange={(ev) => setDocName(ev.target.value)}
                  />
                  <label className="inline-flex h-8 cursor-pointer items-center gap-1.5 rounded-lg border border-[var(--od-border-strong)] bg-[var(--od-surface)] px-3 text-xs font-medium text-[var(--od-ink)] transition hover:bg-[var(--od-surface-subtle)]">
                    <Upload size={13} />
                    <span>{uploadingDoc ? t.common.uploading : t.common.upload}</span>
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
              }
            >
              <DataGrid
                rows={docs.data}
                loading={docs.isLoading}
                rowKey={(d) => d.id}
                empty={t.hr.noOfficialDocumentsUploadedYet}
                columns={[
                  {
                    header: t.common.type,
                    cell: (d) => <span className="capitalize">{d.kind.replace("_", " ")}</span>,
                  },
                  {
                    header: t.common.name2,
                    cell: (d) => <span className="font-medium text-[var(--od-ink)]">{d.name}</span>,
                  },
                  { header: t.hr.uploaded, cell: (d) => formatDate(d.created_at) },
                  {
                    header: "",
                    cell: (d) => (
                      <div className="flex justify-end gap-1">
                        <Button
                          size="sm"
                          variant="ghost"
                          onClick={() => setPreview({ id: d.file_id, name: d.name })}
                        >
                          <Eye size={13} /> {t.common.preview}
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          className="text-[var(--od-bad)] hover:text-[var(--od-bad)]"
                          onClick={() => deleteDoc.mutate(d.id)}
                        >
                          <Trash2 size={13} />
                        </Button>
                      </div>
                    ),
                  },
                ]}
              />
            </SectionCard>
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
        title={t.hr.editEmployeeProfile}
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
        <Field label={t.hr.fullName}>
          <Input
            required
            value={form.name}
            onChange={(ev) => setForm((f) => ({ ...f, name: ev.target.value }))}
          />
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label={t.common.phone}>
            <Input
              value={form.phone}
              onChange={(ev) => setForm((f) => ({ ...f, phone: ev.target.value }))}
            />
          </Field>
          <Field label={t.common.email}>
            <Input
              type="email"
              value={form.email}
              onChange={(ev) => setForm((f) => ({ ...f, email: ev.target.value }))}
            />
          </Field>
          <Field label={t.hr.positionJobTitle}>
            <Input
              value={form.position}
              onChange={(ev) => setForm((f) => ({ ...f, position: ev.target.value }))}
            />
          </Field>
          <Field label={t.hr.emergencyContactName}>
            <Input
              value={form.emergency_contact}
              onChange={(ev) => setForm((f) => ({ ...f, emergency_contact: ev.target.value }))}
            />
          </Field>
          <Field label={t.hr.emergencyContactPhone} className="col-span-2">
            <Input
              value={form.emergency_contact_phone}
              onChange={(ev) =>
                setForm((f) => ({ ...f, emergency_contact_phone: ev.target.value }))
              }
            />
          </Field>
        </div>
        {canManageComp && (
          <div className="space-y-3 rounded-xl border border-[var(--od-border)] bg-[var(--od-surface-subtle)] p-3.5">
            <label className="flex cursor-pointer items-center gap-2 text-sm font-medium text-[var(--od-ink)]">
              <input
                type="checkbox"
                checked={updateComp}
                onChange={(ev) => setUpdateComp(ev.target.checked)}
              />
              {t.hr.recordACompensationPayoutChange}
            </label>
            {updateComp && (
              <div className="grid grid-cols-2 gap-3">
                <Field label={t.hr.monthlyBasicSalary}>
                  <Input
                    required
                    inputMode="decimal"
                    className="od-tabular"
                    value={form.base_pay}
                    onChange={(ev) => setForm((f) => ({ ...f, base_pay: ev.target.value }))}
                  />
                </Field>
                <Field label={t.hr.payMethod}>
                  <NativeSelect
                    value={form.pay_method}
                    onChange={(ev) => setForm((f) => ({ ...f, pay_method: ev.target.value }))}
                  >
                    <option value="cash">{t.common.cash}</option>
                    <option value="bank">{t.common.bank}</option>
                    <option value="mobile_money">{t.common.mobileMoney}</option>
                  </NativeSelect>
                </Field>
                {form.pay_method !== "cash" && (
                  <>
                    <Field label={t.hr.providerBankNetwork}>
                      <Input
                        value={form.pay_provider}
                        onChange={(ev) => setForm((f) => ({ ...f, pay_provider: ev.target.value }))}
                      />
                    </Field>
                    <Field label={t.hr.accountMobileNumber}>
                      <Input
                        required
                        value={form.pay_account}
                        onChange={(ev) => setForm((f) => ({ ...f, pay_account: ev.target.value }))}
                      />
                    </Field>
                  </>
                )}
                <Field label={t.common.effectiveFrom}>
                  <Input
                    required
                    type="date"
                    value={form.effective_from}
                    onChange={(ev) => setForm((f) => ({ ...f, effective_from: ev.target.value }))}
                  />
                </Field>
                <Field label={t.common.reason}>
                  <Input
                    placeholder={t.hr.eGAnnualSalaryReview}
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
        title={fmt(t.hr.createLoginFor, { name: e.name })}
        submitLabel={t.hr.createLogin}
        busy={createLogin.isPending}
        onSubmit={() => createLogin.mutate(loginForm)}
      >
        <div className="grid grid-cols-2 gap-3">
          <Field label={t.common.phone}>
            <Input
              required
              value={loginForm.phone}
              onChange={(ev) => setLoginForm((l) => ({ ...l, phone: ev.target.value }))}
            />
          </Field>
          <Field label={t.hr.emailOptional}>
            <Input
              type="email"
              value={loginForm.email}
              onChange={(ev) => setLoginForm((l) => ({ ...l, email: ev.target.value }))}
            />
          </Field>
        </div>
        <Field label={t.common.role}>
          <NativeSelect
            required
            value={loginForm.role_id}
            onChange={(ev) => setLoginForm((l) => ({ ...l, role_id: ev.target.value }))}
          >
            <option value="">{t.hr.chooseRole}</option>
            {roles.data?.map((r) => (
              <option key={r.id} value={r.id}>
                {r.name}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field label={t.hr.temporaryPassword}>
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
        title={fmt(t.hr.terminate2, { name: e.name })}
        submitLabel={t.hr.confirmTermination}
        busy={terminate.isPending}
        onSubmit={() => terminate.mutate(termForm)}
      >
        <Field label={t.hr.terminationDate}>
          <Input
            required
            type="date"
            value={termForm.date}
            onChange={(ev) => setTermForm((item) => ({ ...item, date: ev.target.value }))}
          />
        </Field>
        <Field label={t.common.reason}>
          <Input
            required
            value={termForm.reason}
            onChange={(ev) => setTermForm((item) => ({ ...item, reason: ev.target.value }))}
            placeholder={t.hr.resignationEndOfContractEtc}
          />
        </Field>
      </FormDialog>

      <FormDialog
        open={adjOpen}
        onOpenChange={setAdjOpen}
        title={t.hr.addBonusOrDeduction}
        busy={addAdj.isPending}
        onSubmit={() => addAdj.mutate({ ...adjForm, employee_id: id })}
      >
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
          <Field label={t.common.kind}>
            <NativeSelect
              value={adjForm.kind}
              onChange={(ev) => setAdjForm((a) => ({ ...a, kind: ev.target.value }))}
            >
              <option value="bonus">{t.common.bonus}</option>
              <option value="deduction">{t.common.deduction}</option>
            </NativeSelect>
          </Field>
          <Field label={t.common.amount}>
            <Input
              required
              inputMode="decimal"
              className="od-tabular"
              value={adjForm.amount}
              onChange={(ev) => setAdjForm((a) => ({ ...a, amount: ev.target.value }))}
            />
          </Field>
          <Field label={t.hr.periodDate}>
            <Input
              required
              type="date"
              value={adjForm.period}
              onChange={(ev) => setAdjForm((a) => ({ ...a, period: ev.target.value }))}
            />
          </Field>
        </div>
        <Field label={t.common.reason}>
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
    </div>
  );
}
