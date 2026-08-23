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
import { DBPath, Get, Set } from "../wailsjs/go/settings/Service";

type Theme = "light" | "dark";

const THEME_KEY = "appearance.theme";
const DISPLAY_NAME_KEY = "user.display_name";

function applyTheme(theme: Theme) {
  document.documentElement.classList.toggle("dark", theme === "dark");
}

export default function App() {
  const [displayName, setDisplayName] = useState("");
  const [savedName, setSavedName] = useState("");
  const [theme, setTheme] = useState<Theme>("dark");
  const [dbPath, setDbPath] = useState("");
  const [status, setStatus] = useState("");
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    Promise.all([Get(DISPLAY_NAME_KEY), Get(THEME_KEY), DBPath()])
      .then(([name, storedTheme, path]) => {
        if (typeof name === "string") {
          setDisplayName(name);
          setSavedName(name);
        }
        const t: Theme = storedTheme === "light" ? "light" : "dark";
        setTheme(t);
        applyTheme(t);
        setDbPath(path);
        setLoaded(true);
      })
      .catch((err) => setStatus(`Failed to load settings: ${err}`));
  }, []);

  const saveName = async () => {
    try {
      await Set(DISPLAY_NAME_KEY, displayName);
      setSavedName(displayName);
      setStatus("Saved. Restart the app — this value will still be here.");
    } catch (err) {
      setStatus(`Failed to save: ${err}`);
    }
  };

  const switchTheme = async (t: Theme) => {
    setTheme(t);
    applyTheme(t);
    try {
      await Set(THEME_KEY, t);
    } catch (err) {
      setStatus(`Failed to save theme: ${err}`);
    }
  };

  return (
    <div className="min-h-screen bg-background p-8">
      <div className="mx-auto max-w-xl space-y-6">
        <header>
          <h1 className="text-2xl font-semibold tracking-tight">Masque</h1>
          <p className="text-sm text-muted-foreground">Settings</p>
        </header>

        <Card>
          <CardHeader>
            <CardTitle>Display name</CardTitle>
            <CardDescription>
              Stored in the settings table; survives an app restart.
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
            <div className="flex items-center gap-3">
              <Button
                onClick={saveName}
                disabled={!loaded || displayName === savedName}
              >
                Save
              </Button>
              {status && (
                <span className="text-sm text-muted-foreground">{status}</span>
              )}
            </div>
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
              onClick={() => switchTheme("light")}
            >
              Light
            </Button>
            <Button
              variant={theme === "dark" ? "default" : "outline"}
              onClick={() => switchTheme("dark")}
            >
              Dark
            </Button>
          </CardContent>
        </Card>

        <p className="text-xs text-muted-foreground">
          Database: <span className="font-mono">{dbPath || "…"}</span>
        </p>
      </div>
    </div>
  );
}
