import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Health,
  ListModels,
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
import { chat, provider } from "../../wailsjs/go/models";
import { InspectorModal, SamplerPanel } from "@/components/DevPanels";

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
  isLast?: boolean;
  busy?: boolean;
  canRegenerate?: boolean;
  onEdit?: (msg: MessageView) => void;
  onSwipe?: (msg: MessageView, direction: number) => void;
  onRegenerate?: () => void;
  onInspect?: (msg: MessageView) => void;
}

function Bubble({
  msg,
  isLast,
  busy,
  canRegenerate,
  onEdit,
  onSwipe,
  onRegenerate,
  onInspect,
}: BubbleProps) {
  const isUser = msg.role === "user";
  const hasSwipes = isLast && msg.swipeCount > 1;
  return (
    <div className={"group " + (isUser ? "flex justify-end" : "flex justify-start")}>
      <div className="max-w-[85%]">
        <div
          className={
            "whitespace-pre-wrap rounded-lg px-3 py-2 text-sm " +
            (isUser
              ? "bg-primary text-primary-foreground"
              : "bg-muted text-foreground")
          }
        >
          {msg.content}
          {msg.truncated && (
            <div className="mt-1 text-xs italic opacity-70">(cut short)</div>
          )}
        </div>
        <div
          className={
            "mt-0.5 flex items-center gap-2 text-xs text-muted-foreground " +
            (isUser ? "justify-end" : "")
          }
        >
          {hasSwipes && onSwipe && (
            <span className="flex items-center gap-1">
              <button
                className="rounded px-1 hover:bg-muted disabled:opacity-40"
                disabled={busy || msg.swipeIndex <= 1}
                onClick={() => onSwipe(msg, -1)}
              >
                ◀
              </button>
              {msg.swipeIndex}/{msg.swipeCount}
              <button
                className="rounded px-1 hover:bg-muted disabled:opacity-40"
                disabled={busy || msg.swipeIndex >= msg.swipeCount}
                onClick={() => onSwipe(msg, 1)}
              >
                ▶
              </button>
            </span>
          )}
          {isLast && canRegenerate && onRegenerate && (
            <button
              className="rounded px-1 hover:bg-muted disabled:opacity-40"
              disabled={busy}
              onClick={onRegenerate}
              title="Regenerate this reply"
            >
              ↻ regenerate
            </button>
          )}
          {onEdit && (
            <button
              className="invisible rounded px-1 hover:bg-muted group-hover:visible"
              disabled={busy}
              onClick={() => onEdit(msg)}
            >
              edit
            </button>
          )}
          {onInspect && msg.role === "assistant" && msg.id > 0 && (
            <button
              className="invisible rounded px-1 hover:bg-muted group-hover:visible"
              onClick={() => onInspect(msg)}
              title="What was sent to the model for this reply"
            >
              inspect
            </button>
          )}
        </div>
      </div>
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
  const [models, setModels] = useState<provider.ModelInfo[]>([]);
  const [healthErr, setHealthErr] = useState("");
  const [error, setError] = useState("");
  const [input, setInput] = useState("");
  const [streaming, setStreaming] = useState(false);
  const [streamText, setStreamText] = useState("");
  const [editing, setEditing] = useState<MessageView | null>(null);
  const [editText, setEditText] = useState("");
  const [samplerOpen, setSamplerOpen] = useState(false);
  const [inspectId, setInspectId] = useState<number | null>(null);

  // Token deltas are batched to state on a short timer rather than per
  // event (dev spec §2 streaming perf note). A timer, not
  // requestAnimationFrame: WebKitGTK can stall rAF entirely (NVIDIA /
  // DMA-BUF setups), which made replies appear only when finished.
  const pendingRef = useRef("");
  const flushTimerRef = useRef(0);
  const scrollRef = useRef<HTMLDivElement>(null);

  const connect = useCallback(async (providerID: string) => {
    setModels([]);
    setHealthErr(await Health(providerID));
    try {
      setModels((await ListModels(providerID)) ?? []);
    } catch {
      setModels([]);
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
    Providers().then(setProviders).catch(() => {});
    setProviderSel(initial.providerId || "ollama");
    void connect(initial.providerId || "ollama");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initial.chatId]);

  const pickProvider = (id: string) => {
    setProviderSel(id);
    setError("");
    void connect(id);
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

  const pickModel = async (model: string) => {
    if (!model) return;
    try {
      await SetModel(state.chatId, providerSel, model);
      setState(chat.State.createFrom({ ...state, providerId: providerSel, model }));
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
    last.role === "assistant" &&
    messages.some((m) => m.role === "user");

  return (
    <div className="mx-auto flex h-full max-w-2xl flex-col gap-3">
      <div className="flex items-center gap-2">
        <span className="text-sm font-medium">{state.characterName}</span>
        <span className="flex-1" />
        <select
          className="h-8 rounded-md border border-input bg-background px-2 text-sm"
          value={providerSel}
          onChange={(e) => pickProvider(e.target.value)}
          disabled={streaming}
        >
          {providers.map((p) => (
            <option key={p.id} value={p.id}>
              {p.label}
              {p.needsKey ? " (needs API key)" : ""}
            </option>
          ))}
        </select>
        <select
          className="h-8 max-w-64 rounded-md border border-input bg-background px-2 text-sm"
          value={selectedModel}
          onChange={(e) => pickModel(e.target.value)}
          disabled={streaming}
        >
          <option value="" disabled>
            {models.length ? "Select a model…" : "No models found"}
          </option>
          {models.map((m) => (
            <option key={m.id} value={m.id}>
              {m.id}
            </option>
          ))}
          {/* Keep a stale selection visible even if it's not offered anymore. */}
          {selectedModel && !models.some((m) => m.id === selectedModel) && (
            <option value={selectedModel}>{selectedModel} (missing)</option>
          )}
        </select>
        <Button
          variant="outline"
          size="sm"
          onClick={() => void connect(providerSel)}
        >
          Refresh
        </Button>
        {dev && (
          <Button
            variant={samplerOpen ? "secondary" : "outline"}
            size="sm"
            onClick={() => setSamplerOpen((o) => !o)}
          >
            Sampler
          </Button>
        )}
      </div>

      {dev && samplerOpen && (
        <SamplerPanel
          chatId={state.chatId}
          onClose={() => setSamplerOpen(false)}
          onError={setError}
        />
      )}

      {healthErr && (
        <div className="rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          Endpoint problem: {healthErr}
        </div>
      )}
      {error && (
        <div className="rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {error}
        </div>
      )}

      <div
        ref={scrollRef}
        className="flex-1 space-y-3 overflow-y-auto rounded-lg border border-border bg-card p-4"
      >
        {messages.map((m, i) =>
          editing && editing.id === m.id ? (
            <div key={m.id} className="space-y-1.5">
              <textarea
                className="min-h-24 w-full rounded-md border border-input bg-background p-2 text-sm"
                value={editText}
                autoFocus
                onChange={(e) => setEditText(e.target.value)}
              />
              <div className="flex gap-2">
                <Button size="sm" onClick={saveEdit} disabled={!editText.trim()}>
                  Save
                </Button>
                <Button size="sm" variant="outline" onClick={() => setEditing(null)}>
                  Cancel
                </Button>
              </div>
            </div>
          ) : (
            <Bubble
              key={m.id}
              msg={m}
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
            msg={{
              id: -1,
              role: "assistant",
              content: streamText || "…",
              truncated: false,
              swipeIndex: 0,
              swipeCount: 0,
            }}
          />
        )}
      </div>

      {dev && inspectId !== null && (
        <InspectorModal
          chatId={state.chatId}
          messageId={inspectId}
          onClose={() => setInspectId(null)}
        />
      )}

      <div className="flex gap-2">
        <Input
          value={input}
          placeholder={
            state.model ? "Say something…" : "Select a model to start"
          }
          disabled={streaming || !state.model}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && !e.shiftKey && send()}
        />
        {streaming ? (
          <Button variant="outline" onClick={() => Stop(state.chatId)}>
            Stop
          </Button>
        ) : (
          <Button onClick={send} disabled={!input.trim() || !state.model}>
            Send
          </Button>
        )}
      </div>
    </div>
  );
}
