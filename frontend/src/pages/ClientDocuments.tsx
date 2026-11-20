import { useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { Download, Eye, FolderOpen, List, Plus, Upload } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  DataGrid,
  Field,
  FormDialog,
  NativeSelect,
  PageHeader,
  StatusBadge,
} from "@/components/shared/kit";
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
          <span className="text-sm text-muted-foreground">{fileName}</span>
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
    <>
      <PageHeader
        title="Client documents"
        description="Every document, by client and project. Final design files are kept here too."
        actions={
          <div className="flex gap-2">
            <div className="inline-flex rounded-lg border bg-card p-0.5">
              <Button
                size="sm"
                variant={view === "folders" ? "default" : "ghost"}
                onClick={() => setView("folders")}
              >
                <FolderOpen size={14} /> Folders
              </Button>
              <Button
                size="sm"
                variant={view === "table" ? "default" : "ghost"}
                onClick={() => setView("table")}
              >
                <List size={14} /> Table
              </Button>
            </div>
            {manage && (
              <Button onClick={() => setAdding(true)}>
                <Plus size={16} /> Add document
              </Button>
            )}
          </div>
        }
      />
      <div className="mb-4 flex flex-wrap gap-3">
        <Input
          aria-label="Search documents"
          className="max-w-xs"
          placeholder="Search name or client…"
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
        <div className="w-60">
          <ClientSelect value={client} onChange={setClient} />
        </div>
        <NativeSelect
          aria-label="Status"
          className="w-40"
          value={status}
          onChange={(e) => setStatus(e.target.value)}
        >
          <option value="">All</option>
          <option value="received">Received</option>
          <option value="missing">Missing</option>
        </NativeSelect>
        {client && (
          <Button variant="ghost" onClick={() => setClient("")}>
            Clear client
          </Button>
        )}
      </div>
      {view === "folders" ? (
        <div className="space-y-4">
          {folders.length === 0 && !docs.isLoading && (
            <p className="rounded-2xl border bg-card p-8 text-center text-sm text-muted-foreground">
              No documents match
            </p>
          )}
          {folders.map(([ckey, group]) => (
            <section key={ckey} className="rounded-2xl border bg-card p-4">
              <h2 className="mb-3 flex items-center gap-2 font-semibold">
                <FolderOpen size={16} className="text-primary" /> {group.name}
              </h2>
              <div className="space-y-3 pl-2 sm:pl-4">
                {[...group.projects.entries()].map(([folderName, list]) => (
                  <div key={folderName} className="rounded-xl border bg-muted/20 p-3">
                    <p className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                      {folderName} ({list.length})
                    </p>
                    <ul className="divide-y">
                      {list.map((d) => (
                        <li
                          key={d.id}
                          className="flex flex-wrap items-center justify-between gap-2 py-2 text-sm"
                        >
                          <div className="min-w-0">
                            <p className="font-medium">{d.name}</p>
                            <p className="text-xs text-muted-foreground">
                              {label(d.doc_type)}
                              {d.received_at ? ` · Received ${formatDate(d.received_at)}` : ""}
                            </p>
                          </div>
                          <div className="flex flex-wrap items-center gap-2">
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
                                  <Eye size={14} /> Preview
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
                                  <Download size={14} /> Download
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
            </section>
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
            { header: "Project", cell: (d) => d.project_number ?? "-" },
            { header: "Type", cell: (d) => label(d.doc_type) },
            { header: "Status", cell: (d) => <StatusBadge value={d.status} /> },
            { header: "Received", cell: (d) => (d.received_at ? formatDate(d.received_at) : "-") },
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
                        onClick={() => setPreview({ id: d.file_id!, name: d.file_name ?? d.name })}
                      >
                        <Eye size={14} /> Preview
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
                        <Download size={14} /> Download
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
    </>
  );
}
