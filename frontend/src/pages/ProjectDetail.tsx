import { useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { Download, Eye, MessageCircle, Plus, Trash2, Upload } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Progress } from "@/components/ui/progress";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import {
  DataGrid,
  Field,
  FormDialog,
  NativeSelect,
  PageHeader,
  SectionCard,
  StatusBadge,
} from "@/components/shared/kit";
import { PageLoader } from "@/components/shared/Display";
import { FilePreviewDialog } from "@/components/shared/FilePreview";
import { RecordHistoryTab } from "@/components/shared/RecordHistoryTab";
import { useAuth } from "@/features/auth/auth";
import { CostDialog } from "@/features/services/CostDialog";
import { PaymentDialog, ReasonDialog, StaffSelect, Tile } from "@/features/services/shared";
import { daysUntil, label, whatsappUrl } from "@/features/services/helpers";
import { errorMessage, useGet, useWrite } from "@/hooks/useApi";
import { ApiError, download, uploadFile } from "@/lib/api";
import { formatDate, formatDateTime, formatMoney, today, toNumber } from "@/lib/formatters";
import {
  JOB_TYPES,
  type ProjectDetail as Project,
  type ProjectNote,
  type Stage,
} from "@/types/services";
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";

const STAGE_STATUSES = ["not_started", "in_progress", "done", "stuck"] as const;

