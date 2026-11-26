import { useState } from "react";
import { useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, Clock, ExternalLink, MessageSquareWarning } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { PageLoader } from "@/components/shared/Display";
import { ApiError, api, get, publicUrl } from "@/lib/api";
import { errorMessage } from "@/hooks/useApi";
import { formatDate } from "@/lib/formatters";
import type { PublicApproval } from "@/types/services";

function Shell({
  children,
  title,
  meta,
}: {
  children: React.ReactNode;
  title?: string;
  meta?: React.ReactNode;
}) {
  return (
    <div className="min-h-screen bg-[var(--od-app-sidebar)] px-4 py-8 sm:px-6 sm:py-12">
      <main className="mx-auto max-w-2xl space-y-6">
        <header className="border-b border-[var(--od-border)] pb-5">
          <p className="od-kpi-label">Design for approval</p>
          <h1 className="od-page-title mt-1 text-[var(--od-ink)]">{title ?? "Your design"}</h1>
          {meta && <div className="mt-2">{meta}</div>}
        </header>
        {children}
      </main>
    </div>
  );
}

/** Private page the client opens from the SMS link. No sign-in. One decision per link. */
export default function ClientApproval() {
  const { token = "" } = useParams();
  const queryClient = useQueryClient();
  const [comment, setComment] = useState("");
  const [choice, setChoice] = useState<"approve" | "changes" | null>(null);

  const page = useQuery({
    queryKey: ["public-approval", token],
    queryFn: () => get<PublicApproval>(`/public/design-approvals/${token}`),
    retry: false,
  });
  const decide = useMutation({
    mutationFn: (decision: "approve" | "changes") =>
      api.post(
        `/public/design-approvals/${token}/decision`,
        { decision, comment },
        { noRefresh: true },
      ),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["public-approval", token] }),
  });

  if (page.isLoading) return <PageLoader />;
  if (page.error) {
    const e = page.error;
    const expired = e instanceof ApiError && e.status === 410;
    return (
      <Shell>
        <div className="od-card p-8 text-center">
          <div className="mx-auto mb-3 flex h-11 w-11 items-center justify-center rounded-full border border-[var(--od-border)] bg-[var(--od-surface-subtle)] text-[var(--od-ink-muted)]">
            <Clock size={20} />
          </div>
          <p className="text-base font-semibold text-[var(--od-ink)]">
            {expired ? "This link has expired" : "This link is not valid"}
          </p>
          <p className="mt-1 text-sm text-[var(--od-ink-secondary)]">
            {expired ? "Please ask us to send you a new one." : errorMessage(e)}
          </p>
        </div>
      </Shell>
    );
  }
  const a = page.data!;
  const fileUrl = publicUrl(`/public/design-approvals/${token}/file`);
  const decided = a.status === "approved" || a.status === "changes_requested" || !!a.decided_at;
  const mime = a.mime_type ?? "";

  return (
    <Shell
      title={a.title}
      meta={
        <div className="flex flex-wrap items-center gap-2 text-sm text-[var(--od-ink-secondary)]">
          <span>
            {a.business_name} · for {a.client_name}
          </span>
          <Badge variant="secondary" className="od-tabular">
            Version {a.version_no}
          </Badge>
        </div>
      }
    >
      <section className="od-card-flush overflow-hidden">
        {a.has_file && mime.startsWith("image/") ? (
          <div className="bg-[var(--od-surface-subtle)] p-2">
            <img
              src={fileUrl}
              alt={`${a.title}, version ${a.version_no}`}
              className="mx-auto max-h-[70vh] w-full rounded-lg object-contain"
            />
          </div>
        ) : a.has_file && mime === "application/pdf" ? (
          <iframe title={a.title} src={fileUrl} className="h-[70vh] w-full" />
        ) : a.has_file ? (
          <div className="p-8 text-center">
            <Button variant="outline" asChild>
              <a href={fileUrl} target="_blank" rel="noreferrer">
                <ExternalLink size={14} /> Open the file{a.file_name ? ` (${a.file_name})` : ""}
              </a>
            </Button>
          </div>
        ) : a.link ? (
          <div className="p-8 text-center">
            <Button variant="outline" asChild>
              <a href={a.link} target="_blank" rel="noreferrer">
                <ExternalLink size={14} /> Open the design
              </a>
            </Button>
          </div>
        ) : (
          <p className="p-8 text-center text-sm text-[var(--od-ink-muted)]">
            No preview is available.
          </p>
        )}
      </section>

      {a.comments.length > 0 && (
        <section className="od-card space-y-3 p-5">
          <h2 className="od-card-title text-[var(--od-ink)]">Comments</h2>
          <div className="space-y-2">
            {a.comments.map((c) => (
              <div
                key={c.created_at + c.comment}
                className="rounded-xl border border-[var(--od-border)] bg-[var(--od-surface-subtle)] px-3.5 py-2.5 text-sm text-[var(--od-ink)]"
              >
                <span className="font-semibold">
                  {c.author_type === "client" ? "You" : "Designer"}:{" "}
                </span>
                <span className="text-[var(--od-ink-secondary)]">{c.comment}</span>
              </div>
            ))}
          </div>
        </section>
      )}

      {decided || decide.isSuccess ? (
        <div className="od-card p-6 text-center">
          {a.status === "changes_requested" || (decide.isSuccess && choice === "changes") ? (
            <>
              <MessageSquareWarning className="mx-auto mb-2.5 text-[var(--od-warning)]" size={28} />
              <p className="text-base font-semibold text-[var(--od-ink)]">
                Thank you. We will send you a new version.
              </p>
            </>
          ) : (
            <>
              <CheckCircle2 className="mx-auto mb-2.5 text-[var(--od-good)]" size={28} />
              <p className="text-base font-semibold text-[var(--od-ink)]">Approved. Thank you!</p>
              <p className="mt-1 text-sm text-[var(--od-ink-secondary)]">
                We will prepare the final files.
              </p>
            </>
          )}
          {a.decided_at && <p className="od-caption mt-2.5">Decided {formatDate(a.decided_at)}</p>}
        </div>
      ) : (
        <section className="od-card space-y-4 p-6">
          <div>
            <label className="block text-sm font-medium text-[var(--od-ink)]" htmlFor="comment">
              Comments (required if you want changes)
            </label>
            <Textarea
              id="comment"
              rows={3}
              className="mt-2"
              placeholder="For example: make the text bigger"
              value={comment}
              onChange={(e) => setComment(e.target.value)}
            />
          </div>
          {decide.error && (
            <p className="text-sm text-[var(--od-bad)]">{errorMessage(decide.error)}</p>
          )}
          <div className="flex flex-wrap gap-3">
            <Button
              size="lg"
              disabled={decide.isPending}
              onClick={() => {
                setChoice("approve");
                decide.mutate("approve");
              }}
            >
              Approve
            </Button>
            <Button
              size="lg"
              variant="outline"
              disabled={decide.isPending || !comment.trim()}
              onClick={() => {
                setChoice("changes");
                decide.mutate("changes");
              }}
            >
              Request changes
            </Button>
          </div>
          <p className="od-caption border-t border-[var(--od-border)] pt-3">
            This link works until {formatDate(a.expires_at)}. You can decide once.
          </p>
        </section>
      )}
    </Shell>
  );
}
