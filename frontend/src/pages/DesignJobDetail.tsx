import { useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { Download, MessageCircle, Play, Square, Upload } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
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
import { CopyButton, PageLoader } from "@/components/shared/Display";
import { FilePreviewDialog, FileThumbnail } from "@/components/shared/FilePreview";
import { RecordHistoryTab } from "@/components/shared/RecordHistoryTab";
import { useAuth } from "@/features/auth/auth";
import { CostDialog } from "@/features/services/CostDialog";
import { PaymentDialog, StaffSelect, Tile } from "@/features/services/shared";
import { label, whatsappUrl } from "@/features/services/helpers";
import { errorMessage, useGet, useWrite } from "@/hooks/useApi";
import { ApiError, download, uploadFile } from "@/lib/api";
import { formatDate, formatDateTime, formatMoney, toNumber } from "@/lib/formatters";
import {
  DESIGN_NEXT,
  type Contractor,
  type DesignJobDetail as Job,
  type DesignVersion,
} from "@/types/services";

const STEP_LABEL: Record<string, string> = {
  in_design: "Start design",
  internal_review: "Internal review",
  with_client: "Mark as with client",
  changes_requested: "Client wants changes",
  approved: "Mark approved",
  printing: "Sent to print",
  delivered: "Mark delivered",
  cancelled: "Cancel job",
};

function VersionDialog({ job, onClose }: { job: Job | null; onClose: () => void }) {
  const [kind, setKind] = useState("design");
  const [link, setLink] = useState("");
  const [notes, setNotes] = useState("");
  const [fileId, setFileId] = useState<string | null>(null);
  const [fileName, setFileName] = useState("");
  const [uploading, setUploading] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  const add = useWrite("POST", `/design-jobs/${job?.id}/versions`, {
    invalidate: ["/design-jobs"],
    success: "Version added",
    onSuccess: () => {
      setLink("");
      setNotes("");
      setFileId(null);
      setFileName("");
      onClose();
    },
  });
  const afterApproval = job && ["approved", "printing", "delivered"].includes(job.status);
  return (
    <FormDialog
      open={!!job}
      onOpenChange={(o) => !o && onClose()}
      title="Upload a version"
      description="Add an image or PDF, or a link to the working file."
      busy={add.isPending || uploading}
      onSubmit={() => {
        if (!fileId && !link.trim()) {
          toast.error("Add a file or a link");
          return;
        }
        add.mutate({
          kind,
          file_id: fileId ?? undefined,
          link: link || undefined,
          notes: notes || undefined,
        });
      }}
    >
      <Field label="What is this?">
        <NativeSelect value={kind} onChange={(e) => setKind(e.target.value)}>
          <option value="design">Design for the client</option>
          {afterApproval && <option value="print_ready">Print-ready file</option>}
          {afterApproval && <option value="source">Source file</option>}
        </NativeSelect>
      </Field>
      <Field label="File">
        <input
          ref={input}
          type="file"
          accept="image/*,application/pdf"
          className="hidden"
          onChange={async (e) => {
            const f = e.target.files?.[0];
            e.target.value = "";
            if (!f) return;
            setUploading(true);
            try {
              setFileId(await uploadFile(f));
              setFileName(f.name);
            } catch (error) {
              toast.error(errorMessage(error));
            } finally {
              setUploading(false);
            }
          }}
        />
        <div className="flex items-center gap-2">
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={uploading}
            onClick={() => input.current?.click()}
          >
            <Upload size={14} /> {uploading ? "Uploading…" : "Choose file"}
          </Button>
          <span className="od-caption">{fileName}</span>
        </div>
      </Field>
      <Field label="Or a link">
        <Input
          type="url"
          placeholder="https://…"
          value={link}
          onChange={(e) => setLink(e.target.value)}
        />
      </Field>
      <Field label="Notes">
        <Textarea rows={2} value={notes} onChange={(e) => setNotes(e.target.value)} />
      </Field>
    </FormDialog>
  );
}

function PrintDialog({ job, onClose }: { job: Job | null; onClose: () => void }) {
  const { can } = useAuth();
  const contractors = useGet<Contractor[]>(
    "/contractors",
    { per_page: 100 },
    { enabled: can("services.contractor.manage") },
  );
  const [d, setD] = useState({
    contractor_id: "",
    quantity: "1",
    cost: "",
    due_date: "",
    note: "",
  });
  const create = useWrite("POST", `/design-jobs/${job?.id}/print-orders`, {
    invalidate: ["/design-jobs", "/service-costs", "/contractors"],
    success: "Print order created",
    onSuccess: onClose,
  });
  return (
    <FormDialog
      open={!!job}
      onOpenChange={(o) => !o && onClose()}
      title="Print order"
      description="The cost is added to this job and owed to the printer."
      busy={create.isPending}
      onSubmit={() =>
        create.mutate({
          contractor_id: d.contractor_id || undefined,
          quantity: Number(d.quantity) || 1,
          cost: d.cost || "0",
          due_date: d.due_date || undefined,
          note: d.note || undefined,
        })
      }
    >
      <div className="grid gap-3 sm:grid-cols-2">
        {can("services.contractor.manage") && (
          <Field label="Printer" className="sm:col-span-2">
            <NativeSelect
              value={d.contractor_id}
              onChange={(e) => setD({ ...d, contractor_id: e.target.value })}
            >
              <option value="">Choose a printer…</option>
              {contractors.data
                ?.filter((c) => c.is_active)
                .map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name}
                  </option>
                ))}
            </NativeSelect>
          </Field>
        )}
        <Field label="Quantity">
          <Input
            required
            inputMode="numeric"
            value={d.quantity}
            onChange={(e) => setD({ ...d, quantity: e.target.value })}
          />
        </Field>
        <Field label="Cost">
          <Input
            required
            inputMode="decimal"
            value={d.cost}
            onChange={(e) => setD({ ...d, cost: e.target.value })}
          />
        </Field>
        <Field label="Due date" className="sm:col-span-2">
          <Input
            type="date"
            value={d.due_date}
            onChange={(e) => setD({ ...d, due_date: e.target.value })}
          />
        </Field>
        <Field label="Note" className="sm:col-span-2">
          <Input value={d.note} onChange={(e) => setD({ ...d, note: e.target.value })} />
        </Field>
      </div>
    </FormDialog>
  );
}

