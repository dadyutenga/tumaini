import { useState } from "react";
import { Download, Eye, Paperclip, Trash2, Upload } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { FilePreviewDialog } from "@/components/shared/FilePreview";
import { errorMessage, useGet, useWrite } from "@/hooks/useApi";
import { download, uploadFile } from "@/lib/api";
import { formatDateTime } from "@/lib/formatters";

interface Attachment {
  id: string;
  file_id: string;
  file_name: string;
  mime_type: string;
  size_bytes: number;
  label?: string | null;
  uploaded_by_name?: string | null;
  created_at: string;
}

function formatSize(bytes: number) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

export function AttachmentsSection({
  entityType,
  entityId,
  readOnly = false,
}: {
  entityType: string;
  entityId: string;
  readOnly?: boolean;
}) {
  const list = useGet<Attachment[]>(
    "/attachments",
    { entity_type: entityType, entity_id: entityId },
    { enabled: Boolean(entityId) },
  );
  const [label, setLabel] = useState("");
  const [uploading, setUploading] = useState(false);
  const [preview, setPreview] = useState<{ id: string; name: string } | null>(null);

  const attach = useWrite<
    { entity_type: string; entity_id: string; file_id: string; label: string },
    unknown
  >("POST", "/attachments", {
    invalidate: ["/attachments"],
    success: "File attached",
    onSuccess: () => setLabel(""),
  });

  const remove = useWrite<string, unknown>("DELETE", (id) => `/attachments/${id}`, {
    invalidate: ["/attachments"],
    success: "Attachment removed",
  });

  async function onPick(file: File | undefined) {
    if (!file) return;
    setUploading(true);
    try {
      const fileId = await uploadFile(file);
      await attach.mutateAsync({
        entity_type: entityType,
        entity_id: entityId,
        file_id: fileId,
        label: label.trim(),
      });
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setUploading(false);
    }
  }

  const items = list.data ?? [];

  return (
    <div className="space-y-4 rounded-2xl border bg-card p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <Paperclip size={16} className="text-muted-foreground" />
          <h3 className="font-semibold">Attachments ({items.length})</h3>
        </div>
        {!readOnly && (
          <div className="flex flex-wrap items-center gap-2">
            <Input
              className="h-8 w-44 text-xs"
              placeholder="Optional label…"
              value={label}
              onChange={(e) => setLabel(e.target.value)}
            />
            <label className="inline-flex h-8 cursor-pointer items-center gap-1.5 rounded-md border bg-background px-3 text-xs font-medium shadow-sm transition hover:bg-accent">
              <Upload size={13} />
              <span>{uploading ? "Uploading…" : "Attach file"}</span>
              <input
                type="file"
                className="hidden"
                disabled={uploading}
                onChange={(e) => {
                  void onPick(e.target.files?.[0]);
                  e.target.value = "";
                }}
              />
            </label>
          </div>
        )}
      </div>

      {list.isLoading ? (
        <p className="text-xs text-muted-foreground">Loading attachments…</p>
      ) : items.length === 0 ? (
        <p className="text-xs text-muted-foreground">No files attached yet.</p>
      ) : (
        <div className="divide-y rounded-lg border">
          {items.map((a) => (
            <div key={a.id} className="flex flex-wrap items-center justify-between gap-2 px-3 py-2 text-sm">
              <div className="min-w-0">
                <p className="truncate font-medium">
                  {a.label ? `${a.label} — ` : ""}
                  {a.file_name}
                </p>
                <p className="text-xs text-muted-foreground">
                  {formatSize(a.size_bytes)} · {a.uploaded_by_name ?? "User"} ·{" "}
                  {formatDateTime(a.created_at)}
                </p>
              </div>
              <div className="flex items-center gap-1">
                <Button
                  size="sm"
                  variant="ghost"
                  className="h-7 px-2 text-xs"
                  onClick={() => setPreview({ id: a.file_id, name: a.file_name })}
                >
                  <Eye size={13} /> Preview
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  className="h-7 px-2 text-xs"
                  onClick={() =>
                    download(`/files/${a.file_id}`, {}, a.file_name).catch((e) =>
                      toast.error(errorMessage(e)),
                    )
                  }
                >
                  <Download size={13} />
                </Button>
                {!readOnly && (
                  <Button
                    size="sm"
                    variant="ghost"
                    className="h-7 px-2 text-xs text-destructive"
                    disabled={remove.isPending}
                    onClick={() => remove.mutate(a.id)}
                  >
                    <Trash2 size={13} />
                  </Button>
                )}
              </div>
            </div>
          ))}
        </div>
      )}

      <FilePreviewDialog
        fileId={preview?.id ?? null}
        fileName={preview?.name}
        onClose={() => setPreview(null)}
      />
    </div>
  );
}
