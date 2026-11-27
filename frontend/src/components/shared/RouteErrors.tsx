import { Link } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { useI18n } from "@/features/i18n/i18n";

export function NotFound() {
  const { t } = useI18n();
  return (
    <main className="grid min-h-screen place-items-center bg-[var(--od-page)] p-6 text-[var(--od-ink)]">
      <div className="od-card max-w-md items-center text-center">
        <p className="od-mono text-4xl font-bold tracking-tight text-[var(--od-ink-muted)]">404</p>
        <h1 className="od-page-title mt-1 text-xl">{t.common.pageNotFound}</h1>
        <p className="od-caption">{t.common.thePageYouAreLooking}</p>
        <Button asChild className="mt-3">
          <Link to="/">{t.common.returnHome}</Link>
        </Button>
      </div>
    </main>
  );
}

/** React Router renders this when a route loader or render throws. */
export function ErrorBoundary() {
  const { t } = useI18n();
  return (
    <main className="grid min-h-screen place-items-center bg-[var(--od-page)] p-6 text-[var(--od-ink)]">
      <div className="od-card max-w-md items-center text-center">
        <p className="od-mono text-4xl font-bold tracking-tight text-[var(--od-bad)]">500</p>
        <h1 className="od-page-title mt-1 text-xl">{t.common.somethingWentWrong}</h1>
        <p className="od-caption">{t.common.anUnexpectedErrorOccurredWhile}</p>
        <Button asChild className="mt-3">
          <Link to="/">{t.common.returnHome}</Link>
        </Button>
      </div>
    </main>
  );
}
