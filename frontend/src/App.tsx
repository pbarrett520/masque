import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Get, Set } from "../wailsjs/go/settings/Service";
import ChatScreen from "./screens/ChatScreen";
import SettingsScreen from "./screens/SettingsScreen";

type Theme = "light" | "dark";
type View = "chat" | "settings";

const THEME_KEY = "appearance.theme";

function applyTheme(theme: Theme) {
  document.documentElement.classList.toggle("dark", theme === "dark");
}

export default function App() {
  const [view, setView] = useState<View>("chat");
  const [theme, setTheme] = useState<Theme>("dark");

  useEffect(() => {
    Get(THEME_KEY)
      .then((stored) => {
        const t: Theme = stored === "light" ? "light" : "dark";
        setTheme(t);
        applyTheme(t);
      })
      .catch(() => applyTheme("dark"));
  }, []);

  const switchTheme = (t: Theme) => {
    setTheme(t);
    applyTheme(t);
    Set(THEME_KEY, t).catch(() => {});
  };

  return (
    <div className="flex h-screen flex-col bg-background text-foreground">
      <header className="flex items-center gap-4 border-b border-border px-6 py-3">
        <h1 className="text-lg font-semibold tracking-tight">Masque</h1>
        <nav className="flex gap-1">
          <Button
            variant={view === "chat" ? "secondary" : "ghost"}
            size="sm"
            onClick={() => setView("chat")}
          >
            Chat
          </Button>
          <Button
            variant={view === "settings" ? "secondary" : "ghost"}
            size="sm"
            onClick={() => setView("settings")}
          >
            Settings
          </Button>
        </nav>
      </header>
      <main className="min-h-0 flex-1 overflow-y-auto p-6">
        {/* ChatScreen stays mounted so a streaming reply survives tab switches. */}
        <div className={view === "chat" ? "h-full" : "hidden"}>
          <ChatScreen />
        </div>
        {view === "settings" && <SettingsScreen theme={theme} onThemeChange={switchTheme} />}
      </main>
    </div>
  );
}
