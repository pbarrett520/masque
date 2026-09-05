import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Get, Set } from "../wailsjs/go/settings/Service";
import { setSetting } from "@/lib/settings";
import {
  DeleteChat,
  ListChats,
  NewChat,
  OpenChat,
  OpenChatByID,
  StartChat,
} from "../wailsjs/go/chat/Service";
import { chat, store } from "../wailsjs/go/models";
import CharactersScreen from "./screens/CharactersScreen";
import ChatScreen from "./screens/ChatScreen";
import DevScreen from "./screens/DevScreen";
import OnboardingScreen from "./screens/OnboardingScreen";
import SettingsScreen from "./screens/SettingsScreen";

type Theme = "light" | "dark";
type View = "characters" | "chat" | "settings" | "dev";

const THEME_KEY = "appearance.theme";
const DEV_KEY = "dev.enabled";

function applyTheme(theme: Theme) {
  document.documentElement.classList.toggle("dark", theme === "dark");
}

function timeAgo(unixSeconds: number): string {
  const s = Math.max(0, Math.floor(Date.now() / 1000) - unixSeconds);
  if (s < 60) return "now";
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}

export default function App() {
  const [view, setView] = useState<View>("characters");
  const [theme, setTheme] = useState<Theme>("dark");
  const [dev, setDev] = useState(false);
  // null = still deciding; true = show the first-run wizard.
  const [onboarding, setOnboarding] = useState<boolean | null>(null);
  const [chatState, setChatState] = useState<chat.State | null>(null);
  const [chats, setChats] = useState<store.ChatListItem[]>([]);
  const [error, setError] = useState("");
  const initRef = useRef(false);

  const refreshChats = useCallback(() => {
    ListChats()
      .then((items) => setChats(items ?? []))
      .catch(() => {});
  }, []);

  // Resume the last active chat; a zero chatId means nothing to
  // resume, so stay on the characters screen.
  const resume = useCallback(() => {
    StartChat()
      .then((s) => {
        if (s.chatId) {
          setChatState(s);
          setView("chat");
        }
      })
      .catch((err) => setError(`Failed to resume chat: ${err}`));
    refreshChats();
  }, [refreshChats]);

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
    Get(DEV_KEY)
      .then((v) => setDev(v === true))
      .catch(() => {});
    // First run (no completed onboarding, no default model) gets the
    // wizard; anything else — including pre-onboarding installs, which
    // have a default model — goes straight to the app.
    Promise.all([Get("onboarding.done"), Get("provider.default_model")])
      .then(([done, model]) => {
        if (!done && !model) {
          setOnboarding(true);
        } else {
          setOnboarding(false);
          resume();
        }
      })
      .catch(() => {
        setOnboarding(false);
        resume();
      });
  }, [resume]);

  const finishOnboarding = useCallback(() => {
    setOnboarding(false);
    resume();
  }, [resume]);

  const applyState = (s: chat.State) => {
    setChatState(s);
    setView("chat");
    setError("");
    refreshChats();
  };

  const openCharacter = (characterId: number) =>
    OpenChat(characterId).then(applyState).catch((err) => setError(String(err)));

  const openChat = (chatId: number) =>
    OpenChatByID(chatId).then(applyState).catch((err) => setError(String(err)));

  const newChat = () => {
    if (!chatState) return;
    NewChat(chatState.characterId)
      .then(applyState)
      .catch((err) => setError(String(err)));
  };

  const removeChat = async (item: store.ChatListItem) => {
    if (!window.confirm(`Delete this chat with ${item.characterName}?`)) return;
    try {
      await DeleteChat(item.id);
      if (chatState?.chatId === item.id) {
        // Deleted the open chat: resume whatever is left.
        const s = await StartChat();
        setChatState(s.chatId ? s : null);
        if (!s.chatId) setView("characters");
      }
      refreshChats();
    } catch (err) {
      setError(String(err));
    }
  };

  const switchTheme = (t: Theme) => {
    setTheme(t);
    applyTheme(t);
    Set(THEME_KEY, t).catch(() => {});
  };

  // Dev mode is instant and persisted (spec §9); leaving it while on
  // the dev tab falls back to settings.
  const switchDev = (on: boolean) => {
    setDev(on);
    setSetting(DEV_KEY, on ? true : null).catch(() => {});
    if (!on && view === "dev") setView("settings");
  };

  // Nav items are plain text; the live one carries a gilt underline.
  const tab = (v: View, label: string, disabled = false) => (
    <button
      className={
        "relative px-1 py-1 text-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/70 rounded-sm disabled:opacity-40 " +
        (view === v
          ? "text-foreground after:absolute after:inset-x-1 after:-bottom-[9px] after:h-[2px] after:bg-gilt"
          : "text-muted-foreground hover:text-foreground")
      }
      disabled={disabled}
      aria-current={view === v ? "page" : undefined}
      onClick={() => setView(v)}
    >
      {label}
    </button>
  );

  const wordmark = (
    <h1 className="font-title text-[1.2rem] leading-none">Masque</h1>
  );

  // First run has no chrome: the wizard's own wordmark is the hero.
  if (onboarding !== false) {
    return (
      <div className="flex h-screen flex-col bg-background text-foreground">
        <main className="min-h-0 flex-1">
          {onboarding && <OnboardingScreen onDone={finishOnboarding} />}
        </main>
      </div>
    );
  }

  return (
    <div className="flex h-screen flex-col bg-background text-foreground">
      <header className="flex h-12 shrink-0 items-center gap-6 border-b border-border px-6">
        {wordmark}
        <nav className="flex gap-4">
          {tab("characters", "Characters")}
          {tab("chat", "Chat", !chatState)}
          {tab("settings", "Settings")}
          {dev && tab("dev", "Dev")}
        </nav>
        {error && (
          <span className="truncate text-sm text-destructive">{error}</span>
        )}
      </header>
      <main className="min-h-0 flex-1 overflow-y-auto p-6">
        {view === "characters" && <CharactersScreen onOpen={openCharacter} />}
        {/* Chat view stays mounted so a streaming reply survives tab
            switches; ChatScreen is keyed by chatId so switching chats
            remounts it. */}
        {chatState && (
          <div className={view === "chat" ? "mx-auto flex h-full w-full max-w-6xl gap-8" : "hidden"}>
            <aside className="flex w-52 shrink-0 flex-col overflow-y-auto">
              <Button variant="outline" size="sm" className="mb-3" onClick={newChat}>
                New chat
              </Button>
              <ul className="flex flex-col gap-px">
                {chats.map((c) => {
                  const active = c.id === chatState.chatId;
                  return (
                    <li
                      key={c.id}
                      className={
                        "group relative cursor-pointer rounded-md py-1.5 pl-3 pr-2 text-sm " +
                        (active
                          ? "bg-card text-foreground before:absolute before:inset-y-1.5 before:left-0 before:w-[2px] before:rounded-full before:bg-gilt"
                          : "text-muted-foreground hover:bg-card/60 hover:text-foreground")
                      }
                      onClick={() => !active && openChat(c.id)}
                    >
                      <div className="flex items-center gap-1">
                        <span className="truncate font-medium">
                          {c.characterName}
                        </span>
                        <span className="flex-1" />
                        <span className="text-xs text-muted-foreground group-hover:hidden">
                          {timeAgo(c.updatedAt)}
                        </span>
                        <button
                          className="hidden rounded px-1 text-xs text-muted-foreground hover:text-destructive group-hover:block"
                          title="Delete chat"
                          aria-label="Delete chat"
                          onClick={(e) => {
                            e.stopPropagation();
                            void removeChat(c);
                          }}
                        >
                          ✕
                        </button>
                      </div>
                      {c.title && c.title !== c.characterName && (
                        <div className="truncate text-xs text-muted-foreground">
                          {c.title}
                        </div>
                      )}
                    </li>
                  );
                })}
              </ul>
            </aside>
            <div className="min-w-0 flex-1">
              <ChatScreen
                key={chatState.chatId}
                initial={chatState}
                dev={dev}
                onActivity={refreshChats}
              />
            </div>
          </div>
        )}
        {view === "settings" && (
          <SettingsScreen
            theme={theme}
            onThemeChange={switchTheme}
            dev={dev}
            onDevChange={switchDev}
          />
        )}
        {view === "dev" && dev && <DevScreen />}
      </main>
    </div>
  );
}