function EditDialog({ job, onClose }: { job: Job | null; onClose: () => void }) {
  const [d, setD] = useState({
    title: job?.title ?? "",
    brief: job?.brief ?? "",
    deadline: job?.deadline ?? "",
    priority: job?.priority ?? "normal",
    designer_id: job?.designer_id ?? "",
    price: job?.price ?? "",
    deposit: job?.deposit ?? "",
    revisions_included: String(job?.revisions_included ?? 3),
    printing_needed: job?.printing_needed ?? false,
  });
  const save = useWrite("PUT", `/design-jobs/${job?.id}`, {
    invalidate: ["/design-jobs", "/services"],
    success: "Job updated",
    onSuccess: onClose,
  });
  return (
    <FormDialog
      open={!!job}
      onOpenChange={(o) => !o && onClose()}
      title="Edit design job"
      busy={save.isPending}
      onSubmit={() =>
        save.mutate({
          title: d.title,
          brief: d.brief,
          deadline: d.deadline || undefined,
          priority: d.priority,
          designer_id: d.designer_id || undefined,
          price: d.price,
          deposit: d.deposit,
          revisions_included: Number(d.revisions_included) || 0,
          printing_needed: d.printing_needed,
        })
      }
      wide
    >
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Title" className="sm:col-span-2">
          <Input required value={d.title} onChange={(e) => setD({ ...d, title: e.target.value })} />
        </Field>
        <Field label="Brief" className="sm:col-span-2">
          <Textarea
            rows={3}
            value={d.brief}
            onChange={(e) => setD({ ...d, brief: e.target.value })}
          />
        </Field>
        <Field label="Deadline">
          <Input
            type="date"
            value={d.deadline}
            onChange={(e) => setD({ ...d, deadline: e.target.value })}
          />
        </Field>
        <Field label="Priority">
          <NativeSelect
            value={d.priority}
            onChange={(e) => setD({ ...d, priority: e.target.value as "normal" | "urgent" })}
          >
            <option value="normal">Normal</option>
            <option value="urgent">Urgent</option>
          </NativeSelect>
        </Field>
        <Field label="Designer">
          <StaffSelect value={d.designer_id} onChange={(v) => setD({ ...d, designer_id: v })} />
        </Field>
        <Field label="Revisions included">
          <Input
            inputMode="numeric"
            value={d.revisions_included}
            onChange={(e) => setD({ ...d, revisions_included: e.target.value })}
          />
        </Field>
        <Field label="Price">
          <Input
            inputMode="decimal"
            value={d.price}
            onChange={(e) => setD({ ...d, price: e.target.value })}
          />
        </Field>
        <Field label="Deposit">
          <Input
            inputMode="decimal"
            value={d.deposit}
            onChange={(e) => setD({ ...d, deposit: e.target.value })}
          />
        </Field>
      </div>
      <label className="flex items-center gap-2 text-[13px] text-[var(--od-ink-secondary)]">
        <input
          type="checkbox"
          checked={d.printing_needed}
          onChange={(e) => setD({ ...d, printing_needed: e.target.checked })}
        />
        Printing needed
      </label>
    </FormDialog>
  );
}

