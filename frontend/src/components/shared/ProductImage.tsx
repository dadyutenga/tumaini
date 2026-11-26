import { useEffect, useState } from "react";
import { Package } from "lucide-react";
import { fetchFileBlob } from "@/lib/api";
import { cn } from "@/lib/utils";

const blobUrlCache = new Map<string, string>();
const inFlight = new Map<string, Promise<string>>();

function loadProductPhoto(fileId: string): Promise<string> {
  const hit = blobUrlCache.get(fileId);
  if (hit) return Promise.resolve(hit);
  const existing = inFlight.get(fileId);
  if (existing) return existing;
  const req = fetchFileBlob(`/files/${fileId}`)
    .then((res) => {
      blobUrlCache.set(fileId, res.url);
      inFlight.delete(fileId);
      return res.url;
    })
    .catch((err) => {
      inFlight.delete(fileId);
      throw err;
    });
  inFlight.set(fileId, req);
  return req;
}

function initialsOf(name: string) {
  return (
    name
      .trim()
      .split(/\s+/)
      .slice(0, 2)
      .map((w) => w[0]?.toUpperCase() ?? "")
      .join("") || "PR"
  );
}

/**
 * E-commerce style product photo card visual with cached authenticated `/files/:id`
 * loading and a soft, color-matched fallback tile when no photo is uploaded yet.
 */
export function ProductImage({
  fileId,
  name,
  className,
  size = "card",
}: {
  fileId?: string | null;
  name: string;
  className?: string;
  size?: "thumb" | "card";
}) {
  const [url, setUrl] = useState<string | null>(() =>
    fileId ? (blobUrlCache.get(fileId) ?? null) : null,
  );
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    if (!fileId) {
      setUrl(null);
      setFailed(false);
      return;
    }
    const cached = blobUrlCache.get(fileId);
    if (cached) {
      setUrl(cached);
      setFailed(false);
      return;
    }
    let alive = true;
    loadProductPhoto(fileId)
      .then((next) => {
        if (alive) {
          setUrl(next);
          setFailed(false);
        }
      })
      .catch(() => {
        if (alive) setFailed(true);
      });
    return () => {
      alive = false;
    };
  }, [fileId]);

  if (url && !failed) {
    return (
      <img
        src={url}
        alt={name}
        loading="lazy"
        onError={() => setFailed(true)}
        className={cn(
          "h-full w-full object-cover transition duration-300 group-hover:scale-105",
          className,
        )}
      />
    );
  }

  if (size === "thumb") {
    return (
      <div
        className={cn(
          "flex h-full w-full items-center justify-center bg-gradient-to-br from-primary/15 via-primary/5 to-muted text-xs font-bold text-primary",
          className,
        )}
      >
        {initialsOf(name)}
      </div>
    );
  }

  return (
    <div
      className={cn(
        "relative flex h-full w-full flex-col items-center justify-center gap-1.5 bg-gradient-to-br from-primary/12 via-primary/5 to-muted/80 text-primary/80 transition duration-300 group-hover:from-primary/18",
        className,
      )}
    >
      <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-background/80 shadow-sm ring-1 ring-primary/15 backdrop-blur">
        <Package size={22} className="text-primary" />
      </div>
      <span className="text-[11px] font-semibold tracking-wider text-primary/70">
        {initialsOf(name)}
      </span>
    </div>
  );
}
