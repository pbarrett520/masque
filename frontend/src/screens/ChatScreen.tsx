import { useCallback, useEffect, useRef, useState, type ButtonHTMLAttributes } from "react";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { Select } from "@/components/ui/select";
import {
  Health,
  Catalog,
  OpenChatByID,
  Providers,
  Regenerate,
  Send,
  SetModel,
  Stop,
  Swipe,
  EditMessage,
} from "../../wailsjs/go/chat/Service";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { chat, presets } from "../../wailsjs/go/models";
import ModelPicker from "@/components/ModelPicker";
import { InspectorModal, SamplerPanel } from "@/components/DevPanels";
import { Markdown } from "@/components/Markdown";

interface MessageView {
  id: number;
  role: string;
  content: string;
  truncated: boolean;
  swipeIndex: number;
  swipeCount: number;
}

// Payload of the chat:{id}:done event (chat.DonePayload in Go; not in
// generated models because it only travels via events).
interface DonePayload {
  messageId: number;
  content: string;
  truncated: boolean;
  usage: { promptTokens: number; completionTokens: number } | null;
}

interface BubbleProps {
  msg: MessageView;
  // Who is speaking, shown above a character turn.
  name: string;
  isLast?: boolean;
  busy?: boolean;
  streaming?: boolean;
  canRegenerate?: boolean;
  onEdit?: (msg: MessageView) => void;
  onSwipe?: (msg: MessageView, direction: number) => void;
  onRegenerate?: () => void;
  onInspect?: (msg: MessageView) => void;
}

