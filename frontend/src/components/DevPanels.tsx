import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import { Inspect, Params, SetParams } from "../../wailsjs/go/chat/Service";
import { chat, provider } from "../../wailsjs/go/models";

// SamplerPanel edits a chat's sampler overrides (dev spec §9). Fields
// left blank stay unset so the model's own defaults apply; Clear
// removes every override.
export function SamplerPanel({
  chatId,
  onClose,
  onError,
}: {
  chatId: number;
  onClose: () => void;
  onError: (msg: string) => void;
}) {
  // Drafts are strings so partial input ("0.", "-") doesn't fight the
  // user; parsed on save.
  const [draft, setDraft] = useState<Record<string, string>>({});
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    Params(chatId)
      .then((p) => {
        setDraft({
          temperature: p.temperature?.toString() ?? "",
          topP: p.topP?.toString() ?? "",
          topK: p.topK?.toString() ?? "",
          minP: p.minP?.toString() ?? "",
          repeatPenalty: p.repeatPenalty?.toString() ?? "",
          maxTokens: p.maxTokens?.toString() ?? "",
          stop: (p.stop ?? []).join(", "),
        });
        setLoaded(true);
      })
      .catch((err) => onError(String(err)));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [chatId]);

  const num = (key: string): number | undefined => {
    const s = (draft[key] ?? "").trim();
    if (s === "") return undefined;
    const v = Number(s);
    return Number.isFinite(v) ? v : undefined;
  };

  const save = async (clear: boolean) => {
    try {
      const params = clear
        ? provider.SamplerParams.createFrom({})
        : provider.SamplerParams.createFrom({
            temperature: num("temperature"),
            topP: num("topP"),
            topK: num("topK") !== undefined ? Math.trunc(num("topK")!) : undefined,
            minP: num("minP"),
            repeatPenalty: num("repeatPenalty"),
            maxTokens:
              num("maxTokens") !== undefined ? Math.trunc(num("maxTokens")!) : undefined,
            stop: (draft.stop ?? "")
              .split(",")
              .map((s) => s.trim())
              .filter(Boolean),
          });
      await SetParams(chatId, params);
      onError("");
      onClose();
    } catch (err) {
      onError(String(err));
    }
  };

  const field = (key: string, label: string, placeholder: string) => (
    <div className="space-y-1">
      <Label htmlFor={`sampler-${key}`} className="font-mono text-xs font-normal text-muted-foreground">
        {label}
      </Label>
      <Input
        id={`sampler-${key}`}
        className="h-8 bg-background font-mono text-[0.8667rem]"
        value={draft[key] ?? ""}
        placeholder={placeholder}
        disabled={!loaded}
        onChange={(e) => setDraft((d) => ({ ...d, [key]: e.target.value }))}
      />
    </div>
  );

  return (
    <div className="rounded-md border border-border bg-card p-3">
      <div className="mb-3 flex items-baseline gap-3">
        <span className="text-sm font-medium">Sampler overrides for this chat</span>
        <span className="flex-1" />
        <span className="text-xs text-muted-foreground">
          Blank means the model's default. Unsupported params are dropped per provider.
        </span>
      </div>
      <div className="grid grid-cols-3 gap-2 sm:grid-cols-6">
        {field("temperature", "temperature", "0.8")}
        {field("topP", "top_p", "0.9")}
        {field("topK", "top_k", "40")}
        {field("minP", "min_p", "0.05")}
        {field("repeatPenalty", "repeat_penalty", "1.1")}
        {field("maxTokens", "max_tokens", "512")}
      </div>
      <div className="mt-2">{field("stop", "stop strings (comma-separated)", "\\nUser:, <|end|>")}</div>
      <div className="mt-3 flex gap-2">
        <Button size="sm" onClick={() => void save(false)} disabled={!loaded}>
          Save
        </Button>
        <Button size="sm" variant="outline" onClick={() => void save(true)} disabled={!loaded}>
          Clear overrides
        </Button>
        <Button size="sm" variant="ghost" onClick={onClose}>
          Close
        </Button>
      </div>
    </div>
  );
}

