import { useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { AlertCircle, Paperclip, Plus, Search } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  DataGrid,
  Field,
  FilterBar,
  FormDialog,
  NativeSelect,
  PageHeader,
  SegmentedChips,
  StatusBadge,
} from "@/components/shared/kit";
import { PageLoader, StatCard } from "@/components/shared/Display";
import { useAuth } from "@/features/auth/auth";
import { ClientSelect, StaffSelect } from "@/features/services/shared";
import { daysUntil, label, useStaff } from "@/features/services/helpers";
import { errorMessage, useGet, useWrite } from "@/hooks/useApi";
import { uploadFile } from "@/lib/api";
import { formatDate, formatMoney, toNumber } from "@/lib/formatters";
import { cn } from "@/lib/utils";
import {
  DESIGN_NEXT,
  DESIGN_STATUSES,
  JOB_TYPES,
  type CatalogItem,
  type DesignJobRow,
  type DesignStatus,
  type PrintOrderRow,
  type ProjectRow,
} from "@/types/services";
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";
import { useSearchBox } from "@/hooks/useSearchBox";

/** Card edge colour using OpenDashboards semantic marks. */
const EDGE: Record<string, string> = {
  new_brief: "border-l-[var(--od-muted-mark)]",
  in_design: "border-l-[var(--od-warning-mark)]",
  internal_review: "border-l-[var(--od-warning-mark)]",
  with_client: "border-l-[var(--od-info)]",
  changes_requested: "border-l-[var(--od-bad-mark)]",
  approved: "border-l-[var(--od-good-mark)]",
  printing: "border-l-[var(--od-good-mark)]",
  delivered: "border-l-[var(--od-good-mark)]",
};

function JobCard({ job, onOpen }: { job: DesignJobRow; onOpen: () => void }) {
  const { t } = useI18n();
  const left = daysUntil(job.deadline);
  const late = job.overdue || (left !== null && left < 0);
  return (
    <button
      type="button"
      draggable
      onDragStart={(e) => e.dataTransfer.setData("text/plain", job.id)}
      onClick={onOpen}
      className={cn(
        "od-card-flush w-full cursor-pointer border-l-[3px] p-3.5 text-left text-[13px] transition-colors hover:border-[var(--od-ink-muted)]",
        EDGE[job.status],
      )}
    >
      <p className="font-semibold text-[var(--od-ink)]">{job.title}</p>
      <p className="od-caption mt-0.5">{job.client_name}</p>
      <div className="mt-2.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
        <span className="text-[var(--od-ink-secondary)]">
          {job.designer_name ?? t.common.unassigned}
        </span>
        {job.deadline && (
          <span
            className={late ? "font-semibold text-[var(--od-bad)]" : "text-[var(--od-ink-muted)]"}
          >
            {late ? `${t.common.late} ` : `${t.common.due2} `}
            {formatDate(job.deadline)}
          </span>
        )}
        <span
          className={
            job.revisions_over
              ? "od-tabular font-semibold text-[var(--od-bad)]"
              : "od-tabular text-[var(--od-ink-muted)]"
          }
        >
          {fmt(t.design.rev, {
            revisions_used: job.revisions_used,
            revisions_included: job.revisions_included,
          })}
        </span>
        {job.priority === "urgent" && <StatusBadge value="urgent" />}
      </div>
      {job.status === "approved" && job.printing_needed && (
        <p className="mt-1.5 text-xs font-medium text-[var(--od-good)]">{t.design.readyToPrint}</p>
      )}
    </button>
  );
}

