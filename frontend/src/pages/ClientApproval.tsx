import { useState } from "react";
import { useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, Clock, MessageSquareWarning } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { PageLoader } from "@/components/shared/Display";
import { ApiError, api, get, publicUrl } from "@/lib/api";
import { errorMessage } from "@/hooks/useApi";
import { formatDate } from "@/lib/formatters";
import type { PublicApproval } from "@/types/services";

function Shell({ children, title }: { children: React.ReactNode; title?: string }) {
  return (
    <main className="mx-auto min-h-screen max-w-2xl p-4 sm:p-8">
      <header className="mb-6 border-b pb-4">
        <p className="text-sm text-muted-foreground">Design for approval</p>
        <h1 className="text-2xl font-semibold tracking-tight">{title ?? "Your design"}</h1>
      </header>
      {children}
    </main>
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
        <div className="rounded-2xl border bg-card p-8 text-center">
          <Clock className="mx-auto mb-3 text-muted-foreground" />
          <p className="font-medium">
            {expired ? "This link has expired" : "This link is not valid"}
          </p>
          <p className="mt-1 text-sm text-muted-foreground">
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
    <Shell title={a.title}>
      <p className="mb-4 text-sm text-muted-foreground">
        {a.business_name} · for {a.client_name} · Version {a.version_no}
      </p>
      <section className="mb-6 overflow-hidden rounded-2xl border bg-card">
        {a.has_file && mime.startsWith("image/") ? (
          <img
            src={fileUrl}
            alt={`${a.title}, version ${a.version_no}`}
            className="mx-auto max-h-[70vh] w-full object-contain"
          />
        ) : a.has_file && mime === "application/pdf" ? (
          <iframe title={a.title} src={fileUrl} className="h-[70vh] w-full" />
        ) : a.has_file ? (
          <p className="p-8 text-center">
            <a className="underline" href={fileUrl} target="_blank" rel="noreferrer">
              Open the file{a.file_name ? ` (${a.file_name})` : ""}
            </a>
          </p>
        ) : a.link ? (
          <p className="p-8 text-center">
            <a className="underline" href={a.link} target="_blank" rel="noreferrer">
              Open the design
            </a>
          </p>
        ) : (
          <p className="p-8 text-center text-muted-foreground">No preview is available.</p>
        )}
      </section>

      {a.comments.length > 0 && (
        <section className="mb-6 space-y-2">
          <h2 className="text-sm font-semibold">Comments</h2>
          {a.comments.map((c) => (
            <p key={c.created_at + c.comment} className="rounded-xl bg-muted/50 p-3 text-sm">
              <span className="font-medium">
                {c.author_type === "client" ? "You" : "Designer"}:{" "}
              </span>
              {c.comment}
            </p>
          ))}
        </section>
      )}

      {decided || decide.isSuccess ? (
        <div className="rounded-2xl border bg-card p-6 text-center">
          {a.status === "changes_requested" || (decide.isSuccess && choice === "changes") ? (
            <>
              <MessageSquareWarning className="mx-auto mb-2 text-amber-600" />
              <p className="font-medium">Thank you. We will send you a new version.</p>
            </>
          ) : (
            <>
              <CheckCircle2 className="mx-auto mb-2 text-emerald-600" />
              <p className="font-medium">Approved. Thank you!</p>
              <p className="mt-1 text-sm text-muted-foreground">We will prepare the final files.</p>
            </>
          )}
          {a.decided_at && (
            <p className="mt-2 text-xs text-muted-foreground">Decided {formatDate(a.decided_at)}</p>
          )}
        </div>
      ) : (
        <section className="space-y-3">
          <label className="block text-sm font-medium" htmlFor="comment">
            Comments (required if you want changes)
          </label>
          <Textarea
            id="comment"
            rows={3}
            placeholder="For example: make the text bigger"
            value={comment}
            onChange={(e) => setComment(e.target.value)}
          />
          {decide.error && <p className="text-sm text-destructive">{errorMessage(decide.error)}</p>}
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
          <p className="text-xs text-muted-foreground">
            This link works until {formatDate(a.expires_at)}. You can decide once.
          </p>
        </section>
      )}
    </Shell>
  );
}
