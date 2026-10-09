import { useMemo, useState } from "react";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { cn } from "@/lib/utils";
import { presets } from "../../wailsjs/go/models";

// Lists longer than this get a search box; the select itself is capped
// so hosts with tens of thousands of models (Featherless) don't stall
// the webview — the search narrows the rest.
const SEARCH_THRESHOLD = 12;
const MAX_OPTIONS = 400;

interface Props {
  catalog: presets.Catalog | null;
  value: string;
  onChange: (id: string) => void;
  disabled?: boolean;
  id?: string;
  // compact: chat-header sizing (h-8, bounded width).
  compact?: boolean;
  className?: string;
}

// ModelPicker is the one model dropdown for every provider: a
// Recommended group (the preset's pinned models that the endpoint
// offers) above All models (the endpoint's chat-capable list), with a
// search box once the list is long. Ollama has no pins, so it renders
// as a flat list. Native select on purpose (see ui/select.tsx).
export default function ModelPicker({
  catalog,
  value,
  onChange,
  disabled,
  id,
  compact,
  className,
}: Props) {
  const [query, setQuery] = useState("");
  const recommended = catalog?.recommended ?? [];
  const all = catalog?.all ?? [];

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return q ? all.filter((m) => m.id.toLowerCase().includes(q)) : all;
  }, [all, query]);
  const shown = filtered.slice(0, MAX_OPTIONS);
  const hidden = filtered.length - shown.length;

  const known =
    recommended.some((m) => m.id === value) || all.some((m) => m.id === value);
  const empty = recommended.length === 0 && all.length === 0;

  return (
    <div className={cn("flex items-center gap-2", compact ? "" : "flex-col items-stretch")}>
      {all.length > SEARCH_THRESHOLD && (
        <Input
          type="search"
          aria-label="Search models"
          placeholder="Search models"
          value={query}
          disabled={disabled}
          onChange={(e) => setQuery(e.target.value)}
          className={cn(compact ? "h-8 w-36" : "")}
        />
      )}
      <Select
        id={id}
        aria-label="Model"
        value={value}
        disabled={disabled}
        onChange={(e) => onChange(e.target.value)}
        className={cn(compact ? "h-8 max-w-64" : "w-full", className)}
      >
        <option value="" disabled>
          {empty ? "No models found" : "Select a model…"}
        </option>
        {recommended.length > 0 && (
          <optgroup label="Recommended">
            {recommended.map((m) => (
              <option key={`rec:${m.id}`} value={m.id}>
                {m.note ? `${m.label}: ${m.note}` : m.label}
              </option>
            ))}
          </optgroup>
        )}
        {all.length > 0 && (
          <optgroup label={recommended.length > 0 ? "All models" : "Models"}>
            {shown.map((m) => (
              <option key={m.id} value={m.id}>
                {m.id}
              </option>
            ))}
            {hidden > 0 && (
              <option value="" disabled>
                {`${hidden} more, keep typing to narrow`}
              </option>
            )}
          </optgroup>
        )}
        {/* Keep a stale or unlisted selection visible rather than blanking it. */}
        {value && !known && <option value={value}>{value}</option>}
      </Select>
    </div>
  );
}
