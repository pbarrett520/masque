import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Get, Set } from "../wailsjs/go/settings/Service";
import { OpenChat, StartChat } from "../wailsjs/go/chat/Service";
import { chat } from "../wailsjs/go/models";
import CharactersScreen from "./screens/CharactersScreen";
import ChatScreen from "./screens/ChatScreen";
import SettingsScreen from "./screens/SettingsScreen";

type Theme = "light" | "dark";
type View = "characters" | "chat" | "settings";

const THEME_KEY = "appearance.theme";

function applyTheme(theme: Theme) {
  document.documentElement.classList.toggle("dark", theme === "dark");
}

export default function App() {
  const [view, setView] = useState<View>("characters");
  const [theme, setTheme] = useState<Theme>("dark");
  const [chatState, setChatState] = useState<chat.State | null>(null);
  const [error, setError] = useState("");
  const initRef = useRef(false);

  useEffect(() => {
    if (initRef.current) return; // StrictMode double-mount guard
    initRef.current = true;
    Get(THEME_KEY)
      .then((stored) => {
        const t: Theme = stored === "light" ? "light" : "dark";
        setTheme(t);
        applyTheme(t);
      })
      .catch(() => applyTheme("dark"));
    // Resume the last active character's chat; a zero chatId means
    // nothing to resume, so stay on the characters screen.
    StartChat()
      .then((s) => {
        if (s.chatId) {
          setChatState(s);
          setView("chat");
        }
      })
      .catch((err) => setError(`Failed to resume chat: ${err}`));
  }, []);

  const openCharacter = async (characterId: number) => {
    try {
      setError("");
      const s = await OpenChat(characterId);
      setChatState(s);
      setView("chat");
    } catch (err) {
      setError(String(err));
    }
  };

  const switchTheme = (t: Theme) => {
    setTheme(t);
    applyTheme(t);
    Set(THEME_KEY, t).catch(() => {});
  };

  const tab = (v: View, label: string, disabled = false) => (
    <Button
      variant={view === v ? "secondary" : "ghost"}
      size="sm"
      disabled={disabled}
      onClick={() => setView(v)}
    >
      {label}
    </Button>
  );

  return (
    <div className="flex h-screen flex-col bg-background text-foreground">
      <header className="flex items-center gap-4 border-b border-border px-6 py-3">
        <h1 className="text-lg font-semibold tracking-tight">Masque</h1>
        <nav className="flex gap-1">
          {tab("characters", "Characters")}
          {tab("chat", "Chat", !chatState)}
          {tab("settings", "Settings")}
        </nav>
        {error && (
          <span className="truncate text-sm text-destructive">{error}</span>
        )}
      </header>
      <main className="min-h-0 flex-1 overflow-y-auto p-6">
        {view === "characters" && <CharactersScreen onOpen={openCharacter} />}
        {/* ChatScreen stays mounted so a streaming reply survives tab
            switches; keyed by chatId so switching characters remounts. */}
        {chatState && (
          <div className={view === "chat" ? "h-full" : "hidden"}>
            <ChatScreen key={chatState.chatId} initial={chatState} />
          </div>
        )}
        {view === "settings" && (
          <SettingsScreen theme={theme} onThemeChange={switchTheme} />
        )}
      </main>
    </div>
  );
}
