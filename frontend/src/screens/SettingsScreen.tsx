import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Label } from "@/components/ui/label";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { DBPath, Get, Set } from "../../wailsjs/go/settings/Service";
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
// field deletes the key (Set(key, null)).
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
      await Set(spec.key, trimmed === "" ? null : trimmed);
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
    <Card>
      <CardHeader>
        <CardTitle>Persona</CardTitle>
        <CardDescription>
          Who you are in chats: the name replaces {"{{user}}"}, the
          description is shown to the character.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
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
      </CardContent>
    </Card>
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
    <Card>
      <CardHeader>
        <CardTitle>Local models</CardTitle>
        <CardDescription>
          {status === null
            ? "Checking Ollama…"
            : status.reachable
              ? `Ollama ${status.version} at ${status.baseUrl}`
              : `Ollama isn't reachable at ${status.baseUrl} — is it running?`}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {status?.reachable && installed.length > 0 && (
          <div className="space-y-1">
            <p className="text-sm font-medium">Installed</p>
            {installed.map((m) => (
              <div
                key={m.id}
                className="flex items-center gap-2 rounded-md border border-border px-2 py-1.5 text-sm"
              >
                <span className="truncate">{m.id}</span>
                <span className="flex-1" />
                <span className="text-xs text-muted-foreground">
                  {formatBytes(m.size)}
                </span>
                <button
                  className="text-xs text-destructive hover:underline"
                  onClick={() => void remove(m)}
                >
                  delete
                </button>
              </div>
            ))}
          </div>
        )}
        {status?.reachable && (
          <div className="space-y-1">
            <p className="text-sm font-medium">Recommended for roleplay</p>
            <StarterModelList onError={onStatus} />
          </div>
        )}
      </CardContent>
    </Card>
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
    <div className="mx-auto max-w-2xl space-y-6">
      <PersonaCard onStatus={setStatus} />

      <Card>
        <CardHeader>
          <CardTitle>Ollama</CardTitle>
          <CardDescription>
            Local inference endpoint. Leave empty for the default
            (http://localhost:11434).
          </CardDescription>
        </CardHeader>
        <CardContent>
          <SettingField
            spec={{
              key: "provider.ollama.base_url",
              label: "Base URL",
              placeholder: "http://localhost:11434",
            }}
            onStatus={setStatus}
          />
        </CardContent>
      </Card>

      <LocalModelsCard onStatus={setStatus} />

      <Card>
        <CardHeader>
          <CardTitle>OpenAI-compatible</CardTitle>
          <CardDescription>
            OpenRouter, LM Studio, vLLM, llama.cpp server, or OpenAI. The
            base URL includes /v1 (e.g. https://openrouter.ai/api/v1).
            Local servers usually need no key.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
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
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Anthropic</CardTitle>
          <CardDescription>Direct Claude API access.</CardDescription>
        </CardHeader>
        <CardContent>
          <SettingField
            spec={{
              key: "provider.anthropic.api_key",
              label: "API key",
              placeholder: "sk-ant-…",
              secret: true,
            }}
            onStatus={setStatus}
          />
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Developer mode</CardTitle>
          <CardDescription>
            Adds the context inspector, sampler panel, full model manager,
            endpoint config, and request log. Instant, no restart.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex gap-2">
          <Button
            variant={!dev ? "default" : "outline"}
            onClick={() => onDevChange(false)}
          >
            Off
          </Button>
          <Button
            variant={dev ? "default" : "outline"}
            onClick={() => onDevChange(true)}
          >
            On
          </Button>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Appearance</CardTitle>
          <CardDescription>Applied and persisted immediately.</CardDescription>
        </CardHeader>
        <CardContent className="flex gap-2">
          <Button
            variant={theme === "light" ? "default" : "outline"}
            onClick={() => onThemeChange("light")}
          >
            Light
          </Button>
          <Button
            variant={theme === "dark" ? "default" : "outline"}
            onClick={() => onThemeChange("dark")}
          >
            Dark
          </Button>
        </CardContent>
      </Card>

      <div className="space-y-1">
        {status && <p className="text-sm text-muted-foreground">{status}</p>}
        <p className="text-xs text-muted-foreground">
          Database: <span className="font-mono">{dbPath || "…"}</span>
        </p>
      </div>
    </div>
  );
}
