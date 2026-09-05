import * as React from "react";

import { cn } from "@/lib/utils";

// Native <select>, styled to match Input. Native on purpose: WebKitGTK
// renders its own popup, which stays keyboard- and screen-reader-friendly
// without a menu library.
const Select = React.forwardRef<
  HTMLSelectElement,
  React.ComponentProps<"select">
>(({ className, ...props }, ref) => (
  <select
    ref={ref}
    className={cn(
      "h-9 rounded-md border border-input bg-card px-2.5 text-sm transition-colors focus-visible:border-ring focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40 disabled:cursor-not-allowed disabled:opacity-50",
      className
    )}
    {...props}
  />
));
Select.displayName = "Select";

export { Select };
