import * as React from "react";
import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";

import { cn } from "@/lib/utils";

const buttonVariants = cva(
  "inline-flex items-center justify-center gap-1.5 whitespace-nowrap rounded-[var(--od-app-radius,10px)] text-[13px] font-medium transition-all focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60 disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:size-4 [&_svg]:shrink-0 cursor-pointer",
  {
    variants: {
      variant: {
        default:
          "bg-[var(--od-app-primary)] text-[var(--od-app-primary-foreground)] shadow-[0_1px_2px_rgba(0,0,0,0.06)] hover:opacity-90",
        destructive:
          "bg-destructive text-destructive-foreground shadow-[0_1px_2px_rgba(0,0,0,0.06)] hover:opacity-90",
        outline:
          "border border-[var(--od-border-strong)] bg-[var(--od-surface)] text-[var(--od-ink)] shadow-[0_1px_2px_rgba(0,0,0,0.03)] hover:bg-[var(--od-surface-subtle)]",
        secondary:
          "bg-[var(--od-neutral-soft)] text-[var(--od-ink)] hover:bg-[var(--od-border-strong)]",
        ghost:
          "text-[var(--od-ink-secondary)] hover:bg-[var(--od-surface-subtle)] hover:text-[var(--od-ink)]",
        link: "text-[var(--od-ink)] underline-offset-4 hover:underline",
      },
      size: {
        default: "h-9 px-3.5 py-2",
        sm: "h-8 px-2.5 text-xs",
        lg: "h-10 px-5 text-sm",
        icon: "h-8 w-8",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "default",
    },
  },
);

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>, VariantProps<typeof buttonVariants> {
  asChild?: boolean;
}

const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, asChild = false, ...props }, ref) => {
    const Comp = asChild ? Slot : "button";
    return (
      <Comp className={cn(buttonVariants({ variant, size, className }))} ref={ref} {...props} />
    );
  },
);
Button.displayName = "Button";

export { Button, buttonVariants };
