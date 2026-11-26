import { Link } from "react-router-dom";
import { ArrowLeft, Home } from "lucide-react";
import { Button } from "@/components/ui/button";

export default function Status({
  code,
  title,
  description,
}: {
  code: string;
  title: string;
  description?: string;
}) {
  const defaultDescriptions: Record<string, string> = {
    "403": "You do not have permission to access this resource or view this page.",
    "404": "The page you are looking for does not exist or may have been moved.",
    "500": "An unexpected server error occurred. Please try again later.",
  };

  return (
    <main className="grid min-h-[75vh] place-items-center px-4 py-12 text-center">
      <section className="od-card w-full max-w-md p-8">
        <div className="mx-auto inline-flex items-center justify-center rounded-full border border-[var(--od-border-strong)] bg-[var(--od-surface-subtle)] px-3.5 py-1 text-xs font-medium text-[var(--od-ink-secondary)]">
          HTTP Status
        </div>
        <p className="od-mono mt-4 text-6xl font-semibold tracking-tight text-[var(--od-ink)]">
          {code}
        </p>
        <h1 className="mt-3 text-xl font-semibold tracking-tight text-[var(--od-ink)]">{title}</h1>
        <p className="mt-2 text-sm leading-relaxed text-[var(--od-ink-secondary)]">
          {description ?? defaultDescriptions[code] ?? "An unexpected status occurred."}
        </p>
        <div className="mt-6 flex flex-wrap justify-center gap-2.5 border-t border-[var(--od-border)] pt-6">
          <Button asChild>
            <Link to="/">
              <Home size={15} /> Return to Dashboard
            </Link>
          </Button>
          <Button variant="outline" onClick={() => window.history.back()}>
            <ArrowLeft size={15} /> Go back
          </Button>
        </div>
      </section>
    </main>
  );
}
