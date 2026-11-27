import * as React from "react";

import { cn } from "@/lib/utils";

const Textarea = React.forwardRef<HTMLTextAreaElement, React.ComponentProps<"textarea">>(
  ({ className, ...props }, ref) => {
    return (
      <textarea
        className={cn(
          "flex min-h-[76px] w-full rounded-[var(--od-app-radius,10px)] border border-input bg-[var(--od-surface)] px-3 py-2 text-sm text-[var(--od-ink)] shadow-[var(--od-shadow)] transition-colors placeholder:text-[var(--od-ink-muted)] focus-visible:border-ring focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/35 disabled:cursor-not-allowed disabled:opacity-50",
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