function StageDialog({
  projectId,
  stage,
  onClose,
}: {
  projectId: string;
  stage: Stage | null;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const [status, setStatus] = useState<string>(stage?.status ?? "in_progress");
  const [note, setNote] = useState("");
  const [owner, setOwner] = useState(stage?.owner_id ?? "");
  const [deadline, setDeadline] = useState(stage?.deadline ?? "");
  const [checklist, setChecklist] = useState(stage?.checklist ?? []);
  const [item, setItem] = useState("");
  const save = useWrite("PUT", `/projects/${projectId}/stages/${stage?.id}`, {
    invalidate: ["/projects", "/services", "/clients"],
    success: t.services.stageUpdated,
    onSuccess: onClose,
  });
  return (
    <FormDialog
      open={!!stage}
      onOpenChange={(o) => !o && onClose()}
      title={stage ? `${stage.position}. ${stage.name}` : t.services.stage}
      description={t.services.yourNameAndTheTime}
      busy={save.isPending}
      onSubmit={() =>
        save.mutate({
          status,
          note: note || undefined,
          owner_id: owner || undefined,
          deadline: deadline || undefined,
          checklist,
        })
      }
    >
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label={t.common.status}>
          <NativeSelect value={status} onChange={(e) => setStatus(e.target.value)}>
            {STAGE_STATUSES.map((s) => (
              <option key={s} value={s}>
                {label(s)}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field label={t.common.deadline}>
          <Input type="date" value={deadline} onChange={(e) => setDeadline(e.target.value)} />
        </Field>
        <Field label={t.common.owner} className="sm:col-span-2">
          <StaffSelect value={owner} onChange={setOwner} empty={t.services.keepCurrentOwner} />
        </Field>
      </div>
      <Field label={t.services.checklist}>
        <div className="space-y-2">
          {checklist.map((c, i) => (
            <div key={i} className="flex items-center gap-2">
              <input
                type="checkbox"
                aria-label={fmt(t.services.done, { text: c.text })}
                checked={c.done}
                onChange={(e) =>
                  setChecklist((list) =>
                    list.map((x, j) => (j === i ? { ...x, done: e.target.checked } : x)),
                  )
                }
              />
              <span
                className={`flex-1 text-[13px] ${c.done ? "text-[var(--od-ink-muted)] line-through" : ""}`}
              >
                {c.text}
              </span>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                aria-label={t.services.removeItem}
                onClick={() => setChecklist((list) => list.filter((_, j) => j !== i))}
              >
                <Trash2 size={14} />
              </Button>
            </div>
          ))}
          <div className="flex gap-2">
            <Input
              placeholder={t.services.addAStep}
              value={item}
              onChange={(e) => setItem(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && item.trim()) {
                  e.preventDefault();
                  setChecklist((list) => [...list, { text: item.trim(), done: false }]);
                  setItem("");
                }
              }}
            />
            <Button
              type="button"
              variant="outline"
              onClick={() => {
                if (!item.trim()) return;
                setChecklist((list) => [...list, { text: item.trim(), done: false }]);
                setItem("");
              }}
            >
              {t.common.add}
            </Button>
          </div>
        </div>
      </Field>
      <Field label={t.common.noteOptional}>
        <Textarea rows={2} value={note} onChange={(e) => setNote(e.target.value)} />
      </Field>
    </FormDialog>
  );
}

function DesignJobsDialog({
  projectId,
  stage,
  onClose,
}: {
  projectId: string;
  stage: Stage | null;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const [jobs, setJobs] = useState([{ job_type: "logo", title: "", deadline: "" }]);
  const create = useWrite("POST", `/projects/${projectId}/stages/${stage?.id}/design-jobs`, {
    invalidate: ["/projects", "/design-jobs"],
    success: t.services.designJobsCreated,
    onSuccess: () => {
      setJobs([{ job_type: "logo", title: "", deadline: "" }]);
      onClose();
    },
  });
  return (
    <FormDialog
      open={!!stage}
      onOpenChange={(o) => !o && onClose()}
      title={fmt(t.services.designJobsFor, { name: stage?.name ?? t.services.thisStage })}
      description={t.services.theStageIsMarkedDone}
      busy={create.isPending}
      onSubmit={() =>
        create.mutate({
          jobs: jobs.map((j) => ({ ...j, deadline: j.deadline || undefined })),
        })
      }
      wide
    >
      <div className="space-y-3">
        {jobs.map((j, i) => (
          <div key={i} className="grid gap-2 sm:grid-cols-[10rem_1fr_9rem_auto]">
            <NativeSelect
              aria-label={t.services.jobType}
              value={j.job_type}
              onChange={(e) =>
                setJobs((l) => l.map((x, k) => (k === i ? { ...x, job_type: e.target.value } : x)))
              }
            >
              {JOB_TYPES.map((item) => (
                <option key={item} value={item}>
                  {label(item)}
                </option>
              ))}
            </NativeSelect>
            <Input
              required
              aria-label={t.common.title}
              placeholder={t.services.titleForExampleLogo}
              value={j.title}
              onChange={(e) =>
                setJobs((l) => l.map((x, k) => (k === i ? { ...x, title: e.target.value } : x)))
              }
            />
            <Input
              type="date"
              aria-label={t.common.deadline}
              value={j.deadline}
              onChange={(e) =>
                setJobs((l) => l.map((x, k) => (k === i ? { ...x, deadline: e.target.value } : x)))
              }
            />
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label={t.services.removeJob}
              disabled={jobs.length === 1}
              onClick={() => setJobs((l) => l.filter((_, k) => k !== i))}
            >
              <Trash2 size={14} />
            </Button>
          </div>
        ))}
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() =>
            setJobs((l) => [...l, { job_type: "business_card", title: "", deadline: "" }])
          }
        >
          <Plus size={14} /> {t.services.anotherJob}
        </Button>
      </div>
    </FormDialog>
  );
}

export default function ProjectDetail() {
  const { t } = useI18n();
  const { id = "" } = useParams();
  const { can } = useAuth();
  const project = useGet<Project>(`/projects/${id}`);
  const notes = useGet<ProjectNote[]>(`/projects/${id}/notes`);
  const [stage, setStage] = useState<Stage | null>(null);
  const [jobsFor, setJobsFor] = useState<Stage | null>(null);
  const [paying, setPaying] = useState(false);
  const [costing, setCosting] = useState(false);
  const [messaging, setMessaging] = useState(false);
  const [editing, setEditing] = useState(false);
  const [addingStage, setAddingStage] = useState(false);
  const [voiding, setVoiding] = useState<string | null>(null);
  const [voidingCost, setVoidingCost] = useState<string | null>(null);
  const [message, setMessage] = useState("");
  const [msgChannel, setMsgChannel] = useState<"sms" | "whatsapp" | "email">("sms");
  const [msgUnavailable, setMsgUnavailable] = useState(false);
  const [edit, setEdit] = useState({ name: "", status: "in_progress", total_price: "", notes: "" });
  const [newStage, setNewStage] = useState({ name: "", deadline: "" });
  const [note, setNote] = useState({ kind: "call", body: "" });
  const fileInput = useRef<HTMLInputElement>(null);
  const [uploadFor, setUploadFor] = useState<string | null>(null);
  const [previewDoc, setPreviewDoc] = useState<{ id: string; name: string } | null>(null);

  const refresh = ["/projects", "/services", "/clients", "/client-documents", "/service-"];
  const update = useWrite("PUT", `/projects/${id}`, {
    invalidate: refresh,
    success: t.services.projectUpdated,
    onSuccess: () => setEditing(false),
  });
  const addStage = useWrite("POST", `/projects/${id}/stages`, {
    invalidate: refresh,
    success: t.services.stageAdded,
    onSuccess: () => {
      setAddingStage(false);
      setNewStage({ name: "", deadline: "" });
    },
  });
  const addNote = useWrite("POST", `/projects/${id}/notes`, {
    invalidate: ["/projects", "/clients"],
    success: t.services.noteAdded,
    onSuccess: () => setNote((n) => ({ ...n, body: "" })),
  });
  const sendUpdate = useWrite<
    { message: string; channel: string },
    { queued?: boolean; delivered?: boolean; has_phone?: boolean }
  >("POST", `/projects/${id}/updates`, {
    invalidate: ["/projects", "/clients"],
    onSuccess: () => {
      setMessaging(false);
      setMessage("");
      setMsgUnavailable(false);
      toast.success(t.services.updateQueuedForTheClient);
    },
    onError: (err) => {
      if (err instanceof ApiError && err.status === 503) {
        setMsgUnavailable(true);
      }
    },
  });
  const markDoc = useWrite<{ docId: string; status: string; file_id?: string }>(
    "PUT",
    (b) => `/client-documents/${b.docId}`,
    {
      invalidate: refresh,
      success: t.services.documentUpdated,
      toBody: ({ docId: _docId, ...rest }) => rest,
    },
  );
  const voidPayment = useWrite<{ id: string; reason: string }>(
    "POST",
    (b) => `/service-payments/${b.id}/void`,
    {
      invalidate: refresh,
      success: t.services.paymentVoided,
      toBody: (b) => ({ reason: b.reason }),
      onSuccess: () => setVoiding(null),
    },
  );
  const voidCost = useWrite<{ id: string; reason: string }>(
    "POST",
    (b) => `/service-costs/${b.id}/void`,
    {
      invalidate: refresh,
      success: t.services.costVoided,
      toBody: (b) => ({ reason: b.reason }),
      onSuccess: () => setVoidingCost(null),
    },
  );

  if (project.isLoading) return <PageLoader />;
  const p = project.data;
  if (!p)
    return (
      <div className="od-card items-center py-10 text-center">
        <p className="text-[14px] font-semibold text-[var(--od-bad)]">
          {t.services.projectNotFound}
        </p>
        <Button asChild variant="outline" size="sm" className="mt-3">
          <Link to="/projects">{t.services.backToProjects}</Link>
        </Button>
      </div>
    );
  const manage = can("services.project.manage");
  const billing = can("services.invoice.manage");
  const percent = p.stages_total ? (p.stages_done / p.stages_total) * 100 : 0;

  async function onFile(file: File | undefined) {
    const docId = uploadFor;
    setUploadFor(null);
    if (!file || !docId) return;
    try {
      const fileId = await uploadFile(file);
      markDoc.mutate({ docId, status: "received", file_id: fileId });
    } catch (error) {
      toast.error(errorMessage(error));
    }
  }

  return (
    <div className="space-y-6">
      <PageHeader
        backTo="/projects"
        backLabel={t.common.projects}
        title={p.name}
        badge={<StatusBadge value={p.status} />}
        description={fmt(t.common.client3, {
          number: p.number,
          client_name: p.client_name,
          value: p.owner_name ? ` ${fmt(t.services.owner, { owner_name: p.owner_name })}` : "",
        })}
        actions={
          <>
            {manage && (
              <Button
                size="sm"
                variant="outline"
                onClick={() => {
                  setEdit({
                    name: p.name,
                    status: p.status,
                    total_price: p.total_price,
                    notes: p.notes ?? "",
                  });
                  setEditing(true);
                }}
              >
                {t.common.edit}
              </Button>
            )}
            {billing && (
              <Button size="sm" onClick={() => setPaying(true)}>
                {t.common.addPayment}
              </Button>
            )}
            {billing && (
              <Button size="sm" variant="outline" onClick={() => setCosting(true)}>
                {t.services.addExpense}
              </Button>
            )}
            {manage && (
              <Button size="sm" variant="outline" onClick={() => setMessaging(true)}>
                {t.services.sendUpdateToClient}
              </Button>
            )}
          </>
        }
      />

      <SectionCard>
        <div className="flex items-center justify-between text-[13px]">
          <span className="font-semibold text-[var(--od-ink)]">
            {fmt(t.services.ofStagesDone, {
              stages_done: p.stages_done,
              stages_total: p.stages_total,
            })}
          </span>
          <span className="od-caption">
            {p.current_stage
              ? fmt(t.services.now, { current_stage: p.current_stage })
              : t.services.allStagesComplete}
          </span>
        </div>
        <Progress className="h-2" value={percent} />
      </SectionCard>

      <div className="od-card-flush overflow-x-auto">
        <table className="w-full text-[13px]">
          <thead>
            <tr className="border-b border-[var(--od-border-strong)] bg-[var(--od-surface-subtle)]/70 text-left text-[12px] text-[var(--od-ink-muted)]">
              <th className="px-4 py-2.5 font-medium">{t.services.stage}</th>
              <th className="px-4 py-2.5 font-medium">{t.common.status}</th>
              <th className="px-4 py-2.5 font-medium">{t.common.owner}</th>
              <th className="px-4 py-2.5 font-medium">{t.common.deadline}</th>
              <th className="px-4 py-2.5 font-medium">{t.common.documents}</th>
              <th className="px-4 py-2.5" />
            </tr>
          </thead>
          <tbody className="divide-y divide-[var(--od-border)]">
            {p.stages.map((s) => {
              const left = daysUntil(s.deadline);
              const late = left !== null && left < 0 && s.status !== "done";
              const done = s.checklist.filter((c) => c.done).length;
              return (
                <tr key={s.id} className="transition-colors hover:bg-[var(--od-surface-subtle)]">
                  <td className="px-4 py-3">
                    <p className="font-medium text-[var(--od-ink)]">
                      {s.position}. {s.name}
                    </p>
                    {s.checklist.length > 0 && (
                      <p className="od-caption od-tabular">
                        {fmt(t.services.checklist2, { done, count: s.checklist.length })}
                      </p>
                    )}
                    {s.note && <p className="od-caption">{s.note}</p>}
                  </td>
                  <td className="px-4 py-3">
                    <StatusBadge value={s.status} />
                  </td>
                  <td className="px-4 py-3 text-[var(--od-ink-secondary)]">
                    {s.owner_name ?? "—"}
                  </td>
                  <td
                    className={`px-4 py-3 ${late ? "font-semibold text-[var(--od-bad)]" : "text-[var(--od-ink-secondary)]"}`}
                  >
                    {s.deadline ? formatDate(s.deadline) : "—"}
                    {late && ` ${fmt(t.services.dLate, { value: -left! })}`}
                  </td>
                  <td className="px-4 py-3">
                    {s.documents_missing > 0 ? (
                      <span className="font-medium text-[var(--od-bad)]">
                        {fmt(t.services.missing2, { documents_missing: s.documents_missing })}
                      </span>
                    ) : (
                      <span className="text-[var(--od-ink-muted)]">—</span>
                    )}
                  </td>
                  <td className="space-x-1.5 px-4 py-3 text-right">
                    {can("services.design.manage") &&
                      s.status !== "done" &&
                      /brand|design|logo/i.test(s.name) && (
                        <Button size="sm" variant="outline" onClick={() => setJobsFor(s)}>
                          {t.common.designJobs}
                        </Button>
                      )}
                    {manage && (
                      <Button size="sm" variant="outline" onClick={() => setStage(s)}>
                        {t.services.update}
                      </Button>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
        {manage && (
          <div className="border-t border-[var(--od-border)] px-4 py-2.5">
            <Button size="sm" variant="ghost" onClick={() => setAddingStage(true)}>
              <Plus size={14} /> {t.services.addAStage}
            </Button>
          </div>
        )}
      </div>

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Tile label={t.common.total} value={formatMoney(p.total_price)} />
        <Tile label={t.common.paid} value={formatMoney(p.paid)} />
        <Tile
          label={t.common.balance}
          value={formatMoney(p.balance)}
          tone={toNumber(p.balance) > 0 ? "bad" : undefined}
        />
        <Tile
          label={fmt(t.common.costsProfit, { profit: formatMoney(p.profit) })}
          value={formatMoney(p.costs)}
        />
      </div>

      <Tabs defaultValue="overview">
        <TabsList>
          <TabsTrigger value="overview">{t.common.overview}</TabsTrigger>
          <TabsTrigger value="documents">{t.common.documents}</TabsTrigger>
          <TabsTrigger value="payments">{t.common.payments}</TabsTrigger>
          <TabsTrigger value="costs">{t.common.costs}</TabsTrigger>
          <TabsTrigger value="notes">{t.common.notes}</TabsTrigger>
          <TabsTrigger value="history">{t.common.history}</TabsTrigger>
        </TabsList>

        <TabsContent value="overview" className="space-y-6">
          {p.notes && (
            <SectionCard title={t.services.projectNotes}>
              <p className="text-[13px] leading-relaxed text-[var(--od-ink-secondary)]">
                {p.notes}
              </p>
            </SectionCard>
          )}
          <div className="space-y-2.5">
            <h2 className="od-section-title">{t.services.invoices}</h2>
            <DataGrid
              rows={p.invoices}
              rowKey={(i) => i.id}
              empty={t.services.noInvoicesForThisProject}
              columns={[
                {
                  header: t.common.invoice,
                  cell: (i) => (
                    <Link
                      className="od-mono font-medium underline underline-offset-4"
                      to={`/service-invoices?open=${i.id}`}
                    >
                      {i.number}
                    </Link>
                  ),
                },
                { header: t.common.status, cell: (i) => <StatusBadge value={i.status} /> },
                { header: t.common.total, align: "right", cell: (i) => formatMoney(i.total) },
                { header: t.common.balance, align: "right", cell: (i) => formatMoney(i.balance) },
              ]}
            />
          </div>
          <div className="space-y-2.5">
            <h2 className="od-section-title">{t.common.designJobs}</h2>
            <DataGrid
              rows={p.design_jobs}
              rowKey={(j) => j.id}
              empty={t.services.noDesignJobsLinkedTo}
              columns={[
                {
                  header: t.common.job,
                  cell: (j) => (
                    <Link
                      className="font-medium underline underline-offset-4"
                      to={`/design-jobs/${j.id}`}
                    >
                      {j.title}
                    </Link>
                  ),
                },
                { header: t.common.type, cell: (j) => label(j.job_type) },
                { header: t.common.designer, cell: (j) => j.designer_name ?? "—" },
                { header: t.common.status, cell: (j) => <StatusBadge value={j.status} /> },
              ]}
            />
          </div>
        </TabsContent>

        <TabsContent value="documents">
          <input
            ref={fileInput}
            type="file"
            className="hidden"
            onChange={(e) => {
              void onFile(e.target.files?.[0]);
              e.target.value = "";
            }}
          />
          <DataGrid
            rows={p.documents}
            rowKey={(d) => d.id}
            empty={t.services.thisServiceNeedsNoDocuments}
            columns={[
              {
                header: t.common.document,
                cell: (d) => <span className="font-medium">{d.name}</span>,
              },
              { header: t.common.status, cell: (d) => <StatusBadge value={d.status} /> },
              {
                header: t.common.received,
                cell: (d) => (d.received_at ? formatDate(d.received_at) : "—"),
              },
              {
                header: "",
                className: "text-right",
                cell: (d) => (
                  <div className="flex justify-end gap-1.5">
                    {d.file_id && (
                      <>
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => setPreviewDoc({ id: d.file_id!, name: d.name })}
                        >
                          <Eye size={13} /> {t.common.preview}
                        </Button>
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() =>
                            download(`/files/${d.file_id}`, {}, d.name).catch((e) =>
                              toast.error(errorMessage(e)),
                            )
                          }
                        >
                          <Download size={13} /> {t.common.download}
                        </Button>
                      </>
                    )}
                    {manage && (
                      <>
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => {
                            setUploadFor(d.id);
                            fileInput.current?.click();
                          }}
                        >
                          <Upload size={13} /> {t.common.upload}
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          onClick={() =>
                            markDoc.mutate({
                              docId: d.id,
                              status: d.status === "received" ? "missing" : "received",
                            })
                          }
                        >
                          {d.status === "received"
                            ? t.services.markMissing
                            : t.services.markReceived}
                        </Button>
                      </>
                    )}
                  </div>
                ),
              },
            ]}
          />
        </TabsContent>

        <TabsContent value="payments" className="space-y-3">
          <DataGrid
            rows={p.payments}
            rowKey={(x) => x.id}
            empty={t.services.noPaymentsYetAddThe}
            columns={[
              {
                header: t.common.receipt2,
                cell: (x) => <span className="od-mono font-medium">{x.number}</span>,
              },
              { header: t.common.date, cell: (x) => formatDate(x.date) },
              { header: t.common.method, cell: (x) => label(x.method) },
              { header: t.common.status, cell: (x) => <StatusBadge value={x.status} /> },
              {
                header: t.common.amount,
                align: "right",
                cell: (x) => <span className="font-medium">{formatMoney(x.amount)}</span>,
              },
              {
                header: "",
                className: "text-right",
                cell: (x) => (
                  <div className="flex justify-end gap-1.5">
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() =>
                        download(
                          `/service-payments/${x.id}/receipt`,
                          { format: "pdf" },
                          `receipt-${x.number}.pdf`,
                        ).catch((e) => toast.error(errorMessage(e)))
                      }
                    >
                      <Download size={13} /> {t.common.receiptPdf}
                    </Button>
                    {billing && x.status === "posted" && (
                      <Button
                        size="sm"
                        variant="ghost"
                        className="text-[var(--od-bad)] hover:text-[var(--od-bad)]"
                        onClick={() => setVoiding(x.id)}
                      >
                        {t.common.void}
                      </Button>
                    )}
                  </div>
                ),
              },
            ]}
          />
          <p className="od-caption">
            {t.services.balanceStillDue}{" "}
            <strong className="od-tabular text-[var(--od-ink)]">{formatMoney(p.balance)}</strong>
          </p>
        </TabsContent>

        <TabsContent value="costs" className="space-y-3">
          <DataGrid
            rows={p.costs_list}
            rowKey={(x) => x.id}
            empty={t.common.noCostsRecorded}
            columns={[
              { header: t.common.date, cell: (x) => formatDate(x.date) },
              { header: t.common.type, cell: (x) => label(x.kind) },
              { header: t.common.description, cell: (x) => x.description },
              { header: t.common.contractor, cell: (x) => x.contractor_name ?? "—" },
              { header: t.common.status, cell: (x) => <StatusBadge value={x.status} /> },
              {
                header: t.common.amount,
                align: "right",
                cell: (x) => <span className="font-medium">{formatMoney(x.amount)}</span>,
              },
              {
                header: "",
                className: "text-right",
                cell: (x) =>
                  billing && x.status !== "void" && toNumber(x.settled) === 0 ? (
                    <Button
                      size="sm"
                      variant="ghost"
                      className="text-[var(--od-bad)] hover:text-[var(--od-bad)]"
                      onClick={() => setVoidingCost(x.id)}
                    >
                      {t.common.void}
                    </Button>
                  ) : null,
              },
            ]}
          />
          <p className="od-caption">
            {t.services.profitPaymentsReceivedMinusCosts}{" "}
            <strong className="od-tabular text-[var(--od-ink)]">{formatMoney(p.profit)}</strong>
          </p>
        </TabsContent>

        <TabsContent value="notes" className="space-y-4">
          {manage && (
            <form
              className="flex flex-wrap items-start gap-2"
              onSubmit={(e) => {
                e.preventDefault();
                if (note.body.trim()) addNote.mutate({ ...note, noted_on: today() });
              }}
            >
              <NativeSelect
                aria-label={t.common.type}
                className="w-36"
                value={note.kind}
                onChange={(e) => setNote((n) => ({ ...n, kind: e.target.value }))}
              >
                <option value="call">{t.services.call}</option>
                <option value="message">{t.common.message}</option>
                <option value="meeting">{t.services.meeting}</option>
                <option value="note">{t.common.note}</option>
              </NativeSelect>
              <Input
                aria-label={t.common.note}
                className="min-w-60 flex-1"
                placeholder={t.services.whatWasSaidOrAgreed}
                value={note.body}
                onChange={(e) => setNote((n) => ({ ...n, body: e.target.value }))}
              />
              <Button type="submit" disabled={addNote.isPending}>
                {t.services.addNote}
              </Button>
            </form>
          )}
          <DataGrid
            rows={notes.data}
            loading={notes.isLoading}
            rowKey={(n) => n.id}
            empty={t.services.noCallsMessagesOrMeetings}
            columns={[
              { header: t.common.date, cell: (n) => formatDate(n.noted_on) },
              { header: t.common.type, cell: (n) => label(n.kind) },
              { header: t.common.by, cell: (n) => n.author ?? "—" },
              { header: t.common.note, cell: (n) => n.body, className: "whitespace-normal" },
            ]}
          />
        </TabsContent>

        <TabsContent value="history" className="space-y-6">
          <div className="space-y-2.5">
            <h2 className="od-section-title">{t.services.stageUpdates}</h2>
            <DataGrid
              rows={p.stage_events ?? []}
              rowKey={(e) => e.id}
              empty={t.services.noStageUpdatesYet}
              columns={[
                { header: t.common.when, cell: (e) => formatDateTime(e.created_at) },
                {
                  header: t.services.stage,
                  cell: (e) => <span className="font-medium">{e.stage_name}</span>,
                },
                {
                  header: t.common.change,
                  cell: (e) =>
                    `${e.from_status ? label(e.from_status) : t.common.created} → ${label(e.to_status)}`,
                },
                { header: t.common.by, cell: (e) => e.actor_name ?? "—" },
                {
                  header: t.common.note,
                  cell: (e) => e.note ?? "—",
                  className: "whitespace-normal",
                },
              ]}
            />
          </div>
          <SectionCard title={t.common.recordAuditLog}>
            <RecordHistoryTab entityType="svc_project" entityId={p.id} />
          </SectionCard>
        </TabsContent>
      </Tabs>

      <StageDialog
        key={stage?.id ?? "none"}
        projectId={id}
        stage={stage}
        onClose={() => setStage(null)}
      />
      <DesignJobsDialog
        key={`j-${jobsFor?.id ?? "none"}`}
        projectId={id}
        stage={jobsFor}
        onClose={() => setJobsFor(null)}
      />
      <PaymentDialog
        open={paying}
        onOpenChange={setPaying}
        title={t.common.addPayment}
        hint={t.services.paysIssuedInvoicesFirstAnything}
        path={`/projects/${id}/payments`}
        defaultAmount={toNumber(p.balance) > 0 ? p.balance : undefined}
        invalidate={refresh}
        success={t.services.paymentReceivedReceiptIssued}
      />
      <CostDialog
        open={costing}
        onOpenChange={setCosting}
        target={{ project_id: id }}
        invalidate={refresh}
      />
      <ReasonDialog
        open={!!voiding}
        onOpenChange={(o) => !o && setVoiding(null)}
        title={t.services.voidThisPayment}
        description={t.services.itsInvoiceAllocationsAndDeposit}
        submitLabel={t.common.voidPayment}
        busy={voidPayment.isPending}
        onSubmit={(reason) => voiding && voidPayment.mutate({ id: voiding, reason })}
      />
      <ReasonDialog
        open={!!voidingCost}
        onOpenChange={(o) => !o && setVoidingCost(null)}
        title={t.services.voidThisCost}
        submitLabel={t.services.voidCost}
        busy={voidCost.isPending}
        onSubmit={(reason) => voidingCost && voidCost.mutate({ id: voidingCost, reason })}
      />
      <FormDialog
        open={messaging}
        onOpenChange={(o) => {
          setMessaging(o);
          if (!o) setMsgUnavailable(false);
        }}
        title={t.services.sendUpdateToClient}
        description={t.services.queuedThroughTheBusinessOutbox}
        submitLabel={t.common.send}
        busy={sendUpdate.isPending}
        onSubmit={() => {
          setMsgUnavailable(false);
          sendUpdate.mutate({ message, channel: msgChannel });
        }}
      >
        <Field label={t.common.channel}>
          <NativeSelect
            value={msgChannel}
            onChange={(e) => {
              setMsgChannel(e.target.value as "sms" | "whatsapp" | "email");
              setMsgUnavailable(false);
            }}
          >
            <option value="sms">SMS</option>
            <option value="whatsapp">WhatsApp</option>
            <option value="email">{t.common.email}</option>
          </NativeSelect>
        </Field>
        <div className="flex flex-wrap gap-1.5">
          {[
            "Your TIN is ready.",
            "Your business name has been approved.",
            "We need one more document from you. Please bring it to the office.",
            "Your project is complete. You can collect your documents.",
          ].map((item) => (
            <Button
              key={item}
              type="button"
              size="sm"
              variant="outline"
              onClick={() => setMessage(item)}
            >
              {item.length > 28 ? `${item.slice(0, 28)}…` : item}
            </Button>
          ))}
        </div>
        <Field label={t.common.message}>
          <Textarea
            required
            rows={3}
            value={message}
            onChange={(e) => setMessage(e.target.value)}
          />
        </Field>
        {msgUnavailable && message.trim() && (
          <div className="flex items-center justify-between gap-2 rounded-[var(--od-radius-sm,8px)] bg-[var(--od-warning-soft)] p-3 text-xs">
            <span className="font-medium text-[var(--od-warning)]">
              {t.services.channelNotConfiguredSendManually}
            </span>
            <Button type="button" size="sm" variant="outline" asChild>
              <a
                href={whatsappUrl(undefined, `${p.name}: ${message}`)}
                target="_blank"
                rel="noreferrer"
              >
                <MessageCircle size={13} /> {t.common.openInWhatsapp}
              </a>
            </Button>
          </div>
        )}
      </FormDialog>
      <FormDialog
        open={editing}
        onOpenChange={setEditing}
        title={t.services.editProject}
        busy={update.isPending}
        onSubmit={() =>
          update.mutate({
            name: edit.name,
            status: edit.status,
            total_price: edit.total_price,
            notes: edit.notes,
          })
        }
      >
        <Field label={t.common.name2}>
          <Input
            required
            value={edit.name}
            onChange={(e) => setEdit({ ...edit, name: e.target.value })}
          />
        </Field>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label={t.common.status}>
            <NativeSelect
              value={edit.status}
              onChange={(e) => setEdit({ ...edit, status: e.target.value })}
            >
              <option value="in_progress">{t.services.inProgress}</option>
              <option value="on_hold">{t.services.onHold}</option>
              <option value="completed">{t.common.completed}</option>
              <option value="cancelled">{t.common.cancelled}</option>
            </NativeSelect>
          </Field>
          <Field label={t.services.totalPrice}>
            <Input
              inputMode="decimal"
              value={edit.total_price}
              onChange={(e) => setEdit({ ...edit, total_price: e.target.value })}
            />
          </Field>
        </div>
        <Field label={t.common.notes}>
          <Textarea
            rows={2}
            value={edit.notes}
            onChange={(e) => setEdit({ ...edit, notes: e.target.value })}
          />
        </Field>
      </FormDialog>
      <FormDialog
        open={addingStage}
        onOpenChange={setAddingStage}
        title={t.services.addAStage}
        busy={addStage.isPending}
        onSubmit={() =>
          addStage.mutate({ name: newStage.name, deadline: newStage.deadline || undefined })
        }
      >
        <Field label={t.services.stageName}>
          <Input
            required
            value={newStage.name}
            onChange={(e) => setNewStage({ ...newStage, name: e.target.value })}
          />
        </Field>
        <Field label={t.common.deadline}>
          <Input
            type="date"
            value={newStage.deadline}
            onChange={(e) => setNewStage({ ...newStage, deadline: e.target.value })}
          />
        </Field>
      </FormDialog>
      <FilePreviewDialog
        fileId={previewDoc?.id ?? null}
        fileName={previewDoc?.name}
        onClose={() => setPreviewDoc(null)}
      />
    </div>
  );
}
