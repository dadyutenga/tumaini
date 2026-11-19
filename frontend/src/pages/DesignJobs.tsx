import { useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Plus, Paperclip } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
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
import { useAuth } from "@/features/auth/auth";
import { ClientSelect, StaffSelect } from "@/features/services/shared";
import { daysUntil, label, useStaff } from "@/features/services/helpers";
import { errorMessage, useGet, useWrite } from "@/hooks/useApi";
import { uploadFile } from "@/lib/api";
import { formatDate, formatMoney } from "@/lib/formatters";
import { cn } from "@/lib/utils";
import {
  DESIGN_NEXT,
  DESIGN_STATUSES,
  JOB_TYPES,
  type CatalogItem,
  type DesignJobRow,
  type DesignStatus,
  type ProjectRow,
} from "@/types/services";

/** Card edge colour by workflow step: grey, yellow, blue, red, green. */
const EDGE: Record<string, string> = {
  new_brief: "border-l-muted-foreground/40",
  in_design: "border-l-amber-500",
  internal_review: "border-l-amber-500",
  with_client: "border-l-sky-500",
  changes_requested: "border-l-red-500",
  approved: "border-l-emerald-500",
  printing: "border-l-emerald-500",
  delivered: "border-l-emerald-500",
};

function JobCard({ job, onOpen }: { job: DesignJobRow; onOpen: () => void }) {
  const left = daysUntil(job.deadline);
  const late = job.overdue || (left !== null && left < 0);
  return (
    <button
      type="button"
      draggable
      onDragStart={(e) => e.dataTransfer.setData("text/plain", job.id)}
      onClick={onOpen}
      className={cn(
        "w-full rounded-xl border border-l-4 bg-card p-3 text-left text-sm shadow-sm transition hover:border-primary",
        EDGE[job.status],
      )}
    >
      <p className="font-medium">{job.title}</p>
      <p className="text-xs text-muted-foreground">{job.client_name}</p>
      <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
        <span className="text-muted-foreground">{job.designer_name ?? "Unassigned"}</span>
        {job.deadline && (
          <span className={late ? "font-medium text-destructive" : "text-muted-foreground"}>
            {late ? "Late " : "Due "}
            {formatDate(job.deadline)}
          </span>
        )}
        <span
          className={job.revisions_over ? "font-medium text-destructive" : "text-muted-foreground"}
        >
          Rev {job.revisions_used}/{job.revisions_included}
        </span>
        {job.priority === "urgent" && <StatusBadge value="urgent" />}
      </div>
      {job.status === "approved" && job.printing_needed && (
        <p className="mt-1 text-xs text-emerald-700 dark:text-emerald-400">Ready to print</p>
      )}
    </button>
  );
}

function Board({ designer }: { designer: string }) {
  const navigate = useNavigate();
  const { can } = useAuth();
  const board = useGet<{ status: string; jobs: DesignJobRow[] }[]>("/design-jobs/board", {
    designer_id: designer,
  });
  const [over, setOver] = useState<string | null>(null);
  const move = useWrite<{ id: string; status: string }>(
    "POST",
    (b) => `/design-jobs/${b.id}/status`,
    {
      invalidate: ["/design-jobs", "/services"],
      toBody: (b) => ({ status: b.status }),
    },
  );

  if (board.isLoading) return <PageLoader />;
  const jobs = board.data?.flatMap((c) => c.jobs) ?? [];

  function drop(status: string, id: string) {
    setOver(null);
    const job = jobs.find((j) => j.id === id);
    if (!job || job.status === status) return;
    if (!DESIGN_NEXT[job.status]?.includes(status as DesignStatus)) {
      toast.error(`A job in ${label(job.status)} cannot go straight to ${label(status)}.`);
      return;
    }
    move.mutate({ id, status });
  }

  return (
    <div className="flex gap-3 overflow-x-auto pb-3">
      {board.data?.map((col) => (
        <section
          key={col.status}
          onDragOver={(e) => {
            if (can("design.manage")) {
              e.preventDefault();
              setOver(col.status);
            }
          }}
          onDragLeave={() => setOver((o) => (o === col.status ? null : o))}
          onDrop={(e) => {
            e.preventDefault();
            drop(col.status, e.dataTransfer.getData("text/plain"));
          }}
          className={cn(
            "w-64 shrink-0 rounded-2xl border bg-muted/30 p-3",
            over === col.status && "ring-2 ring-primary",
          )}
        >
          <h2 className="mb-3 flex items-center justify-between text-sm font-semibold">
            {label(col.status)}
            <span className="rounded-full bg-background px-2 py-0.5 text-xs text-muted-foreground">
              {col.jobs.length}
            </span>
          </h2>
          <div className="space-y-2">
            {col.jobs.map((j) => (
              <JobCard key={j.id} job={j} onOpen={() => navigate(`/design-jobs/${j.id}`)} />
            ))}
          </div>
        </section>
      ))}
    </div>
  );
}

