import * as React from "react";

import { cn } from "@/lib/utils";

const Textarea = React.forwardRef<HTMLTextAreaElement, React.ComponentProps<"textarea">>(
  ({ className, ...props }, ref) => {
    return (
      <textarea
        className={cn(
          "flex min-h-[76px] w-full rounded-[var(--od-app-radius,10px)] border border-[var(--od-border-strong)] bg-[var(--od-surface)] px-3 py-2 text-sm text-[var(--od-ink)] shadow-[0_1px_2px_rgba(0,0,0,0.02)] transition-colors placeholder:text-[var(--od-ink-muted)] focus-visible:border-[var(--od-ink-secondary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40 disabled:cursor-not-allowed disabled:opacity-50",
          className,
        )}
        ref={ref}
        {...props}
      />
    );
  },
);
Textarea.displayName = "Textarea";

export { Textarea };
