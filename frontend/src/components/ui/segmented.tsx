import { cn } from "@/lib/utils";

// A two-or-three-way choice shown as one control, for settings that are
// a state rather than an action (theme, developer mode, streaming).
interface SegmentedProps<T extends string> {
  value: T;
  options: { value: T; label: string }[];
  onChange: (v: T) => void;
  "aria-label"?: string;
  size?: "sm" | "default";
}

export function Segmented<T extends string>({
  value,
  options,
  onChange,
  size = "default",
  ...rest
}: SegmentedProps<T>) {
  return (
    <div
      role="radiogroup"
      aria-label={rest["aria-label"]}
      className="inline-flex rounded-md border border-input bg-card p-0.5"
    >
      {options.map((o) => {
        const active = o.value === value;
        return (
          <button
            key={o.value}
            role="radio"
            aria-checked={active}
            className={cn(
              "rounded-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/70",
              size === "sm" ? "px-2.5 py-0.5 text-[0.8667rem]" : "px-3 py-1 text-sm",
              active
                ? "bg-primary text-primary-foreground"
                : "text-muted-foreground hover:text-foreground"
            )}
            onClick={() => onChange(o.value)}
          >
            {o.label}
          </button>
        );
      })}
    </div>
  );
}
