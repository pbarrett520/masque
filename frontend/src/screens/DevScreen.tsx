import { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Section } from "@/components/ui/section";
import { Segmented } from "@/components/ui/segmented";
import { Get } from "../../wailsjs/go/settings/Service";
import { setSetting } from "@/lib/settings";
import {
  All,
  CancelPull,
  Delete as DeleteModel,
  Loaded,
  Pull,
  PullInFlight,
  Status,
} from "../../wailsjs/go/ollamamgr/Service";
import { Clear, Entries } from "../../wailsjs/go/devlog/Service";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { devlog, ollama, ollamamgr, provider } from "../../wailsjs/go/models";
import { formatBytes } from "@/components/StarterModelList";

interface PullProgress {
  ref: string;
  status: string;
  total: number;
  completed: number;
  done: boolean;
  error: string;
}

// ModelManagerCard is the full Ollama manager (dev spec §9): complete
// model list including non-chat models, pull-by-name (hf.co refs
// included), delete, and loaded/VRAM status.
function ModelManagerCard({ onStatus }: { onStatus: (s: string) => void }) {
  const [status, setStatus] = useState<ollamamgr.Status | null>(null);
  const [models, setModels] = useState<provider.ModelInfo[]>([]);
  const [loaded, setLoaded] = useState<ollama.LoadedModel[]>([]);
  const [pullRef, setPullRef] = useState("");
  const [pulling, setPulling] = useState("");
  const [progress, setProgress] = useState<PullProgress | null>(null);

  const refresh = useCallback(async () => {
    try {
      const s = await Status();
      setStatus(s);
      if (s.reachable) {
        setModels((await All()) ?? []);
        setLoaded((await Loaded()) ?? []);
      } else {
        setModels([]);
        setLoaded([]);
      }
      setPulling(await PullInFlight());
    } catch (err) {
      onStatus(String(err));
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    void refresh();
    const off = EventsOn("ollama:pull", (p: PullProgress) => {
      if (p.done || p.error) {
        setPulling("");
        setProgress(null);
        if (p.error && p.error !== "canceled") onStatus(`Pull failed: ${p.error}`);
        void refresh();
      } else {
        setPulling(p.ref);
        setProgress(p);
      }
    });
    return off;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const startPull = async () => {
    const ref = pullRef.trim();
    if (!ref) return;
    onStatus("");
    try {
      await Pull(ref);
      setPulling(ref);
      setProgress(null);
    } catch (err) {
      onStatus(String(err));
    }
  };

  const remove = async (m: provider.ModelInfo) => {
    if (!window.confirm(`Delete ${m.id}? This frees ${formatBytes(m.size)} on disk.`)) return;
    try {
      await DeleteModel(m.id);
      onStatus(`Deleted ${m.id}.`);
      void refresh();
    } catch (err) {
      onStatus(String(err));
    }
  };

  const pct =
    progress && progress.total > 0
      ? Math.min(100, Math.round((progress.completed / progress.total) * 100))
      : null;

  return (
    <Section
      title="Model manager"
      description={
        status === null
          ? "Checking Ollama…"
          : status.reachable
            ? `Ollama ${status.version} is running at ${status.baseUrl}.`
            : `Ollama isn't reachable at ${status.baseUrl}.`
      }
    >
      <div className="space-y-5">
        <div className="space-y-1.5">
          <Label htmlFor="dev-pull">Pull by name</Label>
          <div className="flex gap-2">
            <Input
              id="dev-pull"
              className="font-mono text-[0.8667rem]"
              placeholder="llama3.1:8b or hf.co/{user}/{repo}:Q4_K_M"
              value={pullRef}
              disabled={pulling !== ""}
              onChange={(e) => setPullRef(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && void startPull()}
            />
            <Button onClick={() => void startPull()} disabled={pulling !== "" || !pullRef.trim()}>
              Pull
            </Button>
          </div>
          {pulling && (
            <div className="flex items-center gap-2 pt-1">
              <span className="max-w-64 truncate font-mono text-xs">{pulling}</span>
              <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-muted">
                <div
                  className="h-full bg-gilt transition-[width]"
                  style={{ width: `${pct ?? 0}%` }}
                />
              </div>
              <span className="w-28 text-right text-xs tabular-nums text-muted-foreground">
                {pct !== null && progress
                  ? `${formatBytes(progress.completed)}, ${pct}%`
                  : (progress?.status ?? "Starting…")}
              </span>
              <Button size="sm" variant="outline" onClick={() => CancelPull()}>
                Cancel
              </Button>
            </div>
          )}
        </div>

        {loaded.length > 0 && (
          <div>
            <p className="mb-1 text-sm font-medium">Loaded now</p>
            <ul className="divide-y divide-border">
              {loaded.map((m) => (
                <li key={m.name} className="flex items-center gap-3 py-1.5 text-sm">
                  <span className="truncate font-mono text-[0.8667rem]">{m.name}</span>
                  <span className="flex-1" />
                  <span className="shrink-0 text-xs text-muted-foreground">
                    {formatBytes(m.size)}, {formatBytes(m.sizeVram)} in VRAM
                  </span>
                </li>
              ))}
            </ul>
          </div>
        )}

        <div>
          <div className="mb-1 flex items-center">
            <p className="text-sm font-medium">Installed ({models.length})</p>
            <span className="flex-1" />
            <Button size="sm" variant="ghost" onClick={() => void refresh()}>
              Refresh
            </Button>
          </div>
          <ul className="divide-y divide-border">
            {models.map((m) => (
              <li key={m.id} className="flex items-center gap-3 py-1.5 text-sm">
                <span className="truncate font-mono text-[0.8667rem]">{m.id}</span>
                {m.quant && (
                  <span className="rounded-sm bg-muted px-1 font-mono text-[0.7333rem] text-muted-foreground">
                    {m.quant}
                  </span>
                )}
                {m.family && (
                  <span className="shrink-0 text-xs text-muted-foreground">{m.family}</span>
                )}
                <span className="flex-1" />
                <span className="shrink-0 text-xs text-muted-foreground">{formatBytes(m.size)}</span>
                <button
                  className="shrink-0 text-xs text-muted-foreground hover:text-destructive"
                  onClick={() => void remove(m)}
                >
                  Delete
                </button>
              </li>
            ))}
          </ul>
        </div>
      </div>
    </Section>
  );
}

// NumberSetting edits one numeric settings key; blank clears it.
function NumberSetting({
  settingKey,
  label,
  placeholder,
  onStatus,
}: {
  settingKey: string;
  label: string;
  placeholder: string;
  onStatus: (s: string) => void;
}) {
  const [value, setValue] = useState("");
  const [saved, setSaved] = useState("");
  const [loadedFlag, setLoadedFlag] = useState(false);

  useEffect(() => {
    Get(settingKey)
      .then((v) => {
        const s = typeof v === "number" ? String(v) : "";
        setValue(s);
        setSaved(s);
        setLoadedFlag(true);
      })
      .catch((err) => onStatus(String(err)));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [settingKey]);

  const save = async () => {
    const trimmed = value.trim();
    const n = Number(trimmed);
    if (trimmed !== "" && (!Number.isFinite(n) || n < 0)) {
      onStatus(`${label} must be a non-negative number`);
      return;
    }
    try {
      await setSetting(settingKey, trimmed === "" ? null : n);
      setSaved(trimmed);
      setValue(trimmed);
      onStatus("Saved. Applies to the next request.");
    } catch (err) {
      onStatus(String(err));
    }
  };

  return (
    <div className="space-y-1.5">
      <Label htmlFor={settingKey}>{label}</Label>
      <div className="flex gap-2">
        <Input
          id={settingKey}
          value={value}
          placeholder={placeholder}
          disabled={!loadedFlag}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && void save()}
        />
        <Button onClick={() => void save()} disabled={!loadedFlag || value === saved}>
          Save
        </Button>
      </div>
    </div>
  );
}

// EndpointConfigCard: request timeout, streaming toggle, and the cloud
// context-window overrides (dev spec §9; §5 "static table + user
// override").
function EndpointConfigCard({ onStatus }: { onStatus: (s: string) => void }) {
  const [streaming, setStreaming] = useState(true);

  useEffect(() => {
    Get("dev.streaming")
      .then((v) => setStreaming(v !== false))
      .catch(() => {});
  }, []);

  const setStream = async (on: boolean) => {
    try {
      // Unset means streaming on; only store an explicit false.
      await setSetting("dev.streaming", on ? null : false);
      setStreaming(on);
      onStatus(on ? "Streaming enabled." : "Streaming disabled — replies arrive whole.");
    } catch (err) {
      onStatus(String(err));
    }
  };

  return (
    <Section
      title="Endpoint config"
      description="Applies to every provider. Endpoint URLs and keys live in Settings."
    >
      <div className="space-y-3">
        <NumberSetting
          settingKey="dev.request_timeout_secs"
          label="Request timeout (seconds, blank = none)"
          placeholder="120"
          onStatus={onStatus}
        />
        <div className="space-y-1.5">
          <Label>Streaming</Label>
          <div>
            <Segmented
              aria-label="Streaming"
              size="sm"
              value={streaming ? "on" : "off"}
              options={[
                { value: "on", label: "On" },
                { value: "off", label: "Off" },
              ]}
              onChange={(v) => void setStream(v === "on")}
            />
          </div>
        </div>
        <NumberSetting
          settingKey="provider.openai.context_window"
          label="OpenAI-compat context window override (tokens)"
          placeholder="16384 (default)"
          onStatus={onStatus}
        />
        <NumberSetting
          settingKey="provider.anthropic.context_window"
          label="Anthropic context window override (tokens)"
          placeholder="200000 (default)"
          onStatus={onStatus}
        />
      </div>
    </Section>
  );
}

// LogViewCard renders the in-memory ring of provider requests (dev
// spec §9): newest first, expandable request/response bodies.
function LogViewCard({ onStatus }: { onStatus: (s: string) => void }) {
  const [entries, setEntries] = useState<devlog.Entry[]>([]);
  const [open, setOpen] = useState(-1);

  const refresh = useCallback(() => {
    Entries()
      .then((e) => setEntries(e ?? []))
      .catch((err) => onStatus(String(err)));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const clear = async () => {
    await Clear();
    setOpen(-1);
    refresh();
  };

  const fmtTime = (unix: number) => new Date(unix * 1000).toLocaleTimeString();

  return (
    <Section
      title="Request log"
      description={
        (entries.length
          ? `Last ${entries.length} provider request${entries.length === 1 ? "" : "s"} this session. `
          : "Provider requests from this session appear here. ") +
        "Kept in memory only; keys are never logged."
      }
    >
      <div className="space-y-3">
        <div className="flex gap-2">
          <Button size="sm" variant="outline" onClick={refresh}>
            Refresh
          </Button>
          <Button size="sm" variant="ghost" onClick={() => void clear()}>
            Clear
          </Button>
        </div>
        {entries.length === 0 && (
          <p className="text-sm text-muted-foreground">No requests yet.</p>
        )}
        {entries.length > 0 && (
          <ul className="divide-y divide-border rounded-md border border-border">
            {entries.map((e, i) => (
              <li key={e.id}>
                <button
                  className="flex w-full items-center gap-3 px-3 py-1.5 text-left text-sm hover:bg-accent/60"
                  aria-expanded={open === i}
                  onClick={() => setOpen(open === i ? -1 : i)}
                >
                  <span className="text-xs tabular-nums text-muted-foreground">{fmtTime(e.time)}</span>
                  <span className="truncate font-mono text-[0.8rem]">
                    {e.providerId} {e.model}
                  </span>
                  <span
                    className={
                      "text-xs " +
                      (e.status === "ok"
                        ? "text-gilt"
                        : e.status === "canceled"
                          ? "text-muted-foreground"
                          : "text-destructive")
                    }
                  >
                    {e.status}
                  </span>
                  <span className="flex-1" />
                  {e.usage && (
                    <span className="text-xs tabular-nums text-muted-foreground">
                      {e.usage.promptTokens} in, {e.usage.completionTokens} out
                    </span>
                  )}
                  <span className="text-xs tabular-nums text-muted-foreground">{e.durationMs} ms</span>
                </button>
                {open === i && (
                  <div className="space-y-2 border-t border-border bg-card/60 p-3">
                    {e.error && <p className="text-xs text-destructive">{e.error}</p>}
                    <p className="font-mono text-xs text-muted-foreground">POST {e.url}</p>
                    <pre className="max-h-48 overflow-auto rounded-md bg-background p-2 font-mono text-xs">
                      {JSON.stringify(e.request, null, 2)}
                    </pre>
                    {e.response && (
                      <pre className="max-h-48 overflow-auto whitespace-pre-wrap rounded-md bg-background p-2 font-mono text-xs">
                        {e.response}
                        {e.truncated ? "\n… (truncated)" : ""}
                      </pre>
                    )}
                  </div>
                )}
              </li>
            ))}
          </ul>
        )}
      </div>
    </Section>
  );
}

export default function DevScreen() {
  const [status, setStatus] = useState("");
  return (
    <div className="mx-auto max-w-2xl">
      <h2 className="font-title text-2xl leading-none">Developer mode</h2>
      {/* Status sits under the title, where it is seen; the page scrolls. */}
      <p className="mb-6 mt-2 min-h-5 text-sm text-muted-foreground" role="status">
        {status}
      </p>
      <div className="space-y-6">
        <ModelManagerCard onStatus={setStatus} />
        <EndpointConfigCard onStatus={setStatus} />
        <LogViewCard onStatus={setStatus} />
      </div>
    </div>
  );
}
