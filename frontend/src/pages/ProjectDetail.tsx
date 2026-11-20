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
import { download, uploadFile } from "@/lib/api";
import { formatDate, formatDateTime, formatMoney, today, toNumber } from "@/lib/formatters";
import {
  JOB_TYPES,
  type ProjectDetail as Project,
  type ProjectNote,
  type Stage,
} from "@/types/services";

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
  const [status, setStatus] = useState<string>(stage?.status ?? "in_progress");
  const [note, setNote] = useState("");
  const [owner, setOwner] = useState(stage?.owner_id ?? "");
  const [deadline, setDeadline] = useState(stage?.deadline ?? "");
  const [checklist, setChecklist] = useState(stage?.checklist ?? []);
  const [item, setItem] = useState("");
  const save = useWrite("PUT", `/projects/${projectId}/stages/${stage?.id}`, {
    invalidate: ["/projects", "/services", "/clients"],
    success: "Stage updated",
    onSuccess: onClose,
  });
  return (
    <FormDialog
      open={!!stage}
      onOpenChange={(o) => !o && onClose()}
      title={stage ? `${stage.position}. ${stage.name}` : "Stage"}
      description="Your name and the time are recorded with the change."
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
        <Field label="Status">
          <NativeSelect value={status} onChange={(e) => setStatus(e.target.value)}>
            {STAGE_STATUSES.map((s) => (
              <option key={s} value={s}>
                {label(s)}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field label="Deadline">
          <Input type="date" value={deadline} onChange={(e) => setDeadline(e.target.value)} />
        </Field>
        <Field label="Owner" className="sm:col-span-2">
          <StaffSelect value={owner} onChange={setOwner} empty="Keep current owner" />
        </Field>
      </div>
      <Field label="Checklist">
        <div className="space-y-2">
          {checklist.map((c, i) => (
            <div key={i} className="flex items-center gap-2">
              <input
                type="checkbox"
                aria-label={`Done: ${c.text}`}
                checked={c.done}
                onChange={(e) =>
                  setChecklist((list) =>
                    list.map((x, j) => (j === i ? { ...x, done: e.target.checked } : x)),
                  )
                }
              />
              <span
                className={`flex-1 text-sm ${c.done ? "text-muted-foreground line-through" : ""}`}
              >
                {c.text}
              </span>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                aria-label="Remove item"
                onClick={() => setChecklist((list) => list.filter((_, j) => j !== i))}
              >
                <Trash2 size={14} />
              </Button>
            </div>
          ))}
          <div className="flex gap-2">
            <Input
              placeholder="Add a step…"
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
              Add
            </Button>
          </div>
        </div>
      </Field>
      <Field label="Note (optional)">
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
  const [jobs, setJobs] = useState([{ job_type: "logo", title: "", deadline: "" }]);
  const create = useWrite("POST", `/projects/${projectId}/stages/${stage?.id}/design-jobs`, {
    invalidate: ["/projects", "/design-jobs"],
    success: "Design jobs created",
    onSuccess: () => {
      setJobs([{ job_type: "logo", title: "", deadline: "" }]);
      onClose();
    },
  });
  return (
    <FormDialog
      open={!!stage}
      onOpenChange={(o) => !o && onClose()}
      title={`Design jobs for ${stage?.name ?? "this stage"}`}
      description="The stage is marked done when every job here is approved or delivered."
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
              aria-label="Job type"
              value={j.job_type}
              onChange={(e) =>
                setJobs((l) => l.map((x, k) => (k === i ? { ...x, job_type: e.target.value } : x)))
              }
            >
              {JOB_TYPES.map((t) => (
                <option key={t} value={t}>
                  {label(t)}
                </option>
              ))}
            </NativeSelect>
            <Input
              required
              aria-label="Title"
              placeholder="Title, for example Logo"
              value={j.title}
              onChange={(e) =>
                setJobs((l) => l.map((x, k) => (k === i ? { ...x, title: e.target.value } : x)))
              }
            />
            <Input
              type="date"
              aria-label="Deadline"
              value={j.deadline}
              onChange={(e) =>
                setJobs((l) => l.map((x, k) => (k === i ? { ...x, deadline: e.target.value } : x)))
              }
            />
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label="Remove job"
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
          onClick={() =>
            setJobs((l) => [...l, { job_type: "business_card", title: "", deadline: "" }])
          }
        >
          <Plus size={14} /> Another job
        </Button>
      </div>
    </FormDialog>
  );
}

export default function ProjectDetail() {
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
  const [edit, setEdit] = useState({ name: "", status: "in_progress", total_price: "", notes: "" });
  const [newStage, setNewStage] = useState({ name: "", deadline: "" });
  const [note, setNote] = useState({ kind: "call", body: "" });
  const fileInput = useRef<HTMLInputElement>(null);
  const [uploadFor, setUploadFor] = useState<string | null>(null);
  const [previewDoc, setPreviewDoc] = useState<{ id: string; name: string } | null>(null);

  const refresh = ["/projects", "/services", "/clients", "/client-documents", "/service-"];
  const update = useWrite("PUT", `/projects/${id}`, {
    invalidate: refresh,
    success: "Project updated",
    onSuccess: () => setEditing(false),
  });
  const addStage = useWrite("POST", `/projects/${id}/stages`, {
    invalidate: refresh,
    success: "Stage added",
    onSuccess: () => {
      setAddingStage(false);
      setNewStage({ name: "", deadline: "" });
    },
  });
  const addNote = useWrite("POST", `/projects/${id}/notes`, {
    invalidate: ["/projects", "/clients"],
    success: "Note added",
    onSuccess: () => setNote((n) => ({ ...n, body: "" })),
  });
  const sendUpdate = useWrite<{ message: string }, { delivered: boolean; has_phone: boolean }>(
    "POST",
    `/projects/${id}/updates`,
    {
      invalidate: ["/projects", "/clients"],
      onSuccess: (r) => {
        setMessaging(false);
        setMessage("");
        if (r.delivered) toast.success("Update sent to the client");
        else if (!r.has_phone) toast.warning("Saved, but the client has no phone number");
        else
          toast.warning(
            "Saved in the client's history. No SMS provider is set up, so it was not sent.",
          );
      },
    },
  );
  const markDoc = useWrite<{ docId: string; status: string; file_id?: string }>(
    "PUT",
    (b) => `/client-documents/${b.docId}`,
    {
      invalidate: refresh,
      success: "Document updated",
      toBody: ({ docId: _docId, ...rest }) => rest,
    },
  );
  const voidPayment = useWrite<{ id: string; reason: string }>(
    "POST",
    (b) => `/service-payments/${b.id}/void`,
    {
      invalidate: refresh,
      success: "Payment voided",
      toBody: (b) => ({ reason: b.reason }),
      onSuccess: () => setVoiding(null),
    },
  );
  const voidCost = useWrite<{ id: string; reason: string }>(
    "POST",
    (b) => `/service-costs/${b.id}/void`,
    {
      invalidate: refresh,
      success: "Cost voided",
      toBody: (b) => ({ reason: b.reason }),
      onSuccess: () => setVoidingCost(null),
    },
  );

  if (project.isLoading) return <PageLoader />;
  const p = project.data;
  if (!p)
    return (
      <p className="text-destructive">
        Project not found.{" "}
        <Link className="underline" to="/projects">
          Back
        </Link>
      </p>
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
    <>
      <PageHeader
        title={p.name}
        description={`${p.number} · Client: ${p.client_name}${p.owner_name ? ` · Owner: ${p.owner_name}` : ""}`}
        actions={
          <>
            <StatusBadge value={p.status} />
            {manage && (
              <Button
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
                Edit
              </Button>
            )}
            {billing && <Button onClick={() => setPaying(true)}>Add payment</Button>}
            {billing && (
              <Button variant="outline" onClick={() => setCosting(true)}>
                Add expense
              </Button>
            )}
            {manage && (
              <Button variant="outline" onClick={() => setMessaging(true)}>
                Send update to client
              </Button>
            )}
          </>
        }
      />
      <section className="mb-6 rounded-2xl border bg-card p-5">
        <div className="mb-2 flex items-center justify-between text-sm">
          <span className="font-medium">
            {p.stages_done} of {p.stages_total} stages done
          </span>
          <span className="text-muted-foreground">
            {p.current_stage ? `Now: ${p.current_stage}` : "All stages complete"}
          </span>
        </div>
        <Progress value={percent} />
      </section>

      <div className="mb-6 overflow-x-auto rounded-2xl border bg-card">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b text-left text-muted-foreground">
              <th className="px-4 py-3 font-medium">Stage</th>
              <th className="px-4 py-3 font-medium">Status</th>
              <th className="px-4 py-3 font-medium">Owner</th>
              <th className="px-4 py-3 font-medium">Deadline</th>
              <th className="px-4 py-3 font-medium">Documents</th>
              <th className="px-4 py-3" />
            </tr>
          </thead>
          <tbody>
            {p.stages.map((s) => {
              const left = daysUntil(s.deadline);
              const late = left !== null && left < 0 && s.status !== "done";
              const done = s.checklist.filter((c) => c.done).length;
              return (
                <tr key={s.id} className="border-b last:border-0">
                  <td className="px-4 py-3">
                    <p className="font-medium">
                      {s.position}. {s.name}
                    </p>
                    {s.checklist.length > 0 && (
                      <p className="text-xs text-muted-foreground">
                        Checklist {done}/{s.checklist.length}
                      </p>
                    )}
                    {s.note && <p className="text-xs text-muted-foreground">{s.note}</p>}
                  </td>
                  <td className="px-4 py-3">
                    <StatusBadge value={s.status} />
                  </td>
                  <td className="px-4 py-3">{s.owner_name ?? "-"}</td>
                  <td className={`px-4 py-3 ${late ? "font-medium text-destructive" : ""}`}>
                    {s.deadline ? formatDate(s.deadline) : "-"}
                    {late && ` (${-left!}d late)`}
                  </td>
                  <td className="px-4 py-3">
                    {s.documents_missing > 0 ? (
                      <span className="text-destructive">{s.documents_missing} missing</span>
                    ) : (
                      "-"
                    )}
                  </td>
                  <td className="space-x-2 px-4 py-3 text-right">
                    {can("services.design.manage") &&
                      s.status !== "done" &&
                      /brand|design|logo/i.test(s.name) && (
                        <Button size="sm" variant="outline" onClick={() => setJobsFor(s)}>
                          Design jobs
                        </Button>
                      )}
                    {manage && (
                      <Button size="sm" variant="outline" onClick={() => setStage(s)}>
                        Update
                      </Button>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
        {manage && (
          <div className="border-t p-3">
            <Button size="sm" variant="ghost" onClick={() => setAddingStage(true)}>
              <Plus size={14} /> Add a stage
            </Button>
          </div>
        )}
      </div>

      <div className="mb-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Tile label="Total" value={formatMoney(p.total_price)} />
        <Tile label="Paid" value={formatMoney(p.paid)} />
        <Tile
          label="Balance"
          value={formatMoney(p.balance)}
          tone={toNumber(p.balance) > 0 ? "bad" : undefined}
        />
        <Tile label={`Costs · profit ${formatMoney(p.profit)}`} value={formatMoney(p.costs)} />
      </div>

      <Tabs defaultValue="overview">
        <TabsList className="mb-4 flex-wrap">
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="documents">Documents</TabsTrigger>
          <TabsTrigger value="payments">Payments</TabsTrigger>
          <TabsTrigger value="costs">Costs</TabsTrigger>
          <TabsTrigger value="notes">Notes</TabsTrigger>
          <TabsTrigger value="history">History</TabsTrigger>
        </TabsList>

        <TabsContent value="overview" className="space-y-6">
          {p.notes && <p className="rounded-2xl border bg-card p-4 text-sm">{p.notes}</p>}
          <div>
            <h2 className="mb-2 font-semibold">Invoices</h2>
            <DataGrid
              rows={p.invoices}
              rowKey={(i) => i.id}
              empty="No invoices for this project"
              columns={[
                {
                  header: "Invoice",
                  cell: (i) => (
                    <Link className="underline" to={`/service-invoices?open=${i.id}`}>
                      {i.number}
                    </Link>
                  ),
                },
                { header: "Status", cell: (i) => <StatusBadge value={i.status} /> },
                { header: "Total", align: "right", cell: (i) => formatMoney(i.total) },
                { header: "Balance", align: "right", cell: (i) => formatMoney(i.balance) },
              ]}
            />
          </div>
          <div>
            <h2 className="mb-2 font-semibold">Design jobs</h2>
            <DataGrid
              rows={p.design_jobs}
              rowKey={(j) => j.id}
              empty="No design jobs linked to this project"
              columns={[
                {
                  header: "Job",
                  cell: (j) => (
                    <Link className="underline" to={`/design-jobs/${j.id}`}>
                      {j.title}
                    </Link>
                  ),
                },
                { header: "Type", cell: (j) => label(j.job_type) },
                { header: "Designer", cell: (j) => j.designer_name ?? "-" },
                { header: "Status", cell: (j) => <StatusBadge value={j.status} /> },
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
            empty="This service needs no documents"
            columns={[
              { header: "Document", cell: (d) => <span className="font-medium">{d.name}</span> },
              { header: "Status", cell: (d) => <StatusBadge value={d.status} /> },
              {
                header: "Received",
                cell: (d) => (d.received_at ? formatDate(d.received_at) : "-"),
              },
              {
                header: "",
                className: "text-right",
                cell: (d) => (
                  <div className="flex justify-end gap-2">
                    {d.file_id && (
                      <>
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => setPreviewDoc({ id: d.file_id!, name: d.name })}
                        >
                          <Eye size={14} /> Preview
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
                          <Download size={14} /> Download
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
                          <Upload size={14} /> Upload
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
                          {d.status === "received" ? "Mark missing" : "Mark received"}
                        </Button>
                      </>
                    )}
                  </div>
                ),
              },
            ]}
          />
        </TabsContent>

        <TabsContent value="payments">
          <DataGrid
            rows={p.payments}
            rowKey={(x) => x.id}
            empty="No payments yet. Add the deposit to issue a receipt."
            columns={[
              { header: "Receipt", cell: (x) => x.number },
              { header: "Date", cell: (x) => formatDate(x.date) },
              { header: "Method", cell: (x) => label(x.method) },
              { header: "Status", cell: (x) => <StatusBadge value={x.status} /> },
              { header: "Amount", align: "right", cell: (x) => formatMoney(x.amount) },
              {
                header: "",
                className: "text-right",
                cell: (x) => (
                  <div className="flex justify-end gap-2">
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
                      <Download size={14} /> Receipt PDF
                    </Button>
                    {billing && x.status === "posted" && (
                      <Button
                        size="sm"
                        variant="ghost"
                        className="text-destructive"
                        onClick={() => setVoiding(x.id)}
                      >
                        Void
                      </Button>
                    )}
                  </div>
                ),
              },
            ]}
          />
          <p className="mt-3 text-sm text-muted-foreground">
            Balance still due: <strong>{formatMoney(p.balance)}</strong>
          </p>
        </TabsContent>

        <TabsContent value="costs">
          <DataGrid
            rows={p.costs_list}
            rowKey={(x) => x.id}
            empty="No costs recorded"
            columns={[
              { header: "Date", cell: (x) => formatDate(x.date) },
              { header: "Type", cell: (x) => label(x.kind) },
              { header: "Description", cell: (x) => x.description },
              { header: "Contractor", cell: (x) => x.contractor_name ?? "-" },
              { header: "Status", cell: (x) => <StatusBadge value={x.status} /> },
              { header: "Amount", align: "right", cell: (x) => formatMoney(x.amount) },
              {
                header: "",
                className: "text-right",
                cell: (x) =>
                  billing && x.status !== "void" && toNumber(x.settled) === 0 ? (
                    <Button
                      size="sm"
                      variant="ghost"
                      className="text-destructive"
                      onClick={() => setVoidingCost(x.id)}
                    >
                      Void
                    </Button>
                  ) : null,
              },
            ]}
          />
          <p className="mt-3 text-sm text-muted-foreground">
            Profit = payments received minus costs: <strong>{formatMoney(p.profit)}</strong>
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
                aria-label="Type"
                className="w-36"
                value={note.kind}
                onChange={(e) => setNote((n) => ({ ...n, kind: e.target.value }))}
              >
                <option value="call">Call</option>
                <option value="message">Message</option>
                <option value="meeting">Meeting</option>
                <option value="note">Note</option>
              </NativeSelect>
              <Input
                aria-label="Note"
                className="min-w-60 flex-1"
                placeholder="What was said or agreed?"
                value={note.body}
                onChange={(e) => setNote((n) => ({ ...n, body: e.target.value }))}
              />
              <Button type="submit" disabled={addNote.isPending}>
                Add note
              </Button>
            </form>
          )}
          <DataGrid
            rows={notes.data}
            loading={notes.isLoading}
            rowKey={(n) => n.id}
            empty="No calls, messages or meetings recorded"
            columns={[
              { header: "Date", cell: (n) => formatDate(n.noted_on) },
              { header: "Type", cell: (n) => label(n.kind) },
              { header: "By", cell: (n) => n.author ?? "-" },
              { header: "Note", cell: (n) => n.body, className: "whitespace-normal" },
            ]}
          />
        </TabsContent>

        <TabsContent value="history" className="space-y-6">
          <div>
            <h2 className="mb-2 font-semibold">Stage updates</h2>
            <DataGrid
              rows={p.stage_events ?? []}
              rowKey={(e) => e.id}
              empty="No stage updates yet"
              columns={[
                { header: "When", cell: (e) => formatDateTime(e.created_at) },
                {
                  header: "Stage",
                  cell: (e) => <span className="font-medium">{e.stage_name}</span>,
                },
                {
                  header: "Change",
                  cell: (e) =>
                    `${e.from_status ? label(e.from_status) : "Created"} → ${label(e.to_status)}`,
                },
                { header: "By", cell: (e) => e.actor_name ?? "-" },
                { header: "Note", cell: (e) => e.note ?? "-", className: "whitespace-normal" },
              ]}
            />
          </div>
          <div>
            <h2 className="mb-2 font-semibold">Record audit log</h2>
            <RecordHistoryTab entityType="svc_project" entityId={p.id} />
          </div>
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
        title="Add payment"
        hint="Pays issued invoices first. Anything left is held as a deposit for this project."
        path={`/projects/${id}/payments`}
        defaultAmount={toNumber(p.balance) > 0 ? p.balance : undefined}
        invalidate={refresh}
        success="Payment received. Receipt issued."
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
        title="Void this payment?"
        description="Its invoice allocations and deposit are reversed and the money leaves the account."
        submitLabel="Void payment"
        busy={voidPayment.isPending}
        onSubmit={(reason) => voiding && voidPayment.mutate({ id: voiding, reason })}
      />
      <ReasonDialog
        open={!!voidingCost}
        onOpenChange={(o) => !o && setVoidingCost(null)}
        title="Void this cost?"
        submitLabel="Void cost"
        busy={voidCost.isPending}
        onSubmit={(reason) => voidingCost && voidCost.mutate({ id: voidingCost, reason })}
      />
      <FormDialog
        open={messaging}
        onOpenChange={setMessaging}
        title="Send update to client"
        description="Sent by SMS and saved in the client's history."
        submitLabel="Send"
        busy={sendUpdate.isPending}
        onSubmit={() => sendUpdate.mutate({ message })}
      >
        <div className="flex flex-wrap gap-2">
          {[
            "Your TIN is ready.",
            "Your business name has been approved.",
            "We need one more document from you. Please bring it to the office.",
            "Your project is complete. You can collect your documents.",
          ].map((t) => (
            <Button key={t} type="button" size="sm" variant="outline" onClick={() => setMessage(t)}>
              {t.length > 28 ? `${t.slice(0, 28)}…` : t}
            </Button>
          ))}
        </div>
        <Field label="Message">
          <Textarea
            required
            rows={3}
            value={message}
            onChange={(e) => setMessage(e.target.value)}
          />
        </Field>
        {message.trim() && (
          <div className="flex justify-end">
            <Button type="button" size="sm" variant="outline" asChild>
              <a
                href={whatsappUrl(undefined, `${p.name}: ${message}`)}
                target="_blank"
                rel="noreferrer"
              >
                <MessageCircle size={14} /> Open in WhatsApp
              </a>
            </Button>
          </div>
        )}
      </FormDialog>
      <FormDialog
        open={editing}
        onOpenChange={setEditing}
        title="Edit project"
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
        <Field label="Name">
          <Input
            required
            value={edit.name}
            onChange={(e) => setEdit({ ...edit, name: e.target.value })}
          />
        </Field>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Status">
            <NativeSelect
              value={edit.status}
              onChange={(e) => setEdit({ ...edit, status: e.target.value })}
            >
              <option value="in_progress">In progress</option>
              <option value="on_hold">On hold</option>
              <option value="completed">Completed</option>
              <option value="cancelled">Cancelled</option>
            </NativeSelect>
          </Field>
          <Field label="Total price">
            <Input
              inputMode="decimal"
              value={edit.total_price}
              onChange={(e) => setEdit({ ...edit, total_price: e.target.value })}
            />
          </Field>
        </div>
        <Field label="Notes">
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
        title="Add a stage"
        busy={addStage.isPending}
        onSubmit={() =>
          addStage.mutate({ name: newStage.name, deadline: newStage.deadline || undefined })
        }
      >
        <Field label="Stage name">
          <Input
            required
            value={newStage.name}
            onChange={(e) => setNewStage({ ...newStage, name: e.target.value })}
          />
        </Field>
        <Field label="Deadline">
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
    </>
  );
}
