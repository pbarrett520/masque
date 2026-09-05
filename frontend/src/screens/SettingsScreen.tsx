import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Label } from "@/components/ui/label";
import { Section } from "@/components/ui/section";
import { Segmented } from "@/components/ui/segmented";
import { DBPath, Get } from "../../wailsjs/go/settings/Service";
import { setSetting } from "@/lib/settings";
import { Persona, SetPersona } from "../../wailsjs/go/chat/Service";
import {
  Delete as DeleteModel,
  Installed,
  Status,
} from "../../wailsjs/go/ollamamgr/Service";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { ollamamgr, provider } from "../../wailsjs/go/models";
import StarterModelList, { formatBytes } from "@/components/StarterModelList";

type Theme = "light" | "dark";

interface Props {
  theme: Theme;
  onThemeChange: (t: Theme) => void;
  dev: boolean;
  onDevChange: (on: boolean) => void;
}

interface FieldSpec {
  key: string; // settings key
  label: string;
  placeholder?: string;
  secret?: boolean; // render as a password field
}

// SettingField loads, edits, and saves one string setting. An emptied
// field deletes the key.
function SettingField({
  spec,
  onStatus,
}: {
  spec: FieldSpec;
  onStatus: (s: string) => void;
}) {
  const [value, setValue] = useState("");
  const [saved, setSaved] = useState("");
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    Get(spec.key)
      .then((v) => {
        if (typeof v === "string") {
          setValue(v);
          setSaved(v);
        }
        setLoaded(true);
      })
      .catch((err) => onStatus(`Failed to load ${spec.label}: ${err}`));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [spec.key]);

  const save = async () => {
    try {
      const trimmed = value.trim();
      await setSetting(spec.key, trimmed === "" ? null : trimmed);
      setValue(trimmed);
      setSaved(trimmed);
      onStatus("Saved. Applies to the next request.");
    } catch (err) {
      onStatus(`Failed to save ${spec.label}: ${err}`);
    }
  };

  return (
    <div className="space-y-1.5">
      <Label htmlFor={spec.key}>{spec.label}</Label>
      <div className="flex gap-2">
        <Input
          id={spec.key}
          type={spec.secret ? "password" : "text"}
          value={value}
          placeholder={spec.placeholder}
          disabled={!loaded}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && save()}
        />
        <Button onClick={save} disabled={!loaded || value === saved}>
          Save
        </Button>
      </div>
    </div>
  );
}

// PersonaCard edits the default persona (name drives {{user}}; the
// description is added to the system prompt).
function PersonaCard({ onStatus }: { onStatus: (s: string) => void }) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [saved, setSaved] = useState({ name: "", description: "" });
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    Persona()
      .then((p) => {
        setName(p.name ?? "");
        setDescription(p.description ?? "");
        setSaved({ name: p.name ?? "", description: p.description ?? "" });
        setLoaded(true);
      })
      .catch((err) => onStatus(`Failed to load persona: ${err}`));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const save = async () => {
    try {
      await SetPersona(name, description);
      setSaved({ name: name.trim(), description: description.trim() });
      onStatus("Persona saved. Applies to the next message.");
    } catch (err) {
      onStatus(`Failed to save persona: ${err}`);
    }
  };

  const dirty = name !== saved.name || description !== saved.description;

  return (
    <Section
      title="Persona"
      description={
        <>
          Who you are in chats: the name replaces {"{{user}}"}, the
          description is shown to the character.
        </>
      }
    >
      <div className="space-y-3">
        <div className="space-y-1.5">
          <Label htmlFor="persona-name">Name</Label>
          <Input
            id="persona-name"
            value={name}
            placeholder="How should characters address you?"
            disabled={!loaded}
            onChange={(e) => setName(e.target.value)}
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="persona-description">Description</Label>
          <Textarea
            id="persona-description"
            className="max-h-64 min-h-20"
            value={description}
            placeholder="A few words about who the characters are talking to (optional)"
            disabled={!loaded}
            onChange={(e) => setDescription(e.target.value)}
          />
        </div>
        <Button onClick={save} disabled={!loaded || !name.trim() || !dirty}>
          Save
        </Button>
      </div>
    </Section>
  );
}

