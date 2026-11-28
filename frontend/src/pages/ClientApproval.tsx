import { useState } from "react";
import { useParams, useSearchParams } from "react-router-dom";
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
import { I18nProvider, useI18n } from "@/features/i18n/i18n";
import type { Locale } from "@/types";
import { fmt } from "@/features/i18n/format";

function Shell({
  children,
  title,
  meta,
}: {
  children: React.ReactNode;
  title?: string;
  meta?: React.ReactNode;
}) {
  const { t } = useI18n();
  return (
    <div className="min-h-screen bg-[var(--od-app-sidebar)] px-4 py-8 sm:px-6 sm:py-12">
      <main className="mx-auto max-w-2xl space-y-6">
        <header className="border-b border-[var(--od-border)] pb-5">
          <p className="od-kpi-label">{t.design.designForApproval}</p>
          <h1 className="od-page-title mt-1 text-[var(--od-ink)]">
            {title ?? t.design.yourDesign}
          </h1>
          {meta && <div className="mt-2">{meta}</div>}
        </header>
        {children}
      </main>
    </div>
  );
}

/** The client has no account: follow `?lang=`, then the language of their browser. */
function publicLocale(requested: string | null): Locale {
  if (requested === "sw" || requested === "en") return requested;
  return navigator.language.toLowerCase().startsWith("sw") ? "sw" : "en";
}

/** Private page the client opens from the SMS link. No sign-in. One decision per link. */
export default function ClientApproval() {
  const [search] = useSearchParams();
  return (
    <I18nProvider fixedLocale={publicLocale(search.get("lang"))}>
      <Approval />
    </I18nProvider>
  );
}

function Approval() {
  const { t } = useI18n();
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
            {expired ? t.design.thisLinkHasExpired : t.design.thisLinkIsNotValid}
          </p>
          <p className="mt-1 text-sm text-[var(--od-ink-secondary)]">
            {expired ? t.design.pleaseAskUsToSend : errorMessage(e)}
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
            {fmt(t.design.for, { business_name: a.business_name, client_name: a.client_name })}
          </span>
          <Badge variant="secondary" className="od-tabular">
            {fmt(t.design.version, { version_no: a.version_no })}
          </Badge>
        </div>
      }
    >
      <section className="od-card-flush overflow-hidden">
        {a.has_file && mime.startsWith("image/") ? (
          <div className="bg-[var(--od-surface-subtle)] p-2">
            <img
              src={fileUrl}
              alt={fmt(t.design.version2, { title: a.title, version_no: a.version_no })}
              className="mx-auto max-h-[70vh] w-full rounded-lg object-contain"
            />
          </div>
        ) : a.has_file && mime === "application/pdf" ? (
          <iframe title={a.title} src={fileUrl} className="h-[70vh] w-full" />
        ) : a.has_file ? (
          <div className="p-8 text-center">
            <Button variant="outline" asChild>
              <a href={fileUrl} target="_blank" rel="noreferrer">
                <ExternalLink size={14} />{" "}
                {fmt(t.design.openTheFile, { value: a.file_name ? ` (${a.file_name})` : "" })}
              </a>
            </Button>
          </div>
        ) : a.link ? (
          <div className="p-8 text-center">
            <Button variant="outline" asChild>
              <a href={a.link} target="_blank" rel="noreferrer">
                <ExternalLink size={14} /> {t.design.openTheDesign}
              </a>
            </Button>
          </div>
        ) : (
          <p className="p-8 text-center text-sm text-[var(--od-ink-muted)]">
            {t.design.noPreviewIsAvailable}
          </p>
        )}
      </section>

      {a.comments.length > 0 && (
        <section className="od-card space-y-3 p-5">
          <h2 className="od-card-title text-[var(--od-ink)]">{t.design.comments}</h2>
          <div className="space-y-2">
            {a.comments.map((c) => (
              <div
                key={c.created_at + c.comment}
                className="rounded-xl border border-[var(--od-border)] bg-[var(--od-surface-subtle)] px-3.5 py-2.5 text-sm text-[var(--od-ink)]"
              >
                <span className="font-semibold">
                  {c.author_type === "client" ? t.design.you : t.common.designer}:{" "}
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
                {t.design.thankYouWeWillSend}
              </p>
            </>
          ) : (
            <>
              <CheckCircle2 className="mx-auto mb-2.5 text-[var(--od-good)]" size={28} />
              <p className="text-base font-semibold text-[var(--od-ink)]">
                {t.design.approvedThankYou}
              </p>
              <p className="mt-1 text-sm text-[var(--od-ink-secondary)]">
                {t.design.weWillPrepareTheFinal}
              </p>
            </>
          )}
          {a.decided_at && (
            <p className="od-caption mt-2.5">
              {fmt(t.design.decided, { decided_at: formatDate(a.decided_at) })}
            </p>
          )}
        </div>
      ) : (
        <section className="od-card space-y-4 p-6">
          <div>
            <label className="block text-sm font-medium text-[var(--od-ink)]" htmlFor="comment">
              {t.design.commentsRequiredIfYouWant}
            </label>
            <Textarea
              id="comment"
              rows={3}
              className="mt-2"
              placeholder={t.design.forExampleMakeTheText}
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
              {t.common.approve}
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
              {t.design.requestChanges}
            </Button>
          </div>
          <p className="od-caption border-t border-[var(--od-border)] pt-3">
            {fmt(t.design.thisLinkWorksUntilYou, { expires_at: formatDate(a.expires_at) })}
          </p>
        </section>
      )}
    </Shell>
  );
}