function Preview({
  v,
  onPreview,
}: {
  v: DesignVersion;
  onPreview: (file: { id: string; name: string }) => void;
}) {
  if (v.file_id) {
    const name = v.file_name ?? `version-${v.version_no}`;
    return (
      <div className="flex flex-col items-start gap-1.5">
        <FileThumbnail
          fileId={v.file_id}
          fileName={name}
          mimeType={v.mime_type}
          onOpen={() => onPreview({ id: v.file_id!, name })}
        />
        <Button
          size="sm"
          variant="ghost"
          className="h-7 px-2 text-xs"
          onClick={() =>
            download(`/files/${v.file_id}`, {}, name).catch((e) => toast.error(errorMessage(e)))
          }
        >
          <Download size={12} /> Download
        </Button>
      </div>
    );
  }
  return (
    <div className="flex h-28 w-40 items-center justify-center overflow-hidden rounded-[var(--od-radius-sm,8px)] border border-[var(--od-border-strong)] bg-[var(--od-surface-subtle)] text-xs text-[var(--od-ink-muted)]">
      {v.link ? (
        <a
          className="p-2 text-center font-medium text-[var(--od-ink)] underline underline-offset-4"
          href={v.link}
          target="_blank"
          rel="noreferrer"
        >
          Open link
        </a>
      ) : (
        "No file"
      )}
    </div>
  );
}