// InspectorModal shows the context inspector (dev spec §9, the flagship
// dev feature): segment breakdown with token estimates, the param
// report, and the raw request JSON, copyable.
export function InspectorModal({
  chatId,
  messageId,
  onClose,
}: {
  chatId: number;
  messageId: number;
  onClose: () => void;
}) {
  const [insp, setInsp] = useState<chat.Inspection | null>(null);
  const [error, setError] = useState("");
  const [copied, setCopied] = useState(false);
  const [openSegment, setOpenSegment] = useState(-1);

  useEffect(() => {
    Inspect(chatId, messageId)
      .then(setInsp)
      .catch((err) => setError(String(err)));
  }, [chatId, messageId]);

  const rawJSON = insp
    ? JSON.stringify(insp.rawRequest ?? null, null, 2)
    : "";

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(rawJSON);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard can be unavailable in the webview; selection still works.
    }
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-6"
      onClick={onClose}
    >
      <div
        role="dialog"
        aria-label="Context inspector"
        className="flex max-h-full w-full max-w-3xl flex-col rounded-lg border border-border bg-popover"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-3 border-b border-border px-4 py-2.5">
          <span className="font-title text-lg leading-none">Context inspector</span>
          {insp && (
            <span className="truncate font-mono text-xs text-muted-foreground">
              {insp.providerId} {insp.model}
              {insp.noStream ? " (non-streaming)" : ""}
            </span>
          )}
          <span className="flex-1" />
          <Button size="sm" variant="ghost" onClick={onClose}>
            Close
          </Button>
        </div>
        <div className="min-h-0 flex-1 space-y-5 overflow-y-auto p-4">
          {error && <p className="text-sm text-muted-foreground">{error}</p>}
          {insp && (
            <>
              <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-0.5 text-sm">
                <dt className="text-muted-foreground">Context window</dt>
                <dd className="tabular-nums">{insp.contextWindow} tokens</dd>
                <dt className="text-muted-foreground">Reserved for output</dt>
                <dd className="tabular-nums">{insp.reservedOutput} tokens</dd>
                <dt className="text-muted-foreground">System</dt>
                <dd className="tabular-nums">about {insp.systemTokens} tokens</dd>
                <dt className="text-muted-foreground">History</dt>
                <dd className="tabular-nums">
                  about {insp.historyTokens} tokens
                  {insp.droppedMessages > 0 && (
                    <span className="text-destructive">
                      {" "}
                      ({insp.droppedMessages} older message
                      {insp.droppedMessages === 1 ? "" : "s"} truncated)
                    </span>
                  )}
                </dd>
              </dl>

              <div>
                <p className="mb-1 text-sm font-medium">Segments</p>
                <ul className="divide-y divide-border rounded-md border border-border">
                  {(insp.segments ?? []).map((seg, i) => (
                    <li key={i}>
                      <button
                        className="flex w-full items-center gap-3 px-3 py-1.5 text-left text-sm hover:bg-accent/60"
                        aria-expanded={openSegment === i}
                        onClick={() => setOpenSegment(openSegment === i ? -1 : i)}
                      >
                        <span className="w-16 shrink-0 text-xs text-muted-foreground">
                          {seg.name}
                        </span>
                        <span className="truncate font-mono text-xs">{seg.source}</span>
                        <span className="flex-1" />
                        <span className="shrink-0 text-xs tabular-nums text-muted-foreground">
                          about {seg.tokens} tokens
                        </span>
                      </button>
                      {openSegment === i && (
                        <pre className="max-h-48 overflow-y-auto whitespace-pre-wrap border-t border-border bg-background p-3 font-mono text-xs">
                          {seg.content}
                        </pre>
                      )}
                    </li>
                  ))}
                </ul>
              </div>

              <div>
                <p className="mb-1 text-sm font-medium">Sampler params</p>
                <p className="text-sm text-muted-foreground">
                  Sent:{" "}
                  <span className="font-mono text-xs">
                    {insp.paramReport && Object.keys(insp.paramReport.sent ?? {}).length
                      ? JSON.stringify(insp.paramReport.sent)
                      : "none (model defaults)"}
                  </span>
                </p>
                {insp.paramReport?.dropped?.length ? (
                  <p className="text-sm text-muted-foreground">
                    Dropped as unsupported:{" "}
                    <span className="font-mono text-xs text-destructive">
                      {insp.paramReport.dropped.join(", ")}
                    </span>
                  </p>
                ) : null}
              </div>

              <div>
                <div className="mb-1 flex items-center gap-3">
                  <p className="text-sm font-medium">Raw request</p>
                  {insp.requestUrl && (
                    <span className="truncate font-mono text-xs text-muted-foreground">
                      POST {insp.requestUrl}
                    </span>
                  )}
                  <span className="flex-1" />
                  <Button size="sm" variant="outline" onClick={() => void copy()}>
                    {copied ? "Copied" : "Copy JSON"}
                  </Button>
                </div>
                <pre className="max-h-64 overflow-auto rounded-md border border-border bg-background p-3 font-mono text-xs">
                  {rawJSON}
                </pre>
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
