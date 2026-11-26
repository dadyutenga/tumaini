import { Link } from "react-router-dom";
import { Button } from "@/components/ui/button";

export function NotFound() {
  return (
    <main className="grid min-h-screen place-items-center bg-[var(--od-page)] p-6 text-[var(--od-ink)]">
      <div className="od-card max-w-md items-center text-center">
        <p className="od-mono text-4xl font-bold tracking-tight text-[var(--od-ink-muted)]">404</p>
        <h1 className="od-page-title mt-1 text-xl">Page not found</h1>
        <p className="od-caption">The page you are looking for does not exist or has been moved.</p>
        <Button asChild className="mt-3">
          <Link to="/">Return home</Link>
        </Button>
      </div>
    </main>
  );
}

/** React Router renders this when a route loader or render throws. */
export function ErrorBoundary() {
  return (
    <main className="grid min-h-screen place-items-center bg-[var(--od-page)] p-6 text-[var(--od-ink)]">
      <div className="od-card max-w-md items-center text-center">
        <p className="od-mono text-4xl font-bold tracking-tight text-[var(--od-bad)]">500</p>
        <h1 className="od-page-title mt-1 text-xl">Something went wrong</h1>
        <p className="od-caption">An unexpected error occurred while rendering this page.</p>
        <Button asChild className="mt-3">
          <Link to="/">Return home</Link>
        </Button>
      </div>
    </main>
  );
}