function Board({ designer }: { designer: string }) {
  const { t } = useI18n();
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
      toast.error(
        fmt(t.design.aJobInCannotGo, { status: label(job.status), status2: label(status) }),
      );
      return;
    }
    move.mutate({ id, status });
  }

  return (
    <div className="scrollbar-thin flex gap-3.5 overflow-x-auto pb-3">
      {board.data?.map((col) => (
        <section
          key={col.status}
          onDragOver={(e) => {
            if (can("services.design.manage")) {
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
            "w-64 shrink-0 rounded-[var(--od-radius,16px)] border border-[var(--od-border)] bg-[var(--od-surface-subtle)] p-3 transition-shadow",
            over === col.status && "ring-2 ring-[var(--od-ink)]",
          )}
        >
          <h2 className="mb-3 flex items-center justify-between px-1 text-[13px] font-semibold text-[var(--od-ink)]">
            <span>{label(col.status)}</span>
            <span className="od-tabular rounded-full bg-[var(--od-surface)] px-2 py-0.5 text-[11px] font-medium text-[var(--od-ink-secondary)] ring-1 ring-[var(--od-border-strong)]">
              {col.jobs.length}
            </span>
          </h2>
          <div className="space-y-2.5">
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
  const { t } = useI18n();
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
    success: t.design.designJobCreated,
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
      title={t.common.newDesignJob}
      description={t.design.priceAndIncludedRevisionsCome}
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
        <Field label={t.common.client2}>
          <ClientSelect
            required
            value={d.client_id}
            onChange={(v) => setD((x) => ({ ...x, client_id: v, project_id: "" }))}
          />
        </Field>
        <Field label={t.common.projectOptional}>
          <NativeSelect value={d.project_id} onChange={(e) => set("project_id", e.target.value)}>
            <option value="">{t.design.standAloneJob}</option>
            {projects.data?.map((p) => (
              <option key={p.id} value={p.id}>
                {p.number} · {p.name}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field label={t.design.jobType}>
          <NativeSelect value={d.job_type} onChange={(e) => set("job_type", e.target.value)}>
            {JOB_TYPES.map((item) => (
              <option key={item} value={item}>
                {label(item)}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field label={t.design.priceListItem}>
          <NativeSelect value={d.service_id} onChange={(e) => set("service_id", e.target.value)}>
            <option value="">{t.design.noneEnterThePriceBelow}</option>
            {catalog.data?.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name} · {formatMoney(s.standard_price)}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field label={t.design.title} className="sm:col-span-2">
          <Input
            required
            placeholder={t.design.logoForClientBusiness}
            value={d.title}
            onChange={(e) => set("title", e.target.value)}
          />
        </Field>
        <Field label={t.design.brief} className="sm:col-span-2">
          <Textarea
            rows={3}
            placeholder={t.design.whatTheClientWantsIn}
            value={d.brief}
            onChange={(e) => set("brief", e.target.value)}
          />
        </Field>
        <Field label={t.design.sizeAndFormat}>
          <Input
            placeholder={t.design.a4PrintBusinessCardBanner}
            value={d.size_format}
            onChange={(e) => set("size_format", e.target.value)}
          />
        </Field>
        <Field label={t.design.styleNotes}>
          <Input
            placeholder={t.design.colorsMoodFontsExamples}
            value={d.style_notes}
            onChange={(e) => set("style_notes", e.target.value)}
          />
        </Field>
        <Field label={t.design.textToInclude} className="sm:col-span-2">
          <Input
            placeholder={t.design.businessNameSloganPhoneAddress}
            value={d.text_to_include}
            onChange={(e) => set("text_to_include", e.target.value)}
          />
        </Field>
        <Field label={t.common.deadline}>
          <Input type="date" value={d.deadline} onChange={(e) => set("deadline", e.target.value)} />
        </Field>
        <Field label={t.common.priority}>
          <NativeSelect value={d.priority} onChange={(e) => set("priority", e.target.value)}>
            <option value="normal">{t.common.normal}</option>
            <option value="urgent">{t.common.urgent}</option>
          </NativeSelect>
        </Field>
        {staff.length > 0 && (
          <Field label={t.common.designer}>
            <StaffSelect value={d.designer_id} onChange={(v) => set("designer_id", v)} />
          </Field>
        )}
        <Field label={t.design.revisionsIncluded}>
          <Input
            inputMode="numeric"
            placeholder={service ? String(service.included_revisions) : "3"}
            value={d.revisions_included}
            onChange={(e) => set("revisions_included", e.target.value)}
          />
        </Field>
        <Field label={t.common.price}>
          <Input
            inputMode="decimal"
            placeholder={service?.standard_price}
            value={d.price}
            onChange={(e) => set("price", e.target.value)}
          />
        </Field>
        <Field label={t.design.deposit}>
          <Input
            inputMode="decimal"
            value={d.deposit}
            onChange={(e) => set("deposit", e.target.value)}
          />
        </Field>
      </div>
      <label className="flex items-center gap-2 text-[13px] text-[var(--od-ink-secondary)]">
        <input
          type="checkbox"
          checked={d.printing_needed}
          onChange={(e) => set("printing_needed", e.target.checked)}
        />
        {t.design.printingNeededAPrintOrder}
      </label>
      <Field label={t.design.referenceFiles}>
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
            <Paperclip size={13} /> {t.design.addFiles}
          </Button>
          {refs.map((r) => (
            <span
              key={r.id}
              className="rounded-full bg-[var(--od-neutral-soft)] px-2.5 py-0.5 text-xs text-[var(--od-ink-secondary)]"
            >
              {r.name}
            </span>
          ))}
        </div>
      </Field>
    </FormDialog>
  );
}

export default function DesignJobs() {
  const { t } = useI18n();
  const { can, user } = useAuth();
  const navigate = useNavigate();
  const [designer, setDesigner] = useState("");
  const [status, setStatus] = useState("");
  const [printStatusFilter, setPrintStatusFilter] = useState("");
  const [overdue, setOverdue] = useState(false);
  const [q, setQ] = useSearchBox();
  const [creating, setCreating] = useState(false);
  const list = useGet<DesignJobRow[]>("/design-jobs", {
    q,
    status,
    designer_id: designer,
    overdue: overdue || undefined,
    per_page: 100,
  });
  const printOrders = useGet<PrintOrderRow[]>("/print-orders", {
    status: printStatusFilter,
    per_page: 100,
  });
  const updatePrint = useWrite<{ id: string; status: string }>(
    "PUT",
    (b) => `/print-orders/${b.id}`,
    {
      invalidate: ["/print-orders", "/design-jobs", "/service-costs", "/contractors"],
      success: t.design.printOrderUpdated,
      toBody: (b) => ({ status: b.status }),
    },
  );
  const staff = useStaff();
  const manage = can("services.design.manage");
  const myJobs = (list.data ?? []).filter((j) => j.designer_id === user?.id);
  const myDueToday = myJobs.filter((j) => {
    const d = daysUntil(j.deadline);
    return d !== null && d <= 0 && !["delivered", "cancelled"].includes(j.status);
  }).length;
  const myWithClient = myJobs.filter((j) => j.status === "with_client").length;
  const myInDesign = myJobs.filter((j) => j.status === "in_design").length;

  return (
    <div className="space-y-6">
      <PageHeader
        title={t.common.designJobs}
        description={t.design.logosCardsPostersBannersAnd}
        actions={
          <div className="flex flex-wrap gap-2">
            {user?.id && (
              <Button
                size="sm"
                variant={designer === user.id ? "default" : "outline"}
                onClick={() => setDesigner((d) => (d === user.id ? "" : user.id))}
              >
                {fmt(t.design.myJobs, { value: myJobs.length ? ` (${myJobs.length})` : "" })}
              </Button>
            )}
            {manage && (
              <Button size="sm" onClick={() => setCreating(true)}>
                <Plus size={14} /> {t.common.newJob}
              </Button>
            )}
          </div>
        }
      />

      {user?.id && myJobs.length > 0 && (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <StatCard label={t.design.assignedToMe} value={myJobs.length} />
          <StatCard
            label={t.design.dueTodayOverdue}
            value={myDueToday}
            tone={myDueToday > 0 ? "bad" : "neutral"}
          />
          <StatCard label={t.common.waitingForClient} value={myWithClient} />
          <StatCard label={t.design.inDesign} value={myInDesign} />
        </div>
      )}

      <Tabs defaultValue="board">
        <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
          <TabsList>
            <TabsTrigger value="board">{t.common.board}</TabsTrigger>
            <TabsTrigger value="list">{t.common.list}</TabsTrigger>
            <TabsTrigger value="print">{t.design.printOrders}</TabsTrigger>
          </TabsList>
          {staff.length > 0 && (
            <NativeSelect
              aria-label={t.common.designer}
              className="w-48"
              value={designer}
              onChange={(e) => setDesigner(e.target.value)}
            >
              <option value="">{t.design.allDesigners}</option>
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

        <TabsContent value="list" className="space-y-4">
          <FilterBar>
            <div className="relative min-w-60 flex-1 sm:max-w-xs">
              <Search
                size={15}
                className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-[var(--od-ink-muted)]"
              />
              <Input
                aria-label={t.common.search}
                className="pl-9"
                placeholder={t.design.searchTitleOrClient}
                value={q}
                onChange={(e) => setQ(e.target.value)}
              />
            </div>
            <NativeSelect
              aria-label={t.common.status}
              className="w-44"
              value={status}
              onChange={(e) => setStatus(e.target.value)}
            >
              <option value="">{t.design.allSteps}</option>
              {[...DESIGN_STATUSES, "cancelled"].map((s) => (
                <option key={s} value={s}>
                  {label(s)}
                </option>
              ))}
            </NativeSelect>
            <button
              type="button"
              onClick={() => setOverdue((v) => !v)}
              className={cn(
                "inline-flex h-9 cursor-pointer items-center gap-1.5 rounded-[var(--od-app-radius,10px)] border px-3 text-[13px] font-medium transition-colors",
                overdue
                  ? "border-transparent bg-[color-mix(in_srgb,var(--od-bad-mark)_12%,transparent)] text-[var(--od-bad)]"
                  : "border-[var(--od-border-strong)] bg-[var(--od-surface)] text-[var(--od-ink-secondary)] hover:text-[var(--od-ink)]",
              )}
            >
              <AlertCircle size={14} /> {t.common.lateOnly}
            </button>
          </FilterBar>

          <DataGrid
            rows={list.data}
            loading={list.isLoading}
            rowKey={(j) => j.id}
            empty={t.design.noDesignJobsYet}
            onRowClick={(j) => navigate(`/design-jobs/${j.id}`)}
            columns={[
              {
                header: t.common.number,
                sortValue: (j) => j.number,
                cell: (j) => <span className="od-mono font-medium">{j.number}</span>,
              },
              {
                header: t.common.job,
                sortValue: (j) => j.title,
                cell: (j) => (
                  <div>
                    <p className="font-medium">{j.title}</p>
                    <p className="od-caption">{j.client_name}</p>
                  </div>
                ),
              },
              { header: t.common.type, cell: (j) => label(j.job_type) },
              { header: t.common.designer, cell: (j) => j.designer_name ?? "—" },
              { header: t.common.due2, cell: (j) => (j.deadline ? formatDate(j.deadline) : "—") },
              {
                header: t.common.status,
                cell: (j) => <StatusBadge value={j.overdue ? "overdue" : j.status} />,
              },
              {
                header: t.common.price,
                align: "right",
                sortValue: (j) => toNumber(j.price),
                cell: (j) => formatMoney(j.price),
              },
              {
                header: t.common.balance,
                align: "right",
                sortValue: (j) => toNumber(j.balance),
                cell: (j) => (
                  <span
                    className={
                      toNumber(j.balance) > 0 ? "font-semibold text-[var(--od-bad)]" : undefined
                    }
                  >
                    {formatMoney(j.balance)}
                  </span>
                ),
              },
            ]}
          />
        </TabsContent>

        <TabsContent value="print" className="space-y-4">
          <FilterBar>
            <SegmentedChips
              ariaLabel={t.design.printOrderStatus}
              value={printStatusFilter}
              onChange={setPrintStatusFilter}
              options={[
                { value: "", label: t.common.all },
                { value: "sent", label: t.design.sentToPrint },
                { value: "ready", label: t.design.ready },
                { value: "delivered", label: t.design.delivered },
                { value: "cancelled", label: t.common.cancelled },
              ]}
            />
          </FilterBar>
          <DataGrid
            rows={printOrders.data}
            loading={printOrders.isLoading}
            rowKey={(o) => o.id}
            empty={t.design.noPrintOrdersYet}
            onRowClick={(o) => navigate(`/design-jobs/${o.job_id}`)}
            columns={[
              {
                header: t.common.designJob,
                cell: (o) => (
                  <div>
                    <p className="font-medium">{o.job_title}</p>
                    <p className="od-caption">
                      <span className="od-mono">{o.job_number}</span> · {o.client_name}
                    </p>
                  </div>
                ),
              },
              { header: t.design.printer, cell: (o) => o.contractor_name ?? "—" },
              { header: t.common.qty, align: "right", cell: (o) => o.quantity },
              { header: t.common.due2, cell: (o) => (o.due_date ? formatDate(o.due_date) : "—") },
              { header: t.common.status, cell: (o) => <StatusBadge value={o.status} /> },
              { header: t.design.cost, align: "right", cell: (o) => formatMoney(o.cost) },
              {
                header: "",
                className: "text-right",
                cell: (o) =>
                  manage && (o.status === "sent" || o.status === "ready") ? (
                    <div className="flex justify-end gap-1.5" onClick={(e) => e.stopPropagation()}>
                      {o.status === "sent" && (
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => updatePrint.mutate({ id: o.id, status: "ready" })}
                        >
                          {t.design.ready}
                        </Button>
                      )}
                      {o.status === "ready" && (
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => updatePrint.mutate({ id: o.id, status: "delivered" })}
                        >
                          {t.design.delivered}
                        </Button>
                      )}
                      <Button
                        size="sm"
                        variant="ghost"
                        className="text-[var(--od-bad)] hover:text-[var(--od-bad)]"
                        onClick={() => updatePrint.mutate({ id: o.id, status: "cancelled" })}
                      >
                        {t.common.cancel}
                      </Button>
                    </div>
                  ) : null,
              },
            ]}
          />
        </TabsContent>
      </Tabs>

      <NewJobDialog open={creating} onOpenChange={setCreating} />
    </div>
  );
}
