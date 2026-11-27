import { APP_NAME } from "@/lib/constants";
import type { ReactNode } from "react";
import { Link } from "react-router-dom";

export function AuthLayout({ children }: { children: ReactNode }) {
  return (
    <main className="grid min-h-screen place-items-center bg-[var(--od-page)] px-4 py-10">
      <div className="w-full max-w-[420px]">
        <div className="mb-6 flex items-center justify-center">
          <Link
            to="/"
            className="od-wordmark inline-flex items-center gap-2.5 text-[16px] font-semibold text-[var(--od-ink)]"
          >
            <span className="grid h-8 w-8 place-items-center rounded-[10px] bg-[var(--od-app-primary)] text-xs font-bold text-[var(--od-app-primary-foreground)]">
              {APP_NAME.slice(0, 2).toUpperCase()}
            </span>
            <span>{APP_NAME}</span>
          </Link>
        </div>
        <section className="od-card p-6 sm:p-8">{children}</section>
      </div>
    </main>
  );
}
