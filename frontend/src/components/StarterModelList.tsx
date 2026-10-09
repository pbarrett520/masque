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

// formatMemory renders RAM/VRAM in binary gigabytes, rounded DOWN to
// one decimal ("23.9 GB" for a 4090's 24564 MiB, "8 GB" for 8192 MiB).
// Rounding down means a wrong figure can't hide behind a tidy number;
// the exact MiB values live in Machine.detail for dev mode.
export function formatMemory(n: number): string {
  const gb = Math.floor((n / 2 ** 30) * 10) / 10;
  return `${gb} GB`;
}

// describeMachine is the one-line "what we measured" shown above the
// roster and next to the Ollama version on the Dev page, in plain
// words with the device named, so a wrong detection is visible to the
// user ("RTX 2070 Super, 8 GB" when the card is a 2070 Super).
export function describeMachine(m: ollamamgr.Machine): string {
  const ram = m.ramBytes > 0 ? `${formatMemory(m.ramBytes)} of RAM` : "";
  const name = m.gpuName || "";
  switch (m.gpuKind) {
    case "discrete": {
      const total = formatMemory(m.vramTotalBytes);
      const free =
        m.vramFreeBytes > 0
          ? ` (about ${formatMemory(m.vramFreeBytes)} free)`
          : " (free memory couldn't be read, so about 1 GB is assumed in use)";
      return `${name}, ${total}${free}${ram ? `, with ${ram}` : ""}.`;
    }
    case "unified":
      return ram
        ? `${name}, ${formatMemory(m.ramBytes)} of unified memory (about ${formatMemory(m.vramBytes)} usable by the GPU).`
        : `${name}; memory couldn't be measured, so nothing is being flagged.`;
    case "integrated":
      return ram
        ? `${name} is integrated graphics, so models run on the CPU with ${ram}.`
        : `${name} is integrated graphics; RAM couldn't be measured.`;
    case "none":
      return ram
        ? `No graphics card detected; models run on the CPU with ${ram}.`
        : "No graphics card detected and RAM couldn't be measured, so nothing is being flagged.";
    default:
      return ram
        ? `Couldn't measure the graphics card${name ? ` (${name})` : ""}; going by ${ram} alone.`
        : "Couldn't measure this machine's memory, so nothing is being flagged.";
  }
}

// fitNote is the per-model annotation for the split and tight tiers;
// gpu and unknown render nothing. Models are never hidden on fit.
function fitNote(m: ollamamgr.StarterModel): { text: string; warn: boolean } | null {
  const usable = m.vramBytes > 0 ? `${formatMemory(m.vramBytes)} of usable graphics memory` : "";
  const ram = m.ramBytes > 0 ? `${formatMemory(m.ramBytes)} of RAM` : "";
  switch (m.fit) {
    case "split":
      return {
        warn: false,
        text: usable
          ? "Will run partly on CPU; expect slower replies."
          : "Will run on the CPU; expect slower replies.",
      };
    case "tight": {
      const need = formatMemory(m.needBytes);
      const have = usable
        ? [usable, ram].filter(Boolean).join(" and ")
        : ram
          ? `${ram} and no usable graphics memory`
          : "";
      return {
        warn: true,
        text: have
          ? `Needs about ${need} of memory; this machine has ${have}.`
          : `Needs about ${need} of memory; this machine may be too small.`,
      };
    }
    default:
      return null;
  }
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
    <div>
      {models.length > 0 && (
        <p className="mb-2 text-xs text-muted-foreground">{describeMachine(models[0].machine)}</p>
      )}
      <ul className="divide-y divide-border">
        {models.map((m) => {
          const isPulling = pulling === m.ref;
          const note = fitNote(m);
          const pct =
            isPulling && progress && progress.total > 0
              ? Math.min(100, Math.round((progress.completed / progress.total) * 100))
              : null;
          return (
            <li key={m.ref} className="py-3 first:pt-0">
              <div className="flex items-baseline gap-2">
                <span className="font-medium">{m.name}</span>
                <span className="text-xs text-muted-foreground">{m.params}</span>
                {m.recommended && (
                  <span className="text-xs font-medium text-gilt">Recommended</span>
                )}
                <span className="flex-1" />
                <span className="text-xs text-muted-foreground">
                  {formatBytes(m.downloadBytes)} download
                </span>
              </div>
              <p className="mt-1 max-w-prose text-sm text-muted-foreground">{m.description}</p>
              {note && (
                <p className={`mt-1 text-xs ${note.warn ? "text-destructive" : "text-muted-foreground"}`}>
                  {note.text}
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
            </li>
          );
        })}
      </ul>
    </div>
  );
}
