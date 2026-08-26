import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import {
  CancelPull,
  Pull,
  PullInFlight,
  Recommended,
} from "../../wailsjs/go/ollamamgr/Service";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { ollamamgr } from "../../wailsjs/go/models";

// Payload of the ollama:pull event (ollamamgr.PullProgress in Go; not in
// generated models because it only travels via events).
interface PullProgress {
  ref: string;
  status: string;
  total: number;
  completed: number;
  done: boolean;
  error: string;
}

export function formatBytes(n: number): string {
  if (n >= 1e9) return `${(n / 1e9).toFixed(1)} GB`;
  if (n >= 1e6) return `${Math.round(n / 1e6)} MB`;
  return `${n} B`;
}

interface Props {
  // Called when the user picks an installed model (onboarding). Absent
  // in settings, where the list is manage-only.
  onUse?: (ref: string) => void;
  onError?: (msg: string) => void;
}

// StarterModelList renders the curated roster (dev spec §8 simple
// mode): one card per model with fit guidance, a pull button with live
// progress, and — when onUse is given — a "Use this model" action once
// installed.
export default function StarterModelList({ onUse, onError }: Props) {
  const [models, setModels] = useState<ollamamgr.StarterModel[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [pulling, setPulling] = useState(""); // ref being downloaded
  const [progress, setProgress] = useState<PullProgress | null>(null);
  // Refreshing the roster after a pull finishes needs the latest
  // callback without resubscribing the event listener.
  const refreshRef = useRef(() => {});

  const refresh = useCallback(async () => {
    try {
      setModels((await Recommended()) ?? []);
      setLoaded(true);
      setPulling(await PullInFlight());
    } catch (err) {
      onError?.(String(err));
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  refreshRef.current = refresh;

  useEffect(() => {
    void refresh();
    const off = EventsOn("ollama:pull", (p: PullProgress) => {
      if (p.done || p.error) {
        setPulling("");
        setProgress(null);
        if (p.error && p.error !== "canceled") onError?.(`Download failed: ${p.error}`);
        void refreshRef.current();
      } else {
        setPulling(p.ref);
        setProgress(p);
      }
    });
    return off;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const startPull = async (ref: string) => {
    onError?.("");
    try {
      await Pull(ref);
      setPulling(ref);
      setProgress(null);
    } catch (err) {
      onError?.(String(err));
    }
  };

  if (!loaded) {
    return <p className="text-sm text-muted-foreground">Loading models…</p>;
  }

  return (
    <div className="space-y-2">
      {models.map((m) => {
        const isPulling = pulling === m.ref;
        const pct =
          isPulling && progress && progress.total > 0
            ? Math.min(100, Math.round((progress.completed / progress.total) * 100))
            : null;
        return (
          <div
            key={m.ref}
            className={
              "rounded-md border p-3 " +
              (m.recommended ? "border-primary/60" : "border-border")
            }
          >
            <div className="flex items-center gap-2">
              <span className="font-medium">{m.name}</span>
              <span className="text-xs text-muted-foreground">{m.params}</span>
              {m.recommended && (
                <span className="rounded bg-primary/15 px-1.5 py-0.5 text-xs text-primary">
                  recommended
                </span>
              )}
              <span className="flex-1" />
              <span className="text-xs text-muted-foreground">
                {formatBytes(m.downloadBytes)} download
              </span>
            </div>
            <p className="mt-1 text-sm text-muted-foreground">{m.description}</p>
            {!m.fits && (
              <p className="mt-1 text-xs text-amber-500">
                Needs about {formatBytes(m.minRamBytes)} of memory — this machine
                may be too small to run it well.
              </p>
            )}
            <div className="mt-2 flex items-center gap-2">
              {m.installed ? (
                <>
                  <span className="text-xs text-muted-foreground">Installed</span>
                  {onUse && (
                    <Button size="sm" onClick={() => onUse(m.ref)}>
                      Use this model
                    </Button>
                  )}
                </>
              ) : isPulling ? (
                <div className="flex flex-1 items-center gap-2">
                  <div className="h-2 flex-1 overflow-hidden rounded bg-muted">
                    <div
                      className="h-full bg-primary transition-all"
                      style={{ width: `${pct ?? 0}%` }}
                    />
                  </div>
                  <span className="w-24 text-right text-xs text-muted-foreground">
                    {pct !== null && progress
                      ? `${formatBytes(progress.completed)} · ${pct}%`
                      : (progress?.status ?? "starting…")}
                  </span>
                  <Button size="sm" variant="outline" onClick={() => CancelPull()}>
                    Cancel
                  </Button>
                </div>
              ) : (
                <Button
                  size="sm"
                  variant={m.recommended ? "default" : "outline"}
                  disabled={pulling !== ""}
                  onClick={() => void startPull(m.ref)}
                >
                  Download
                </Button>
              )}
            </div>
          </div>
        );
      })}
    </div>
  );
}
