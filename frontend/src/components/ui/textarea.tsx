import * as React from "react";

import { cn } from "@/lib/utils";

// Auto-growing textarea: height tracks content (capped by any max-h-* class
// on it, beyond which it scrolls). Sizing is done in JS because WebKitGTK
// doesn't reliably support `field-sizing: content`.
const Textarea = React.forwardRef<
  HTMLTextAreaElement,
  React.ComponentProps<"textarea">
>(({ className, value, rows = 1, ...props }, ref) => {
  const inner = React.useRef<HTMLTextAreaElement | null>(null);

  React.useLayoutEffect(() => {
    const el = inner.current;
    if (!el) return;
    el.style.height = "auto";
    // scrollHeight is the content box; the element is border-box, so add
    // the border back or the last line overflows by 2px and shows a bar.
    el.style.height = `${el.scrollHeight + el.offsetHeight - el.clientHeight}px`;
  }, [value]);

  return (
    <textarea
      rows={rows}
      value={value}
      className={cn(
        "flex w-full resize-none overflow-y-auto rounded-md border border-input bg-card px-3 py-2 text-sm transition-colors placeholder:text-muted-foreground/80 focus-visible:border-ring focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40 disabled:cursor-not-allowed disabled:opacity-50",
        className
      )}
      ref={(node) => {
        inner.current = node;
        if (typeof ref === "function") ref(node);
        else if (ref) ref.current = node;
      }}
      {...props}
    />
  );
});
Textarea.displayName = "Textarea";

export { Textarea };