// A quiet text action under a turn. Hidden until the turn is hovered
// unless `always` is set (swipes and regenerate stay visible on the
// latest reply so the primary loop is discoverable).
function TurnAction({
  children,
  always,
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & { always?: boolean }) {
  return (
    <button
      className={
        "rounded-sm px-1 text-xs text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/70 disabled:opacity-40 disabled:hover:text-muted-foreground " +
        (always ? "" : "invisible group-hover:visible focus-visible:visible")
      }
      {...props}
    >
      {children}
    </button>
  );
}

// A turn in the script. The character's voice is set as prose in the
// reading column with their name in gilt above; the user's line is a
// velvet panel on the right.
function Bubble({
  msg,
  name,
  isLast,
  busy,
  streaming,
  canRegenerate,
  onEdit,
  onSwipe,
  onRegenerate,
  onInspect,
}: BubbleProps) {
  const isUser = msg.role === "user";
  const hasSwipes = isLast && msg.swipeCount > 1;

  const actions = (
    <div
      className={
        "mt-1 flex min-h-5 items-center gap-2 " + (isUser ? "justify-end" : "")
      }
    >
      {hasSwipes && onSwipe && (
        <span className="flex items-center gap-0.5 text-xs text-muted-foreground">
          <TurnAction
            always
            aria-label="Previous version"
            disabled={busy || msg.swipeIndex <= 1}
            onClick={() => onSwipe(msg, -1)}
          >
            ‹
          </TurnAction>
          <span className="tabular-nums">
            {msg.swipeIndex} of {msg.swipeCount}
          </span>
          <TurnAction
            always
            aria-label="Next version"
            disabled={busy || msg.swipeIndex >= msg.swipeCount}
            onClick={() => onSwipe(msg, 1)}
          >
            ›
          </TurnAction>
        </span>
      )}
      {isLast && canRegenerate && onRegenerate && (
        <TurnAction
          always
          disabled={busy}
          onClick={onRegenerate}
          title="Ask for a different reply"
        >
          Regenerate
        </TurnAction>
      )}
      {onEdit && (
        <TurnAction disabled={busy} onClick={() => onEdit(msg)}>
          Edit
        </TurnAction>
      )}
      {onInspect && msg.role === "assistant" && msg.id > 0 && (
        <TurnAction
          onClick={() => onInspect(msg)}
          title="What was sent to the model for this reply"
        >
          Inspect
        </TurnAction>
      )}
    </div>
  );

  if (isUser) {
    return (
      <div className="group flex flex-col items-end">
        <div className="max-w-[80%] rounded-lg bg-card px-4 py-2.5 text-[0.9667rem]">
          <Markdown text={msg.content} />
        </div>
        {actions}
      </div>
    );
  }

  return (
    <div className="group">
      <div className="mb-1 text-[0.8667rem] font-medium text-gilt">{name}</div>
      <div className={"voice " + (streaming ? "caret-in" : "")}>
        {msg.content ? (
          <Markdown text={msg.content} />
        ) : (
          <p className="caret" aria-label="Waiting for the first words" />
        )}
        {msg.truncated && (
          <p className="mt-1 font-sans text-xs italic text-muted-foreground">
            Cut short
          </p>
        )}
      </div>
      {actions}
    </div>
  );
}

interface Props {
  // The chat opened by App via OpenChat/StartChat. The component is
  // keyed by chatId, so a chat switch remounts it fresh.
  initial: chat.State;
  // Developer mode (spec §9): adds the sampler panel and the per-reply
  // context inspector.
  dev?: boolean;
  // Called whenever this chat's content changed in a way the chat list
  // should reflect (new message, regenerate, …).
  onActivity?: () => void;
}

export default function ChatScreen({ initial, dev, onActivity }: Props) {
  const [state, setState] = useState<chat.State>(initial);
  const [messages, setMessages] = useState<MessageView[]>(
    initial.messages ?? []
  );
  const [providers, setProviders] = useState<chat.ProviderInfo[]>([]);
  // Provider shown in the picker; the chat's actual provider only
  // changes once a model is chosen (SetModel).
  const [providerSel, setProviderSel] = useState("ollama");
  const [catalog, setCatalog] = useState<presets.Catalog | null>(null);
  const [healthErr, setHealthErr] = useState("");
  const [error, setError] = useState("");
  const [input, setInput] = useState("");
  const [streaming, setStreaming] = useState(false);
  const [streamText, setStreamText] = useState("");
  const [editing, setEditing] = useState<MessageView | null>(null);
  const [editText, setEditText] = useState("");
  const [samplerOpen, setSamplerOpen] = useState(false);
  const [inspectId, setInspectId] = useState<number | null>(null);

  // App re-resolves the chat after events it can't see from here (a
  // character delete); adopt the fresh state when it arrives.
  useEffect(() => {
    setState(initial);
    setMessages(initial.messages ?? []);
  }, [initial]);

  // Token deltas are batched to state on a short timer rather than per
  // event (dev spec §2 streaming perf note). A timer, not
  // requestAnimationFrame: WebKitGTK can stall rAF entirely (NVIDIA /
  // DMA-BUF setups), which made replies appear only when finished.
  const pendingRef = useRef("");
  const flushTimerRef = useRef(0);
  const scrollRef = useRef<HTMLDivElement>(null);

  // connect loads the picker catalog for a provider and returns the
  // model to preselect ("" when none). Ollama keeps its reachability
  // probe; cloud providers report through the catalog (a rejected key,
  // or a host with no model list but recommended models to try).
  const connect = useCallback(async (providerID: string): Promise<string> => {
    setCatalog(null);
    setHealthErr("");
    try {
      const c = await Catalog(providerID);
      setCatalog(c);
      if (c.keyRejected) {
        setHealthErr(`API key rejected: ${c.listError}`);
      } else if (!c.listed && c.recommended.length === 0) {
        setHealthErr(providerID === "ollama" ? await Health(providerID) : c.listError);
      }
      return c.default ?? "";
    } catch (err) {
      setHealthErr(String(err));
      return "";
    }
  }, []);

  // Re-fetch the active thread from the source of truth.
  const reload = useCallback(async () => {
    try {
      const fresh = await OpenChatByID(initial.chatId);
      setState(fresh);
      setMessages(fresh.messages ?? []);
    } catch (err) {
      setError(String(err));
    }
  }, [initial.chatId]);

  useEffect(() => {
    Providers().then((all) => setProviders(all ?? [])).catch(() => {});
    const pid = initial.providerId || "ollama";
    setProviderSel(pid);
    void connect(pid).then((def) => {
      // A chat without a model yet (fresh install, provider added in
      // Settings) gets the catalog default so the first message works.
      if (!initial.model && def) void pickModel(def, pid);
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initial.chatId]);

  // Switching provider in the header also switches the chat to that
  // provider's default model, so the next message simply goes there.
  const pickProvider = async (id: string) => {
    setProviderSel(id);
    setError("");
    const def = await connect(id);
    if (def) await pickModel(def, id);
  };

  useEffect(() => {
    const id = state.chatId;
    const offDelta = EventsOn(`chat:${id}:delta`, (delta: string) => {
      pendingRef.current += delta;
      if (!flushTimerRef.current) {
        flushTimerRef.current = window.setTimeout(() => {
          flushTimerRef.current = 0;
          const chunk = pendingRef.current;
          pendingRef.current = "";
          setStreamText((t) => t + chunk);
        }, 33);
      }
    });
    const offDone = EventsOn(`chat:${id}:done`, (_p: DonePayload) => {
      if (flushTimerRef.current) window.clearTimeout(flushTimerRef.current);
      flushTimerRef.current = 0;
      pendingRef.current = "";
      setStreaming(false);
      setStreamText("");
      void reload();
      onActivity?.();
    });
    const offError = EventsOn(`chat:${id}:error`, (msg: string) => {
      setStreaming(false);
      setError(msg);
      void reload();
    });
    return () => {
      offDelta();
      offDone();
      offError();
      if (flushTimerRef.current) window.clearTimeout(flushTimerRef.current);
      flushTimerRef.current = 0;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state.chatId]);

  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight });
  }, [messages, streamText]);

  const send = async () => {
    const text = input.trim();
    if (!text || streaming) return;
    setError("");
    try {
      const userMsg = await Send(state.chatId, text);
      setMessages((m) => [...m, userMsg]);
      setInput("");
      setStreamText("");
      setStreaming(true);
    } catch (err) {
      setError(String(err));
    }
  };

  const regenerate = async () => {
    if (streaming) return;
    setError("");
    try {
      await Regenerate(state.chatId);
      // The replaced reply is already deactivated server-side.
      setStreamText("");
      setStreaming(true);
      void reload();
    } catch (err) {
      setError(String(err));
    }
  };

  const swipe = async (msg: MessageView, direction: number) => {
    setError("");
    try {
      const fresh = await Swipe(state.chatId, msg.id, direction);
      setState(fresh);
      setMessages(fresh.messages ?? []);
    } catch (err) {
      setError(String(err));
    }
  };

  const startEdit = (msg: MessageView) => {
    setEditing(msg);
    setEditText(msg.content);
  };

  const saveEdit = async () => {
    if (!editing) return;
    try {
      await EditMessage(state.chatId, editing.id, editText);
      setEditing(null);
      await reload();
    } catch (err) {
      setError(String(err));
    }
  };

  const pickModel = async (model: string, providerID = providerSel) => {
    if (!model) return;
    try {
      await SetModel(state.chatId, providerID, model);
      setState((s) => chat.State.createFrom({ ...s, providerId: providerID, model }));
      setError("");
    } catch (err) {
      setError(String(err));
    }
  };

  // The model dropdown reflects the chat's model only while the picker
  // is on the chat's provider; after switching providers it's unset
  // until the user picks one.
  const selectedModel = providerSel === state.providerId ? state.model : "";
  const last = messages[messages.length - 1];
  const canRegenerate =
    !!last &&
    !state.characterDeleted &&
    last.role === "assistant" &&
    messages.some((m) => m.role === "user");

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center gap-2 pb-3">
        <h2 className="font-title text-xl leading-none">
          {state.characterName}
          {state.characterDeleted && (
            <span className="ml-2 font-sans text-xs font-normal text-muted-foreground">
              deleted
            </span>
          )}
        </h2>
        <span className="flex-1" />
        <Select
          className="h-8"
          aria-label="Provider"
          value={providerSel}
          onChange={(e) => void pickProvider(e.target.value)}
          disabled={streaming}
        >
          {/* Only providers set up in Settings, plus whatever this chat
              already uses so a removed key doesn't blank the picker. */}
          {providers
            .filter((p) => p.configured || p.id === state.providerId)
            .map((p) => (
              <option key={p.id} value={p.id}>
                {p.label}
                {p.needsKey ? " (needs API key)" : ""}
              </option>
            ))}
        </Select>
        <ModelPicker
          compact
          catalog={catalog}
          value={selectedModel}
          onChange={(id) => void pickModel(id)}
          disabled={streaming}
        />
        <Button
          variant="ghost"
          size="sm"
          onClick={() => void connect(providerSel)}
        >
          Refresh
        </Button>
        {dev && (
          <Button
            variant={samplerOpen ? "secondary" : "ghost"}
            size="sm"
            onClick={() => setSamplerOpen((o) => !o)}
          >
            Sampler
          </Button>
        )}
      </div>

      {dev && samplerOpen && (
        <div className="pb-3">
          <SamplerPanel
            chatId={state.chatId}
            onClose={() => setSamplerOpen(false)}
            onError={setError}
          />
        </div>
      )}

      {healthErr && (
        <div className="mb-3 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          Endpoint problem: {healthErr}
        </div>
      )}
      {error && (
        <div className="mb-3 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {error}
        </div>
      )}

      <div
        ref={scrollRef}
        className="min-h-0 flex-1 overflow-y-auto border-t border-border"
      >
        <div className="mx-auto flex w-full max-w-3xl flex-col gap-6 py-6">
          {messages.map((m, i) =>
            editing && editing.id === m.id ? (
              <div key={m.id} className="space-y-2">
                <Textarea
                  className={
                    "max-h-96 min-h-24 " +
                    (m.role === "assistant" ? "voice max-w-none" : "")
                  }
                  value={editText}
                  autoFocus
                  onChange={(e) => setEditText(e.target.value)}
                />
                <div className="flex gap-2">
                  <Button size="sm" onClick={saveEdit} disabled={!editText.trim()}>
                    Save
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => setEditing(null)}>
                    Cancel
                  </Button>
                </div>
              </div>
            ) : (
              <Bubble
                key={m.id}
                msg={m}
                name={state.characterName}
                isLast={i === messages.length - 1 && !streaming}
                busy={streaming}
                canRegenerate={canRegenerate}
                onEdit={startEdit}
                onSwipe={swipe}
                onRegenerate={regenerate}
                onInspect={dev ? (msg) => setInspectId(msg.id) : undefined}
              />
            )
          )}
          {streaming && (
            <Bubble
              name={state.characterName}
              streaming
              msg={{
                id: -1,
                role: "assistant",
                content: streamText,
                truncated: false,
                swipeIndex: 0,
                swipeCount: 0,
              }}
            />
          )}
        </div>
      </div>

      {dev && inspectId !== null && (
        <InspectorModal
          chatId={state.chatId}
          messageId={inspectId}
          onClose={() => setInspectId(null)}
        />
      )}

      <div className="mx-auto flex w-full max-w-3xl items-end gap-2 pt-3">
        <Textarea
          className="max-h-48 text-[0.9667rem]"
          value={input}
          placeholder={
            state.characterDeleted
              ? "This character was deleted. The chat can be read but not continued."
              : state.model
                ? "Say something… (Shift+Enter for a new line)"
                : "Select a model to start"
          }
          disabled={streaming || !state.model || state.characterDeleted}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              send();
            }
          }}
        />
        {streaming ? (
          <Button variant="outline" onClick={() => Stop(state.chatId)}>
            Stop
          </Button>
        ) : (
          <Button onClick={send} disabled={!input.trim() || !state.model || state.characterDeleted}>
            Send
          </Button>
        )}
      </div>
    </div>
  );
}
