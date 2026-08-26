import { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Get, Set } from "../../wailsjs/go/settings/Service";
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
    <Card>
      <CardHeader>
        <CardTitle>Model manager</CardTitle>
        <CardDescription>
          {status === null
            ? "Checking Ollama…"
            : status.reachable
              ? `Ollama ${status.version} at ${status.baseUrl}`
              : `Ollama isn't reachable at ${status.baseUrl}`}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="space-y-1.5">
          <Label htmlFor="dev-pull">Pull by name</Label>
          <div className="flex gap-2">
            <Input
              id="dev-pull"
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
            <div className="flex items-center gap-2">
              <span className="max-w-64 truncate font-mono text-xs">{pulling}</span>
              <div className="h-2 flex-1 overflow-hidden rounded bg-muted">
                <div
                  className="h-full bg-primary transition-all"
                  style={{ width: `${pct ?? 0}%` }}
                />
              </div>
              <span className="w-28 text-right text-xs text-muted-foreground">
                {pct !== null && progress
                  ? `${formatBytes(progress.completed)} · ${pct}%`
                  : (progress?.status ?? "starting…")}
              </span>
              <Button size="sm" variant="outline" onClick={() => CancelPull()}>
                Cancel
              </Button>
            </div>
          )}
        </div>

        {loaded.length > 0 && (
          <div className="space-y-1">
            <p className="text-sm font-medium">Loaded now</p>
            {loaded.map((m) => (
              <div
                key={m.name}
                className="flex items-center gap-2 rounded-md border border-border px-2 py-1 text-sm"
              >
                <span className="truncate font-mono text-xs">{m.name}</span>
                <span className="flex-1" />
                <span className="text-xs text-muted-foreground">
                  {formatBytes(m.size)} ({formatBytes(m.sizeVram)} VRAM)
                </span>
              </div>
            ))}
          </div>
        )}

        <div className="space-y-1">
          <div className="flex items-center">
            <p className="text-sm font-medium">Installed ({models.length})</p>
            <span className="flex-1" />
            <Button size="sm" variant="outline" onClick={() => void refresh()}>
              Refresh
            </Button>
          </div>
          {models.map((m) => (
            <div
              key={m.id}
              className="flex items-center gap-2 rounded-md border border-border px-2 py-1 text-sm"
            >
              <span className="truncate font-mono text-xs">{m.id}</span>
              {m.quant && (
                <span className="rounded bg-muted px-1 text-xs text-muted-foreground">
                  {m.quant}
                </span>
              )}
              {m.family && (
                <span className="text-xs text-muted-foreground">{m.family}</span>
              )}
              <span className="flex-1" />
              <span className="text-xs text-muted-foreground">{formatBytes(m.size)}</span>
              <button
                className="text-xs text-destructive hover:underline"
                onClick={() => void remove(m)}
              >
                delete
              </button>
            </div>
          ))}
        </div>
      </CardContent>
    </Card>
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
      await Set(settingKey, trimmed === "" ? null : n);
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
      await Set("dev.streaming", on ? null : false);
      setStreaming(on);
      onStatus(on ? "Streaming enabled." : "Streaming disabled — replies arrive whole.");
    } catch (err) {
      onStatus(String(err));
    }
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>Endpoint config</CardTitle>
        <CardDescription>
          Applies to every provider. Endpoint URLs and keys live in Settings.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <NumberSetting
          settingKey="dev.request_timeout_secs"
          label="Request timeout (seconds, blank = none)"
          placeholder="120"
          onStatus={onStatus}
        />
        <div className="space-y-1.5">
          <Label>Streaming</Label>
          <div className="flex gap-2">
            <Button
              variant={streaming ? "default" : "outline"}
              size="sm"
              onClick={() => void setStream(true)}
            >
              On
            </Button>
            <Button
              variant={!streaming ? "default" : "outline"}
              size="sm"
              onClick={() => void setStream(false)}
            >
              Off
            </Button>
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
      </CardContent>
    </Card>
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
    <Card>
      <CardHeader>
        <CardTitle>Request log</CardTitle>
        <CardDescription>
          Last {entries.length} provider request{entries.length === 1 ? "" : "s"} this
          session (in memory only, keys never logged).
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-2">
        <div className="flex gap-2">
          <Button size="sm" variant="outline" onClick={refresh}>
            Refresh
          </Button>
          <Button size="sm" variant="outline" onClick={() => void clear()}>
            Clear
          </Button>
        </div>
        {entries.length === 0 && (
          <p className="text-sm text-muted-foreground">No requests yet.</p>
        )}
        {entries.map((e, i) => (
          <div key={e.id} className="rounded-md border border-border">
            <button
              className="flex w-full items-center gap-2 px-2 py-1 text-left text-sm hover:bg-muted/50"
              onClick={() => setOpen(open === i ? -1 : i)}
            >
              <span className="text-xs text-muted-foreground">{fmtTime(e.time)}</span>
              <span className="font-mono text-xs">
                {e.providerId} · {e.model}
              </span>
              <span
                className={
                  "rounded px-1 text-xs " +
                  (e.status === "ok"
                    ? "bg-primary/15 text-primary"
                    : e.status === "canceled"
                      ? "bg-muted text-muted-foreground"
                      : "bg-destructive/15 text-destructive")
                }
              >
                {e.status}
              </span>
              <span className="flex-1" />
              {e.usage && (
                <span className="text-xs text-muted-foreground">
                  {e.usage.promptTokens}→{e.usage.completionTokens} tok
                </span>
              )}
              <span className="text-xs text-muted-foreground">{e.durationMs} ms</span>
            </button>
            {open === i && (
              <div className="space-y-2 border-t border-border p-2">
                {e.error && <p className="text-xs text-destructive">{e.error}</p>}
                <p className="font-mono text-xs text-muted-foreground">POST {e.url}</p>
                <pre className="max-h-48 overflow-auto rounded bg-muted/30 p-2 text-xs">
                  {JSON.stringify(e.request, null, 2)}
                </pre>
                {e.response && (
                  <pre className="max-h-48 overflow-auto whitespace-pre-wrap rounded bg-muted/30 p-2 text-xs">
                    {e.response}
                    {e.truncated ? "\n… (truncated)" : ""}
                  </pre>
                )}
              </div>
            )}
          </div>
        ))}
      </CardContent>
    </Card>
  );
}

export default function DevScreen() {
  const [status, setStatus] = useState("");
  return (
    <div className="mx-auto max-w-2xl space-y-6">
      <ModelManagerCard onStatus={setStatus} />
      <EndpointConfigCard onStatus={setStatus} />
      <LogViewCard onStatus={setStatus} />
      {status && <p className="text-sm text-muted-foreground">{status}</p>}
    </div>
  );
}
