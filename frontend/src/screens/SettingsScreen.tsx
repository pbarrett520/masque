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

const DISPLAY_NAME_KEY = "user.display_name";
const OLLAMA_URL_KEY = "provider.ollama.base_url";

interface Props {
  theme: Theme;
  onThemeChange: (t: Theme) => void;
}

export default function SettingsScreen({ theme, onThemeChange }: Props) {
  const [displayName, setDisplayName] = useState("");
  const [savedName, setSavedName] = useState("");
  const [ollamaUrl, setOllamaUrl] = useState("");
  const [savedOllamaUrl, setSavedOllamaUrl] = useState("");
  const [dbPath, setDbPath] = useState("");
  const [status, setStatus] = useState("");
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    Promise.all([Get(DISPLAY_NAME_KEY), Get(OLLAMA_URL_KEY), DBPath()])
      .then(([name, url, path]) => {
        if (typeof name === "string") {
          setDisplayName(name);
          setSavedName(name);
        }
        if (typeof url === "string") {
          setOllamaUrl(url);
          setSavedOllamaUrl(url);
        }
        setDbPath(path);
        setLoaded(true);
      })
      .catch((err) => setStatus(`Failed to load settings: ${err}`));
  }, []);

  const saveName = async () => {
    try {
      await Set(DISPLAY_NAME_KEY, displayName);
      setSavedName(displayName);
      setStatus("Saved.");
    } catch (err) {
      setStatus(`Failed to save: ${err}`);
    }
  };

  const saveOllamaUrl = async () => {
    try {
      const trimmed = ollamaUrl.trim();
      // Empty value clears the override back to the default endpoint.
      await Set(OLLAMA_URL_KEY, trimmed === "" ? null : trimmed);
      setOllamaUrl(trimmed);
      setSavedOllamaUrl(trimmed);
      setStatus("Saved. Applies to the next request.");
    } catch (err) {
      setStatus(`Failed to save: ${err}`);
    }
  };

  return (
    <div className="mx-auto max-w-xl space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Display name</CardTitle>
          <CardDescription>
            How characters address you in chat ({"{{user}}"}).
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="space-y-1.5">
            <Label htmlFor="display-name">Name</Label>
            <Input
              id="display-name"
              value={displayName}
              placeholder="How should characters address you?"
              disabled={!loaded}
              onChange={(e) => setDisplayName(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && saveName()}
            />
          </div>
          <Button
            onClick={saveName}
            disabled={!loaded || displayName === savedName}
          >
            Save
          </Button>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Ollama</CardTitle>
          <CardDescription>
            Endpoint for local inference. Leave empty for the default
            (http://localhost:11434).
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="space-y-1.5">
            <Label htmlFor="ollama-url">Base URL</Label>
            <Input
              id="ollama-url"
              value={ollamaUrl}
              placeholder="http://localhost:11434"
              disabled={!loaded}
              onChange={(e) => setOllamaUrl(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && saveOllamaUrl()}
            />
          </div>
          <Button
            onClick={saveOllamaUrl}
            disabled={!loaded || ollamaUrl === savedOllamaUrl}
          >
            Save
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