const blank = {
  client_id: "",
  project_id: "",
  service_id: "",
  job_type: "logo",
  title: "",
  brief: "",
  size_format: "",
  style_notes: "",
  text_to_include: "",
  deadline: "",
  priority: "normal",
  designer_id: "",
  price: "",
  deposit: "0",
  revisions_included: "",
  printing_needed: false,
};

function NewJobDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const navigate = useNavigate();
  const [d, setD] = useState(blank);
  const [refs, setRefs] = useState<{ id: string; name: string }[]>([]);
  const fileInput = useRef<HTMLInputElement>(null);
  const catalog = useGet<CatalogItem[]>("/service-catalog", { category: "design", per_page: 100 });
  const projects = useGet<ProjectRow[]>(
    "/projects",
    { client_id: d.client_id, per_page: 100 },
    { enabled: !!d.client_id },
  );
  const staff = useStaff();
  const create = useWrite<unknown, DesignJobRow>("POST", "/design-jobs", {
    invalidate: ["/design-jobs", "/services", "/projects"],
    success: "Design job created",
    onSuccess: (job) => {
      onOpenChange(false);
      setD(blank);
      setRefs([]);
      navigate(`/design-jobs/${job.id}`);
    },
  });
  const set = <K extends keyof typeof blank>(k: K, v: (typeof blank)[K]) =>
    setD((x) => ({ ...x, [k]: v }));
  const service = catalog.data?.find((s) => s.id === d.service_id);

  async function addFiles(files: FileList | null) {
    if (!files) return;
    for (const f of Array.from(files)) {
      try {
        const id = await uploadFile(f);
        setRefs((r) => [...r, { id, name: f.name }]);
      } catch (error) {
        toast.error(errorMessage(error));
      }
    }
  }

  return (
    <FormDialog
      open={open}
      onOpenChange={onOpenChange}
      title="New design job"
      description="Price and included revisions come from the catalog and can be changed."
      busy={create.isPending}
      onSubmit={() =>
        create.mutate({
          client_id: d.client_id,
          project_id: d.project_id || undefined,
          service_id: d.service_id || undefined,
          job_type: d.job_type,
          title: d.title,
          brief: d.brief,
          size_format: d.size_format,
          style_notes: d.style_notes,
          text_to_include: d.text_to_include,
          deadline: d.deadline || undefined,
          priority: d.priority,
          designer_id: d.designer_id || undefined,
          price: d.price || undefined,
          deposit: d.deposit || "0",
          revisions_included:
            d.revisions_included === "" ? undefined : Number(d.revisions_included),
          printing_needed: d.printing_needed,
          reference_file_ids: refs.map((r) => r.id),
        })
      }
      wide
    >
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Client *">
          <ClientSelect
            required
            value={d.client_id}
            onChange={(v) => setD((x) => ({ ...x, client_id: v, project_id: "" }))}
          />
        </Field>
        <Field label="Project (optional)">
          <NativeSelect value={d.project_id} onChange={(e) => set("project_id", e.target.value)}>
            <option value="">Stand-alone job</option>
            {projects.data?.map((p) => (
              <option key={p.id} value={p.id}>
                {p.number} · {p.name}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field label="Job type *">
          <NativeSelect value={d.job_type} onChange={(e) => set("job_type", e.target.value)}>
            {JOB_TYPES.map((t) => (
              <option key={t} value={t}>
                {label(t)}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field label="Price list item">
          <NativeSelect value={d.service_id} onChange={(e) => set("service_id", e.target.value)}>
            <option value="">None. Enter the price below</option>
            {catalog.data?.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name} · {formatMoney(s.standard_price)}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field label="Title *" className="sm:col-span-2">
          <Input
            required
            placeholder="Logo for Client Business"
            value={d.title}
            onChange={(e) => set("title", e.target.value)}
          />
        </Field>
        <Field label="Brief" className="sm:col-span-2">
          <Textarea
            rows={3}
            placeholder="What the client wants, in their own words"
            value={d.brief}
            onChange={(e) => set("brief", e.target.value)}
          />
        </Field>
        <Field label="Size and format">
          <Input
            placeholder="A4 print, business card, banner 2x1m…"
            value={d.size_format}
            onChange={(e) => set("size_format", e.target.value)}
          />
        </Field>
        <Field label="Style notes">
          <Input
            placeholder="Colors, mood, fonts, examples"
            value={d.style_notes}
            onChange={(e) => set("style_notes", e.target.value)}
          />
        </Field>
        <Field label="Text to include" className="sm:col-span-2">
          <Input
            placeholder="Business name, slogan, phone, address, social handles"
            value={d.text_to_include}
            onChange={(e) => set("text_to_include", e.target.value)}
          />
        </Field>
        <Field label="Deadline">
          <Input type="date" value={d.deadline} onChange={(e) => set("deadline", e.target.value)} />
        </Field>
        <Field label="Priority">
          <NativeSelect value={d.priority} onChange={(e) => set("priority", e.target.value)}>
            <option value="normal">Normal</option>
            <option value="urgent">Urgent</option>
          </NativeSelect>
        </Field>
        {staff.length > 0 && (
          <Field label="Designer">
            <StaffSelect value={d.designer_id} onChange={(v) => set("designer_id", v)} />
          </Field>
        )}
        <Field label="Revisions included">
          <Input
            inputMode="numeric"
            placeholder={service ? String(service.included_revisions) : "3"}
            value={d.revisions_included}
            onChange={(e) => set("revisions_included", e.target.value)}
          />
        </Field>
        <Field label="Price">
          <Input
            inputMode="decimal"
            placeholder={service?.standard_price}
            value={d.price}
            onChange={(e) => set("price", e.target.value)}
          />
        </Field>
        <Field label="Deposit">
          <Input
            inputMode="decimal"
            value={d.deposit}
            onChange={(e) => set("deposit", e.target.value)}
          />
        </Field>
      </div>
      <label className="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          checked={d.printing_needed}
          onChange={(e) => set("printing_needed", e.target.checked)}
        />
        Printing needed (a print order is created after approval)
      </label>
      <Field label="Reference files">
        <input
          ref={fileInput}
          type="file"
          multiple
          className="hidden"
          onChange={(e) => void addFiles(e.target.files)}
        />
        <div className="flex flex-wrap items-center gap-2">
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => fileInput.current?.click()}
          >
            <Paperclip size={14} /> Add files
          </Button>
          {refs.map((r) => (
            <span key={r.id} className="rounded-full bg-muted px-2 py-0.5 text-xs">
              {r.name}
            </span>
          ))}
        </div>
      </Field>
    </FormDialog>
  );
}

export default function DesignJobs() {
  const { can } = useAuth();
  const navigate = useNavigate();
  const [designer, setDesigner] = useState("");
  const [status, setStatus] = useState("");
  const [overdue, setOverdue] = useState(false);
  const [q, setQ] = useState("");
  const [creating, setCreating] = useState(false);
  const list = useGet<DesignJobRow[]>("/design-jobs", {
    q,
    status,
    designer_id: designer,
    overdue: overdue || undefined,
    per_page: 100,
  });
  const staff = useStaff();

  return (
    <>
      <PageHeader
        title="Design jobs"
        description="Logos, cards, posters, banners and branding, from brief to delivery."
        actions={
          can("design.manage") && (
            <Button onClick={() => setCreating(true)}>
              <Plus size={16} /> New job
            </Button>
          )
        }
      />
      <Tabs defaultValue="board">
        <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
          <TabsList>
            <TabsTrigger value="board">Board</TabsTrigger>
            <TabsTrigger value="list">List</TabsTrigger>
          </TabsList>
          {staff.length > 0 && (
            <NativeSelect
              aria-label="Designer"
              className="w-48"
              value={designer}
              onChange={(e) => setDesigner(e.target.value)}
            >
              <option value="">All designers</option>
              {staff.map((u) => (
                <option key={u.id} value={u.id}>
                  {u.name}
                </option>
              ))}
            </NativeSelect>
          )}
        </div>
        <TabsContent value="board">
          <Board designer={designer} />
        </TabsContent>
        <TabsContent value="list">
          <div className="mb-4 flex flex-wrap items-center gap-3">
            <Input
              aria-label="Search"
              className="max-w-xs"
              placeholder="Search title or client…"
              value={q}
              onChange={(e) => setQ(e.target.value)}
            />
            <NativeSelect
              aria-label="Status"
              className="w-44"
              value={status}
              onChange={(e) => setStatus(e.target.value)}
            >
              <option value="">All steps</option>
              {[...DESIGN_STATUSES, "cancelled"].map((s) => (
                <option key={s} value={s}>
                  {label(s)}
                </option>
              ))}
            </NativeSelect>
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={overdue}
                onChange={(e) => setOverdue(e.target.checked)}
              />
              Late only
            </label>
          </div>
          <DataGrid
            rows={list.data}
            loading={list.isLoading}
            rowKey={(j) => j.id}
            empty="No design jobs yet"
            onRowClick={(j) => navigate(`/design-jobs/${j.id}`)}
            columns={[
              { header: "Number", cell: (j) => j.number },
              {
                header: "Job",
                cell: (j) => (
                  <div>
                    <p className="font-medium">{j.title}</p>
                    <p className="text-xs text-muted-foreground">{j.client_name}</p>
                  </div>
                ),
              },
              { header: "Type", cell: (j) => label(j.job_type) },
              { header: "Designer", cell: (j) => j.designer_name ?? "-" },
              { header: "Due", cell: (j) => (j.deadline ? formatDate(j.deadline) : "-") },
              {
                header: "Status",
                cell: (j) => <StatusBadge value={j.overdue ? "overdue" : j.status} />,
              },
              { header: "Price", align: "right", cell: (j) => formatMoney(j.price) },
              { header: "Balance", align: "right", cell: (j) => formatMoney(j.balance) },
            ]}
          />
        </TabsContent>
      </Tabs>
      <NewJobDialog open={creating} onOpenChange={setCreating} />
    </>
  );
}
