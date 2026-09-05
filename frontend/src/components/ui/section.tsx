import * as React from "react";

import { cn } from "@/lib/utils";

// Section is a titled run of content separated from its neighbours by a
// hairline rather than boxed in a card: a settings page reads as one
// document, not a stack of tiles.
interface SectionProps extends Omit<React.HTMLAttributes<HTMLElement>, "title"> {
  title: React.ReactNode;
  description?: React.ReactNode;
}

const Section = React.forwardRef<HTMLElement, SectionProps>(
  ({ className, title, description, children, ...props }, ref) => (
    <section
      ref={ref}
      className={cn(
        "border-t border-border pt-6 first:border-t-0 first:pt-0",
        className
      )}
      {...props}
    >
      <h2 className="font-title text-xl leading-tight">{title}</h2>
      {description && (
        <p className="mt-1 max-w-prose text-sm text-muted-foreground">
          {description}
        </p>
      )}
      <div className="mt-4">{children}</div>
    </section>
  )
);
Section.displayName = "Section";

export { Section };
