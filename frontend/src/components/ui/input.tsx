import * as React from "react";

import { cn } from "@/lib/utils";

const Input = React.forwardRef<HTMLInputElement, React.ComponentProps<"input">>(
  ({ className, type, ...props }, ref) => {
    return (
      <input
        type={type}
        className={cn(
          "flex h-9 w-full rounded-[var(--od-app-radius,10px)] border border-[var(--od-border-strong)] bg-[var(--od-surface)] px-3 py-1.5 text-sm text-[var(--od-ink)] shadow-[0_1px_2px_rgba(0,0,0,0.02)] transition-colors file:border-0 file:bg-transparent file:text-sm file:font-medium file:text-foreground placeholder:text-[var(--od-ink-muted)] focus-visible:border-[var(--od-ink-secondary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40 disabled:cursor-not-allowed disabled:opacity-50",
          className,
        )}
        ref={ref}
        {...props}
      />
    );
  },
);
Input.displayName = "Input";

export { Input };
