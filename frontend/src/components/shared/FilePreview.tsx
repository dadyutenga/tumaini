import { useEffect, useState } from "react";
import { Download, Eye, FileText } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { errorMessage } from "@/hooks/useApi";
import { download, fetchFileBlob } from "@/lib/api";

/** Authenticated thumbnail preview for an image or PDF stored at `/files/:id`. */
export function FileThumbnail({
  fileId,
  fileName,
  mimeType,
  onOpen,
}: {
  fileId: string;
  fileName?: string | null;
  mimeType?: string | null;
  onOpen?: () => void;
}) {
  const [blob, setBlob] = useState<{ url: string; mime: string } | null>(null);
  const [failed, setFailed] = useState(false);
  const isImage = (mimeType ?? blob?.mime ?? "").startsWith("image/");

  useEffect(() => {
    if (mimeType && !mimeType.startsWith("image/")) return;
    const ctrl = new AbortController();
    let url: string | null = null;
    fetchFileBlob(`/files/${fileId}`, ctrl.signal)
      .then((res) => {
        url = res.url;
        setBlob(res);
      })
      .catch(() => {
        if (!ctrl.signal.aborted) setFailed(true);
      });
    return () => {
      ctrl.abort();
      if (url) URL.revokeObjectURL(url);
    };
  }, [fileId, mimeType]);

  return (
    <button
      type="button"
      onClick={onOpen}
      className="group relative flex h-28 w-40 items-center justify-center overflow-hidden rounded-lg border bg-muted text-xs text-muted-foreground transition hover:border-primary"
      title={fileName ?? "Preview file"}
    >
      {isImage && blob && !failed ? (
        <img
          src={blob.url}
          alt={fileName ?? "Preview"}
          className="h-full w-full object-cover transition group-hover:scale-105"
        />
      ) : (
        <div className="flex flex-col items-center gap-1 p-2 text-center">
          <FileText size={20} />
          <span className="line-clamp-2">{fileName ?? "Preview file"}</span>
        </div>
      )}
      <span className="absolute bottom-1 right-1 rounded bg-background/90 px-1.5 py-0.5 text-[10px] font-medium text-foreground shadow-sm">
        <Eye size={10} className="mr-0.5 inline" /> Preview
      </span>
    </button>
  );
}

/** Full-screen authenticated preview dialog for images and PDFs stored at `/files/:id`. */
export function FilePreviewDialog({
  fileId,
  fileName,
  onClose,
}: {
  fileId: string | null;
  fileName?: string | null;
  onClose: () => void;
}) {
  const [blob, setBlob] = useState<{ url: string; mime: string } | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!fileId) {
      setBlob(null);
      setError(null);
      return;
    }
    const ctrl = new AbortController();
    let url: string | null = null;
    setLoading(true);
    setError(null);
    fetchFileBlob(`/files/${fileId}`, ctrl.signal)
      .then((res) => {
        url = res.url;
        setBlob(res);
      })
      .catch((e) => {
        if (!ctrl.signal.aborted) setError(errorMessage(e));
      })
      .finally(() => {
        if (!ctrl.signal.aborted) setLoading(false);
      });
    return () => {
      ctrl.abort();
      if (url) URL.revokeObjectURL(url);
    };
  }, [fileId]);

  const isImage = blob?.mime.startsWith("image/");
  const isPdf = blob?.mime === "application/pdf";

  return (
    <Dialog open={!!fileId} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[92vh] overflow-y-auto sm:max-w-4xl">
        <DialogHeader>
          <DialogTitle className="flex items-center justify-between gap-3 pr-8">
            <span className="truncate">{fileName ?? "File preview"}</span>
            {fileId && (
              <Button
                size="sm"
                variant="outline"
                onClick={() =>
                  download(`/files/${fileId}`, {}, fileName ?? "file").catch((e) =>
                    toast.error(errorMessage(e)),
                  )
                }
              >
                <Download size={14} /> Download
              </Button>
            )}
          </DialogTitle>
          <DialogDescription>Inline preview</DialogDescription>
        </DialogHeader>
        {loading && <p className="py-12 text-center text-sm text-muted-foreground">Loading…</p>}
        {error && <p className="py-8 text-center text-sm text-destructive">{error}</p>}
        {!loading && !error && blob && (
          <div className="flex justify-center">
            {isImage ? (
              <img
                src={blob.url}
                alt={fileName ?? "Preview"}
                className="max-h-[70vh] rounded-lg border object-contain"
              />
            ) : isPdf ? (
              <iframe
                src={blob.url}
                title={fileName ?? "PDF preview"}
                className="h-[70vh] w-full rounded-lg border"
              />
            ) : (
              <div className="py-10 text-center text-sm text-muted-foreground">
                <p>Preview is not available for this file type ({blob.mime || "unknown"}).</p>
                {fileId && (
                  <Button
                    className="mt-3"
                    variant="outline"
                    onClick={() =>
                      download(`/files/${fileId}`, {}, fileName ?? "file").catch((e) =>
                        toast.error(errorMessage(e)),
                      )
                    }
                  >
                    <Download size={14} /> Download file
                  </Button>
                )}
              </div>
            )}
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