// LocalModelsCard is the simple-mode Ollama manager (dev spec §8):
// endpoint status, installed models with delete, and the curated
// starter roster with one-click pulls. The full manager (all quants,
// pull-by-name, HF refs, VRAM) arrives with dev mode in M1.7.
function LocalModelsCard({ onStatus }: { onStatus: (s: string) => void }) {
  const [status, setStatus] = useState<ollamamgr.Status | null>(null);
  const [installed, setInstalled] = useState<provider.ModelInfo[]>([]);

  const refresh = async () => {
    try {
      const s = await Status();
      setStatus(s);
      setInstalled(s.reachable ? ((await Installed()) ?? []) : []);
    } catch (err) {
      onStatus(String(err));
    }
  };

  useEffect(() => {
    void refresh();
    // A finished pull changes the installed list.
    const off = EventsOn("ollama:pull", (p: { done: boolean; error: string }) => {
      if (p.done) void refresh();
    });
    return off;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const remove = async (m: provider.ModelInfo) => {
    if (!window.confirm(`Delete ${m.id}? This frees ${formatBytes(m.size)} on disk.`)) {
      return;
    }
    try {
      await DeleteModel(m.id);
      onStatus(`Deleted ${m.id}.`);
      void refresh();
    } catch (err) {
      onStatus(String(err));
    }
  };

  return (
    <Section
      title="Local models"
      description={
        status === null
          ? "Checking Ollama…"
          : status.reachable
            ? `Ollama ${status.version} is running at ${status.baseUrl}.`
            : `Ollama isn't reachable at ${status.baseUrl}. Is it running?`
      }
    >
      <div className="space-y-5">
        {status?.reachable && installed.length > 0 && (
          <div>
            <p className="mb-1 text-sm font-medium">Installed</p>
            <ul className="divide-y divide-border">
              {installed.map((m) => (
                <li
                  key={m.id}
                  className="flex items-center gap-3 py-1.5 text-sm"
                >
                  <span className="truncate font-mono text-[0.8667rem]">{m.id}</span>
                  <span className="flex-1" />
                  <span className="shrink-0 text-xs text-muted-foreground">
                    {formatBytes(m.size)}
                  </span>
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
        )}
        {status?.reachable && (
          <div>
            <p className="mb-1 text-sm font-medium">Recommended for roleplay</p>
            <StarterModelList onError={onStatus} />
          </div>
        )}
      </div>
    </Section>
  );
}

export default function SettingsScreen({ theme, onThemeChange, dev, onDevChange }: Props) {
  const [dbPath, setDbPath] = useState("");
  const [status, setStatus] = useState("");

  useEffect(() => {
    DBPath()
      .then(setDbPath)
      .catch(() => {});
  }, []);

  return (
    <div className="mx-auto max-w-2xl">
      <h2 className="font-title text-2xl leading-none">Settings</h2>
      {/* Status sits under the title, where it is seen; the page scrolls. */}
      <p className="mb-6 mt-2 min-h-5 text-sm text-muted-foreground" role="status">
        {status}
      </p>
      <div className="space-y-6">
        <PersonaCard onStatus={setStatus} />

        <Section
          title="Ollama"
          description="Local inference endpoint. Leave empty for the default, http://localhost:11434."
        >
          <SettingField
            spec={{
              key: "provider.ollama.base_url",
              label: "Base URL",
              placeholder: "http://localhost:11434",
            }}
            onStatus={setStatus}
          />
        </Section>

        <LocalModelsCard onStatus={setStatus} />

        <Section
          title="OpenAI-compatible"
          description="OpenRouter, LM Studio, vLLM, llama.cpp server, or OpenAI. The base URL includes /v1, for example https://openrouter.ai/api/v1. Local servers usually need no key."
        >
          <div className="space-y-3">
            <SettingField
              spec={{
                key: "provider.openai.base_url",
                label: "Base URL",
                placeholder: "https://openrouter.ai/api/v1",
              }}
              onStatus={setStatus}
            />
            <SettingField
              spec={{
                key: "provider.openai.api_key",
                label: "API key",
                placeholder: "sk-or-…",
                secret: true,
              }}
              onStatus={setStatus}
            />
          </div>
        </Section>

        <Section title="Anthropic" description="Direct Claude API access.">
          <SettingField
            spec={{
              key: "provider.anthropic.api_key",
              label: "API key",
              placeholder: "sk-ant-…",
              secret: true,
            }}
            onStatus={setStatus}
          />
        </Section>

        <Section
          title="Developer mode"
          description="Adds the context inspector, sampler panel, full model manager, endpoint config, and request log. Instant, no restart."
        >
          <Segmented
            aria-label="Developer mode"
            value={dev ? "on" : "off"}
            options={[
              { value: "off", label: "Off" },
              { value: "on", label: "On" },
            ]}
            onChange={(v) => onDevChange(v === "on")}
          />
        </Section>

        <Section title="Appearance" description="Applied and saved immediately.">
          <Segmented
            aria-label="Theme"
            value={theme}
            options={[
              { value: "light", label: "Light" },
              { value: "dark", label: "Dark" },
            ]}
            onChange={onThemeChange}
          />
        </Section>

        <div className="border-t border-border pt-6">
          <p className="text-xs text-muted-foreground">
            Database: <span className="font-mono">{dbPath || "…"}</span>
          </p>
        </div>
      </div>
    </div>
  );
}
