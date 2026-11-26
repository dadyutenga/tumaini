import { useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { Download, Eye, FolderOpen, Plus, Search, Upload } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  DataGrid,
  Field,
  FilterBar,
  FormDialog,
  NativeSelect,
  PageHeader,
  SectionCard,
  SegmentedChips,
  StatusBadge,
} from "@/components/shared/kit";
import { EmptyState } from "@/components/shared/Display";
import { FilePreviewDialog } from "@/components/shared/FilePreview";
import { useAuth } from "@/features/auth/auth";
import { ClientSelect } from "@/features/services/shared";
import { label } from "@/features/services/helpers";
import { useDebounce } from "@/hooks/useDebounce";
import { errorMessage, useGet, useWrite } from "@/hooks/useApi";
import { download, uploadFile } from "@/lib/api";
import { formatDate } from "@/lib/formatters";
import type { ClientDocument, ProjectRow } from "@/types/services";

const TYPES = ["required", "id", "photo", "certificate", "contract", "other"];

function AddDialog({
  open,
  onOpenChange,
  clientId,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  clientId: string;
}) {
  const [d, setD] = useState({ client_id: clientId, project_id: "", name: "", doc_type: "other" });
  const [fileId, setFileId] = useState<string | null>(null);
  const [fileName, setFileName] = useState("");
  const [uploading, setUploading] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  const projects = useGet<ProjectRow[]>(
    "/projects",
    { client_id: d.client_id, per_page: 100 },
    { enabled: !!d.client_id },
  );
  const add = useWrite("POST", "/client-documents", {
    invalidate: ["/client-documents", "/clients", "/projects"],
    success: "Document saved",
    onSuccess: () => {
      onOpenChange(false);
      setFileId(null);
      setFileName("");
      setD((x) => ({ ...x, name: "", project_id: "" }));
    },
  });
  return (
    <FormDialog
      open={open}
      onOpenChange={onOpenChange}
      title="Add a document"
      description="Photograph or upload the client's papers. Documents with a file are marked received."
      busy={add.isPending || uploading}
      onSubmit={() =>
        add.mutate({
          client_id: d.client_id,
          project_id: d.project_id || undefined,
          name: d.name,
          doc_type: d.doc_type,
          file_id: fileId ?? undefined,
        })
      }
    >
      <Field label="Client *">
        <ClientSelect
          required
          value={d.client_id}
          onChange={(v) => setD({ ...d, client_id: v, project_id: "" })}
        />
      </Field>
      <Field label="Project (optional)">
        <NativeSelect
          value={d.project_id}
          onChange={(e) => setD({ ...d, project_id: e.target.value })}
        >
          <option value="">Not tied to a project</option>
          {projects.data?.map((p) => (
            <option key={p.id} value={p.id}>
              {p.number} · {p.name}
            </option>
          ))}
        </NativeSelect>
      </Field>
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Document name *">
          <Input
            required
            placeholder="ID copy"
            value={d.name}
            onChange={(e) => setD({ ...d, name: e.target.value })}
          />
        </Field>
        <Field label="Type">
          <NativeSelect
            value={d.doc_type}
            onChange={(e) => setD({ ...d, doc_type: e.target.value })}
          >
            {TYPES.map((t) => (
              <option key={t} value={t}>
                {label(t)}
              </option>
            ))}
          </NativeSelect>
        </Field>
      </div>
      <Field label="File">
        <input
          ref={input}
          type="file"
          className="hidden"
          onChange={async (e) => {
            const f = e.target.files?.[0];
            e.target.value = "";
            if (!f) return;
            setUploading(true);
            try {
              setFileId(await uploadFile(f));
              setFileName(f.name);
              setD((x) => ({ ...x, name: x.name || f.name.replace(/\.[^.]+$/, "") }));
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
    </FormDialog>
  );
}

export default function ClientDocuments() {
  const { can } = useAuth();
  const [params] = useSearchParams();
  const clientFilter = params.get("client") ?? "";
  const [client, setClient] = useState(clientFilter);
  const [status, setStatus] = useState("");
  const [q, setQ] = useState("");
  const search = useDebounce(q, 300);
  const docs = useGet<ClientDocument[]>("/client-documents", {
    client_id: client,
    status,
    q: search,
    per_page: 100,
  });
  const [adding, setAdding] = useState(false);
  const [view, setView] = useState<"folders" | "table">("folders");
  const [preview, setPreview] = useState<{ id: string; name: string } | null>(null);
  const manage =
    can("services.document.manage") ||
    can("services.client.manage") ||
    can("services.project.manage");
  const mark = useWrite<{ id: string; status: string }>("PUT", (b) => `/client-documents/${b.id}`, {
    invalidate: ["/client-documents", "/projects", "/clients"],
    success: "Document updated",
    toBody: (b) => ({ status: b.status }),
  });

  const folders = useMemo(() => {
    const byClient = new Map<string, { name: string; projects: Map<string, ClientDocument[]> }>();
    for (const d of docs.data ?? []) {
      const cname = d.client_name ?? "Client";
      const ckey = d.client_id ?? cname;
      if (!byClient.has(ckey)) byClient.set(ckey, { name: cname, projects: new Map() });
      const pkey = d.project_number ? `Project ${d.project_number}` : "General documents";
      const grp = byClient.get(ckey)!.projects;
      if (!grp.has(pkey)) grp.set(pkey, []);
      grp.get(pkey)!.push(d);
    }
    return [...byClient.entries()];
  }, [docs.data]);

  return (
    <div className="space-y-6">
      <PageHeader
        title="Client documents"
        description="Client paperwork and final design deliverables organized by client and project."
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <SegmentedChips
              ariaLabel="View mode"
              value={view}
              onChange={setView}
              options={[
                { value: "folders", label: "Folders" },
                { value: "table", label: "Table" },
              ]}
            />
            {manage && (
              <Button size="sm" onClick={() => setAdding(true)}>
                <Plus size={14} /> Add document
              </Button>
            )}
          </div>
        }
      />

      <FilterBar className="justify-between">
        <div className="flex flex-1 flex-wrap items-center gap-2.5">
          <div className="relative min-w-60 flex-1 sm:max-w-xs">
            <Search
              size={15}
              className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-[var(--od-ink-muted)]"
            />
            <Input
              aria-label="Search documents"
              className="pl-9"
              placeholder="Search name or client…"
              value={q}
              onChange={(e) => setQ(e.target.value)}
            />
          </div>
          <div className="w-56">
            <ClientSelect value={client} onChange={setClient} />
          </div>
          {client && (
            <Button size="sm" variant="ghost" onClick={() => setClient("")}>
              Clear client
            </Button>
          )}
        </div>
        <SegmentedChips
          ariaLabel="Document status"
          value={status}
          onChange={setStatus}
          options={[
            { value: "", label: "All" },
            { value: "received", label: "Received" },
            { value: "missing", label: "Missing" },
          ]}
        />
      </FilterBar>

      {view === "folders" ? (
        <div className="space-y-4">
          {folders.length === 0 && !docs.isLoading && (
            <SectionCard>
              <EmptyState title="No documents match" />
            </SectionCard>
          )}
          {folders.map(([ckey, group]) => (
            <SectionCard key={ckey}>
              <h2 className="od-card-title flex items-center gap-2">
                <FolderOpen size={15} className="text-[var(--od-ink-secondary)]" />
                <span>{group.name}</span>
              </h2>
              <div className="space-y-3">
                {[...group.projects.entries()].map(([folderName, list]) => (
                  <div
                    key={folderName}
                    className="rounded-[var(--od-radius-md,12px)] border border-[var(--od-border)] bg-[var(--od-surface-subtle)] p-3.5"
                  >
                    <p className="mb-2 text-[11px] font-semibold uppercase tracking-[0.04em] text-[var(--od-ink-muted)]">
                      {folderName} ({list.length})
                    </p>
                    <ul className="divide-y divide-[var(--od-border)]">
                      {list.map((d) => (
                        <li
                          key={d.id}
                          className="flex flex-wrap items-center justify-between gap-2 py-2.5 text-[13px]"
                        >
                          <div className="min-w-0">
                            <p className="font-medium text-[var(--od-ink)]">{d.name}</p>
                            <p className="od-caption">
                              {label(d.doc_type)}
                              {d.received_at ? ` · Received ${formatDate(d.received_at)}` : ""}
                            </p>
                          </div>
                          <div className="flex flex-wrap items-center gap-1.5">
                            <StatusBadge value={d.status} />
                            {d.file_id && (
                              <>
                                <Button
                                  size="sm"
                                  variant="outline"
                                  onClick={() =>
                                    setPreview({ id: d.file_id!, name: d.file_name ?? d.name })
                                  }
                                >
                                  <Eye size={13} /> Preview
                                </Button>
                                <Button
                                  size="sm"
                                  variant="outline"
                                  onClick={() =>
                                    download(
                                      `/files/${d.file_id}`,
                                      {},
                                      d.file_name ?? d.name,
                                    ).catch((e) => toast.error(errorMessage(e)))
                                  }
                                >
                                  <Download size={13} /> Download
                                </Button>
                              </>
                            )}
                            {manage && (
                              <Button
                                size="sm"
                                variant="ghost"
                                onClick={() =>
                                  mark.mutate({
                                    id: d.id,
                                    status: d.status === "received" ? "missing" : "received",
                                  })
                                }
                              >
                                {d.status === "received" ? "Mark missing" : "Mark received"}
                              </Button>
                            )}
                          </div>
                        </li>
                      ))}
                    </ul>
                  </div>
                ))}
              </div>
            </SectionCard>
          ))}
        </div>
      ) : (
        <DataGrid
          rows={docs.data}
          loading={docs.isLoading}
          rowKey={(d) => d.id}
          empty="No documents match"
          columns={[
            { header: "Document", cell: (d) => <span className="font-medium">{d.name}</span> },
            { header: "Client", cell: (d) => d.client_name },
            {
              header: "Project",
              cell: (d) => (
                <span className="od-mono text-xs text-[var(--od-ink-secondary)]">
                  {d.project_number ?? "—"}
                </span>
              ),
            },
            { header: "Type", cell: (d) => label(d.doc_type) },
            { header: "Status", cell: (d) => <StatusBadge value={d.status} /> },
            { header: "Received", cell: (d) => (d.received_at ? formatDate(d.received_at) : "—") },
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
                        onClick={() => setPreview({ id: d.file_id!, name: d.file_name ?? d.name })}
                      >
                        <Eye size={13} /> Preview
                      </Button>
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() =>
                          download(`/files/${d.file_id}`, {}, d.file_name ?? d.name).catch((e) =>
                            toast.error(errorMessage(e)),
                          )
                        }
                      >
                        <Download size={13} /> Download
                      </Button>
                    </>
                  )}
                  {manage && (
                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={() =>
                        mark.mutate({
                          id: d.id,
                          status: d.status === "received" ? "missing" : "received",
                        })
                      }
                    >
                      {d.status === "received" ? "Mark missing" : "Mark received"}
                    </Button>
                  )}
                </div>
              ),
            },
          ]}
        />
      )}

      <AddDialog key={client} open={adding} onOpenChange={setAdding} clientId={client} />
      <FilePreviewDialog
        fileId={preview?.id ?? null}
        fileName={preview?.name}
        onClose={() => setPreview(null)}
      />
    </div>
  );
}
