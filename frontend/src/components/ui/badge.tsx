import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";

import { cn } from "@/lib/utils";

const badgeVariants = cva(
  "inline-flex items-center gap-1.5 rounded-full border px-2.5 py-0.5 text-[11px] font-medium leading-4 transition-colors focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2",
  {
    variants: {
      variant: {
        default:
          "border-transparent bg-[var(--od-app-primary)] text-[var(--od-app-primary-foreground)]",
        secondary: "border-transparent bg-[var(--od-neutral-soft)] text-[var(--od-ink-secondary)]",
        destructive:
          "border-transparent bg-[color-mix(in_srgb,var(--od-bad)_12%,transparent)] text-[var(--od-bad)]",
        outline: "border-[var(--od-border-strong)] text-[var(--od-ink-secondary)]",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  },
);

export interface BadgeProps
  extends React.HTMLAttributes<HTMLDivElement>, VariantProps<typeof badgeVariants> {}

function Badge({ className, variant, ...props }: BadgeProps) {
  return <div className={cn(badgeVariants({ variant }), className)} {...props} />;
}

export { Badge, badgeVariants };
