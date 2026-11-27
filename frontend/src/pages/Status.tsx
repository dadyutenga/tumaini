import { Link } from "react-router-dom";
import { ArrowLeft, Home } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useI18n } from "@/features/i18n/i18n";

export default function Status({
  code,
  title,
  description,
}: {
  code: string;
  title?: string;
  description?: string;
}) {
  const { t } = useI18n();
  const defaultDescriptions: Record<string, string> = {
    "403": t.common.youDoNotHavePermission,
    "404": t.common.thePageYouAreLooking2,
    "500": t.common.anUnexpectedServerErrorOccurred,
  };

  const defaultTitles: Record<string, string> = {
    "403": t.common.accessDenied,
    "404": t.common.pageNotFound,
    "500": t.common.serverError,
  };

  return (
    <main className="grid min-h-[75vh] place-items-center px-4 py-12 text-center">
      <section className="od-card w-full max-w-md p-8">
        <div className="mx-auto inline-flex items-center justify-center rounded-full border border-[var(--od-border-strong)] bg-[var(--od-surface-subtle)] px-3.5 py-1 text-xs font-medium text-[var(--od-ink-secondary)]">
          {t.common.httpStatus}
        </div>
        <p className="od-mono mt-4 text-6xl font-semibold tracking-tight text-[var(--od-ink)]">
          {code}
        </p>
        <h1 className="mt-3 text-xl font-semibold tracking-tight text-[var(--od-ink)]">
          {title ?? defaultTitles[code]}
        </h1>
        <p className="mt-2 text-sm leading-relaxed text-[var(--od-ink-secondary)]">
          {description ?? defaultDescriptions[code] ?? t.common.anUnexpectedStatusOccurred}
        </p>
        <div className="mt-6 flex flex-wrap justify-center gap-2.5 border-t border-[var(--od-border)] pt-6">
          <Button asChild>
            <Link to="/">
              <Home size={15} /> {t.common.returnToDashboard}
            </Link>
          </Button>
          <Button variant="outline" onClick={() => window.history.back()}>
            <ArrowLeft size={15} /> {t.common.goBack}
          </Button>
        </div>
      </section>
    </main>
  );
}