export default function DesignJobDetail() {
  const { id = "" } = useParams();
  const { can } = useAuth();
  const jobQuery = useGet<Job>(`/design-jobs/${id}`);
  const dash = useGet<{ timer_job_id?: string | null }>("/services/dashboard");
  const [versioning, setVersioning] = useState(false);
  const [paying, setPaying] = useState(false);
  const [costing, setCosting] = useState(false);
  const [printing, setPrinting] = useState(false);
  const [editing, setEditing] = useState(false);
  const [charging, setCharging] = useState(false);
  const [extraCharge, setExtraCharge] = useState({ amount: "", extra_revisions: "1", note: "" });
  const [previewFile, setPreviewFile] = useState<{ id: string; name: string } | null>(null);
  const [comment, setComment] = useState("");
  const [link, setLink] = useState<{ path: string; delivered: boolean } | null>(null);

  const refresh = ["/design-jobs", "/services", "/service-", "/print-orders"];
  const chargeExtra = useWrite("POST", `/design-jobs/${id}/extra-revision-charge`, {
    invalidate: refresh,
    success: "Extra revision charge added",
    onSuccess: () => {
      setCharging(false);
      setExtraCharge({ amount: "", extra_revisions: "1", note: "" });
    },
  });
  const move = useWrite<{ status: string }>("POST", `/design-jobs/${id}/status`, {
    invalidate: refresh,
    success: "Status updated",
  });
  const send = useWrite<
    { versionId: string; channel?: string },
    { approval_path: string; delivered?: boolean; queued?: boolean }
  >("POST", (b) => `/design-jobs/${id}/versions/${b.versionId}/send`, {
    invalidate: refresh,
    toBody: (b) => ({ channel: b.channel ?? "sms" }),
    onSuccess: (r) => {
      const sent = Boolean(r.queued ?? r.delivered);
      setLink({ path: r.approval_path, delivered: sent });
      if (sent) toast.success("Approval link queued for the client");
    },
    onError: (err) => {
      if (err instanceof ApiError && err.status === 503) {
        const d = err.data as { approval_path?: string } | undefined;
        if (d?.approval_path) {
          setLink({ path: d.approval_path, delivered: false });
        }
      }
    },
  });
  const feedback = useWrite<{ comment: string; version_id?: string }>(
    "POST",
    `/design-jobs/${id}/feedback`,
    {
      invalidate: ["/design-jobs"],
      success: "Comment added",
      onSuccess: () => setComment(""),
    },
  );
  const startTimer = useWrite("POST", `/design-jobs/${id}/timer/start`, {
    invalidate: refresh,
    success: "Timer started",
  });
  const stopTimer = useWrite("POST", `/design-jobs/${id}/timer/stop`, {
    invalidate: refresh,
    success: "Timer stopped",
  });
  const invoice = useWrite<unknown, { id: string }>("POST", "/service-invoices", {
    invalidate: ["/service-invoices"],
    success: "Draft invoice created. Open Invoices to issue it.",
  });
  const printStatus = useWrite<{ id: string; status: string }>(
    "PUT",
    (b) => `/print-orders/${b.id}`,
    {
      invalidate: refresh,
      success: "Print order updated",
      toBody: (b) => ({ status: b.status }),
    },
  );

  if (jobQuery.isLoading) return <PageLoader />;
  const job = jobQuery.data;
  if (!job)
    return (
      <div className="od-card items-center py-10 text-center">
        <p className="text-[14px] font-semibold text-[var(--od-bad)]">Design job not found.</p>
        <Button asChild variant="outline" size="sm" className="mt-3">
          <Link to="/design-jobs">Back to design jobs</Link>
        </Button>
      </div>
    );
  const manage = can("services.design.manage");
  const billing = can("services.invoice.manage");
  const canViewDesign = manage || can("services.design.view_all");
  const next = DESIGN_NEXT[job.status] ?? [];
  const timerHere = dash.data?.timer_job_id === id;
  const latest = job.versions.find((v) => v.kind === "design");

  return (
    <div className="space-y-6">
      <PageHeader
        backTo="/design-jobs"
        backLabel="Design jobs"
        title={job.title}
        badge={
          <div className="flex items-center gap-1.5">
            <StatusBadge value={job.overdue ? "overdue" : job.status} />
            {job.priority === "urgent" && <StatusBadge value="urgent" />}
          </div>
        }
        description={`${job.number} · Client: ${job.client_name}${job.designer_name ? ` · Designer: ${job.designer_name}` : ""}`}
        actions={
          <>
            {manage && (
              <Button size="sm" variant="outline" onClick={() => setEditing(true)}>
                Edit
              </Button>
            )}
            {manage && (
              <Button size="sm" variant="outline" onClick={() => setVersioning(true)}>
                Upload version
              </Button>
            )}
            {manage &&
              latest &&
              ["in_design", "internal_review", "with_client"].includes(job.status) && (
                <Button
                  size="sm"
                  onClick={() => send.mutate({ versionId: latest.id })}
                  disabled={send.isPending}
                >
                  Send for approval
                </Button>
              )}
            {billing && (
              <Button size="sm" variant="outline" onClick={() => setPaying(true)}>
                Add payment
              </Button>
            )}
          </>
        }
      />

      <div className="flex flex-wrap items-center gap-x-5 gap-y-1.5 rounded-[var(--od-radius-md,12px)] border border-[var(--od-border)] bg-[var(--od-surface-subtle)] px-4 py-2.5 text-[13px] text-[var(--od-ink-secondary)]">
        {job.project_id && (
          <span>
            Project:{" "}
            <Link
              className="od-mono font-medium text-[var(--od-ink)] underline underline-offset-4"
              to={`/projects/${job.project_id}`}
            >
              {job.project_number}
            </Link>
          </span>
        )}
        <span>Type: {label(job.job_type)}</span>
        {job.deadline && (
          <span className={job.overdue ? "font-semibold text-[var(--od-bad)]" : undefined}>
            Due: {formatDate(job.deadline)}
          </span>
        )}
        <span
          className={
            job.revisions_over ? "od-tabular font-semibold text-[var(--od-bad)]" : "od-tabular"
          }
        >
          Revisions: {job.revisions_used} of {job.revisions_included}
        </span>
      </div>

      {job.revisions_over && (
        <div className="flex flex-wrap items-center justify-between gap-3 rounded-[var(--od-radius-md,12px)] border border-[color-mix(in_srgb,var(--od-bad-mark)_30%,transparent)] bg-[color-mix(in_srgb,var(--od-bad-mark)_10%,transparent)] px-4 py-3 text-[13px] font-medium text-[var(--od-bad)]">
          <p>
            The included revisions are used up. Add an extra charge to the price before more
            changes.
          </p>
          {manage && (
            <Button size="sm" variant="outline" onClick={() => setCharging(true)}>
              Add extra charge
            </Button>
          )}
        </div>
      )}

      {link && (
        <SectionCard
          title={
            link.delivered
              ? "Approval link queued for the client (valid for 14 days)"
              : "Share this private link with the client (valid for 14 days)"
          }
        >
          <div className="flex flex-wrap items-center gap-2">
            <code className="od-mono flex-1 break-all rounded-[var(--od-radius-sm,8px)] bg-[var(--od-surface-subtle)] px-3 py-1.5 text-xs">
              {`${window.location.origin}${link.path}`}
            </code>
            <CopyButton text={`${window.location.origin}${link.path}`} />
            {!link.delivered && (
              <Button size="sm" variant="outline" asChild>
                <a
                  href={whatsappUrl(
                    undefined,
                    `Design ready for your approval (${job.title}): ${window.location.origin}${link.path}`,
                  )}
                  target="_blank"
                  rel="noreferrer"
                >
                  <MessageCircle size={13} /> Open in WhatsApp
                </a>
              </Button>
            )}
          </div>
        </SectionCard>
      )}

      {canViewDesign && (
        <div className="flex flex-wrap items-center gap-2">
          {manage &&
            next.map((s) => (
              <Button
                key={s}
                size="sm"
                variant={s === "cancelled" ? "ghost" : "outline"}
                className={
                  s === "cancelled" ? "text-[var(--od-bad)] hover:text-[var(--od-bad)]" : undefined
                }
                disabled={move.isPending}
                onClick={() => move.mutate({ status: s })}
              >
                {STEP_LABEL[s] ?? label(s)}
              </Button>
            ))}
          {manage && job.status === "approved" && (
            <Button size="sm" onClick={() => setPrinting(true)}>
              Create print order
            </Button>
          )}
          {billing && job.invoices.length === 0 && (
            <Button
              size="sm"
              variant="outline"
              onClick={() => invoice.mutate({ client_id: job.client_id, design_job_id: job.id })}
            >
              Create invoice
            </Button>
          )}
          {canViewDesign &&
            (timerHere ? (
              <Button size="sm" variant="outline" onClick={() => stopTimer.mutate(undefined)}>
                <Square size={13} /> Stop timer
              </Button>
            ) : (
              <Button size="sm" variant="outline" onClick={() => startTimer.mutate(undefined)}>
                <Play size={13} /> Start timer
              </Button>
            ))}
        </div>
      )}

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Tile label="Price" value={formatMoney(job.price)} />
        <Tile label="Paid" value={formatMoney(job.paid)} />
        <Tile
          label="Balance"
          value={formatMoney(job.balance)}
          tone={toNumber(job.balance) > 0 ? "bad" : undefined}
        />
        <Tile label={`Costs · profit ${formatMoney(job.profit)}`} value={formatMoney(job.costs)} />
      </div>

      <Tabs defaultValue="files">
        <TabsList>
          <TabsTrigger value="brief">Brief</TabsTrigger>
          <TabsTrigger value="files">Files</TabsTrigger>
          <TabsTrigger value="feedback">Feedback</TabsTrigger>
          <TabsTrigger value="costs">Costs</TabsTrigger>
          <TabsTrigger value="payments">Payments</TabsTrigger>
          <TabsTrigger value="work">Print and time</TabsTrigger>
          <TabsTrigger value="history">History</TabsTrigger>
        </TabsList>

        <TabsContent value="brief" className="grid gap-4 sm:grid-cols-2">
          {(
            [
              ["Brief", job.brief],
              ["Size and format", job.size_format],
              ["Style notes", job.style_notes],
              ["Text to include", job.text_to_include],
              ["Printing", job.printing_needed ? "Needed after approval" : "Not needed"],
            ] as const
          ).map(([l, v]) => (
            <SectionCard key={l} title={l} className={l === "Brief" ? "sm:col-span-2" : undefined}>
              <p className="whitespace-pre-wrap text-[13px] text-[var(--od-ink-secondary)]">
                {v || "—"}
              </p>
            </SectionCard>
          ))}
          {job.references.length > 0 && (
            <SectionCard title="Reference files" className="sm:col-span-2">
              <div className="flex flex-wrap gap-2">
                {job.references.map((r) => (
                  <Button
                    key={r.file_id}
                    size="sm"
                    variant="outline"
                    onClick={() =>
                      download(`/files/${r.file_id}`, {}, r.file_name ?? "reference").catch((e) =>
                        toast.error(errorMessage(e)),
                      )
                    }
                  >
                    <Download size={13} /> {r.file_name ?? "File"}
                  </Button>
                ))}
              </div>
            </SectionCard>
          )}
        </TabsContent>

        <TabsContent value="files" className="space-y-3">
          {job.versions.length === 0 && (
            <SectionCard className="items-center py-8 text-center">
              <p className="od-caption">No versions yet. Upload the first design.</p>
            </SectionCard>
          )}
          {job.versions.map((v, i) => (
            <div key={v.id} className="od-card flex-row flex-wrap items-start gap-4">
              <Preview v={v} onPreview={setPreviewFile} />
              <div className="min-w-48 flex-1 text-[13px]">
                <p className="font-semibold text-[var(--od-ink)]">
                  {v.kind === "design" ? `Version ${v.version_no}` : label(v.kind)}
                  {i === 0 && v.kind === "design" && " (current)"}
                  {v.is_final && (
                    <span className="ml-2 rounded-full bg-[color-mix(in_srgb,var(--od-good-mark)_12%,transparent)] px-2 py-0.5 text-[11px] font-semibold text-[var(--od-good)]">
                      Final
                    </span>
                  )}
                </p>
                <p className="od-caption mt-0.5">
                  Uploaded {formatDateTime(v.created_at)} by {v.uploaded_by ?? "—"}
                </p>
                {v.notes && <p className="mt-2 text-[var(--od-ink-secondary)]">{v.notes}</p>}
                <div className="mt-2.5">
                  <StatusBadge value={v.status === "sent" ? "with_client" : v.status} />
                </div>
              </div>
              {manage &&
                v.kind === "design" &&
                ["draft", "sent", "changes_requested"].includes(v.status) &&
                i === 0 && (
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => send.mutate({ versionId: v.id })}
                  >
                    {v.status === "sent" ? "Send new link" : "Send for approval"}
                  </Button>
                )}
            </div>
          ))}
        </TabsContent>

        <TabsContent value="feedback" className="space-y-4">
          <form
            className="flex flex-wrap items-start gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              if (comment.trim()) feedback.mutate({ comment, version_id: latest?.id });
            }}
          >
            <Input
              aria-label="Comment"
              className="min-w-60 flex-1"
              placeholder="Add a comment on the current version…"
              value={comment}
              onChange={(e) => setComment(e.target.value)}
            />
            <Button type="submit" disabled={feedback.isPending}>
              Add comment
            </Button>
          </form>
          <DataGrid
            rows={job.feedback}
            rowKey={(f) => `${f.created_at}-${f.comment}`}
            empty="No feedback yet"
            columns={[
              { header: "When", cell: (f) => formatDateTime(f.created_at) },
              {
                header: "Version",
                cell: (f) => (
                  <span className="od-mono">{f.version_no ? `v${f.version_no}` : "—"}</span>
                ),
              },
              {
                header: "From",
                cell: (f) => (f.author_type === "client" ? "Client" : (f.author_name ?? "Staff")),
              },
              { header: "Comment", cell: (f) => f.comment, className: "whitespace-normal" },
            ]}
          />
        </TabsContent>

        <TabsContent value="costs" className="space-y-3">
          {billing && (
            <div className="flex justify-end">
              <Button size="sm" variant="outline" onClick={() => setCosting(true)}>
                Add cost
              </Button>
            </div>
          )}
          <DataGrid
            rows={job.costs_list}
            rowKey={(c) => c.id}
            empty="No costs recorded"
            columns={[
              { header: "Date", cell: (c) => formatDate(c.date) },
              { header: "Type", cell: (c) => label(c.kind) },
              { header: "Description", cell: (c) => c.description },
              { header: "Contractor", cell: (c) => c.contractor_name ?? "—" },
              { header: "Status", cell: (c) => <StatusBadge value={c.status} /> },
              { header: "Amount", align: "right", cell: (c) => formatMoney(c.amount) },
            ]}
          />
        </TabsContent>

        <TabsContent value="payments" className="space-y-4">
          <DataGrid
            rows={job.payments}
            rowKey={(p) => p.id}
            empty="No payments yet"
            columns={[
              {
                header: "Receipt",
                cell: (p) => <span className="od-mono font-medium">{p.number}</span>,
              },
              { header: "Date", cell: (p) => formatDate(p.date) },
              { header: "Method", cell: (p) => label(p.method) },
              { header: "Amount", align: "right", cell: (p) => formatMoney(p.amount) },
              {
                header: "",
                className: "text-right",
                cell: (p) => (
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() =>
                      download(
                        `/service-payments/${p.id}/receipt`,
                        { format: "pdf" },
                        `receipt-${p.number}.pdf`,
                      ).catch((e) => toast.error(errorMessage(e)))
                    }
                  >
                    <Download size={13} /> Receipt PDF
                  </Button>
                ),
              },
            ]}
          />
          {job.invoices.length > 0 && (
            <DataGrid
              rows={job.invoices}
              rowKey={(i) => i.id}
              columns={[
                {
                  header: "Invoice",
                  cell: (i) => (
                    <Link
                      className="od-mono font-medium underline underline-offset-4"
                      to={`/service-invoices?open=${i.id}`}
                    >
                      {i.number}
                    </Link>
                  ),
                },
                { header: "Status", cell: (i) => <StatusBadge value={i.status} /> },
                { header: "Total", align: "right", cell: (i) => formatMoney(i.total) },
                { header: "Balance", align: "right", cell: (i) => formatMoney(i.balance) },
              ]}
            />
          )}
        </TabsContent>

        <TabsContent value="work" className="space-y-6">
          <div className="space-y-2.5">
            <h2 className="od-section-title">Print orders</h2>
            <DataGrid
              rows={job.print_orders}
              rowKey={(o) => o.id}
              empty="No print orders"
              columns={[
                { header: "Printer", cell: (o) => o.contractor_name ?? "—" },
                { header: "Quantity", align: "right", cell: (o) => o.quantity },
                { header: "Due", cell: (o) => (o.due_date ? formatDate(o.due_date) : "—") },
                { header: "Status", cell: (o) => <StatusBadge value={o.status} /> },
                { header: "Cost", align: "right", cell: (o) => formatMoney(o.cost) },
                {
                  header: "",
                  className: "text-right",
                  cell: (o) =>
                    manage && (o.status === "sent" || o.status === "ready") ? (
                      <div className="flex justify-end gap-1.5">
                        {o.status === "sent" && (
                          <Button
                            size="sm"
                            variant="outline"
                            onClick={() => printStatus.mutate({ id: o.id, status: "ready" })}
                          >
                            Mark ready
                          </Button>
                        )}
                        {o.status === "ready" && (
                          <Button
                            size="sm"
                            variant="outline"
                            onClick={() => printStatus.mutate({ id: o.id, status: "delivered" })}
                          >
                            Mark delivered
                          </Button>
                        )}
                        <Button
                          size="sm"
                          variant="ghost"
                          className="text-[var(--od-bad)] hover:text-[var(--od-bad)]"
                          onClick={() => printStatus.mutate({ id: o.id, status: "cancelled" })}
                        >
                          Cancel
                        </Button>
                      </div>
                    ) : null,
                },
              ]}
            />
          </div>
          <div className="space-y-2.5">
            <h2 className="od-section-title">Time logged</h2>
            <DataGrid
              rows={job.time}
              rowKey={(t) => t.started_at}
              empty="No time logged"
              columns={[
                { header: "Who", cell: (t) => t.user_name ?? "—" },
                { header: "Started", cell: (t) => formatDateTime(t.started_at) },
                {
                  header: "Ended",
                  cell: (t) => (t.ended_at ? formatDateTime(t.ended_at) : "Running"),
                },
                { header: "Minutes", align: "right", cell: (t) => t.minutes ?? "—" },
              ]}
            />
          </div>
        </TabsContent>

        <TabsContent value="history">
          <SectionCard title="Record audit log">
            <RecordHistoryTab entityType="design_job" entityId={job.id} />
          </SectionCard>
        </TabsContent>
      </Tabs>

      <FormDialog
        open={charging}
        onOpenChange={setCharging}
        title="Add extra revision charge"
        description="Adds the fee to this job's price and grants additional revisions."
        busy={chargeExtra.isPending}
        onSubmit={() =>
          chargeExtra.mutate({
            amount: extraCharge.amount,
            extra_revisions: Number(extraCharge.extra_revisions) || 1,
            note: extraCharge.note,
          })
        }
      >
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Extra charge amount *">
            <Input
              required
              inputMode="decimal"
              placeholder="25000"
              value={extraCharge.amount}
              onChange={(e) => setExtraCharge({ ...extraCharge, amount: e.target.value })}
            />
          </Field>
          <Field label="Extra revisions granted">
            <Input
              inputMode="numeric"
              value={extraCharge.extra_revisions}
              onChange={(e) => setExtraCharge({ ...extraCharge, extra_revisions: e.target.value })}
            />
          </Field>
        </div>
        <Field label="Note">
          <Input
            placeholder="Additional concept changes requested by client"
            value={extraCharge.note}
            onChange={(e) => setExtraCharge({ ...extraCharge, note: e.target.value })}
          />
        </Field>
      </FormDialog>
      <FilePreviewDialog
        fileId={previewFile?.id ?? null}
        fileName={previewFile?.name}
        onClose={() => setPreviewFile(null)}
      />

      <VersionDialog
        key={`v-${versioning}`}
        job={versioning ? job : null}
        onClose={() => setVersioning(false)}
      />
      <PrintDialog
        key={`p-${printing}`}
        job={printing ? job : null}
        onClose={() => setPrinting(false)}
      />
      <EditDialog
        key={`e-${editing}`}
        job={editing ? job : null}
        onClose={() => setEditing(false)}
      />
      <PaymentDialog
        open={paying}
        onOpenChange={setPaying}
        title="Add payment"
        hint="A deposit or the balance for this job. A receipt is issued."
        path={`/design-jobs/${id}/payments`}
        defaultAmount={toNumber(job.balance) > 0 ? job.balance : undefined}
        invalidate={refresh}
      />
      <CostDialog
        open={costing}
        onOpenChange={setCosting}
        target={{ design_job_id: id }}
        invalidate={refresh}
      />
    </div>
  );
}
