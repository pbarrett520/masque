import { useEffect, useState } from "react";
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
import { DBPath, Get, Set } from "../../wailsjs/go/settings/Service";

type Theme = "light" | "dark";

interface Props {
  theme: Theme;
  onThemeChange: (t: Theme) => void;
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

export default function SettingsScreen({ theme, onThemeChange }: Props) {
  const [dbPath, setDbPath] = useState("");
  const [status, setStatus] = useState("");

  useEffect(() => {
    DBPath()
      .then(setDbPath)
      .catch(() => {});
  }, []);

  return (
    <div className="mx-auto max-w-xl space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Display name</CardTitle>
          <CardDescription>
            How characters address you in chat ({"{{user}}"}).
          </CardDescription>
        </CardHeader>
        <CardContent>
          <SettingField
            spec={{
              key: "user.display_name",
              label: "Name",
              placeholder: "How should characters address you?",
            }}
            onStatus={setStatus}
          />
        </CardContent>
      </Card>

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
