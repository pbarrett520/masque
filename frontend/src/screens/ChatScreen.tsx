import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Health,
  ListModels,
  Providers,
  Send,
  SetModel,
  Stop,
} from "../../wailsjs/go/chat/Service";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { chat, provider } from "../../wailsjs/go/models";

interface MessageView {
  id: number;
  role: string;
  content: string;
  truncated: boolean;
}

// Payload of the chat:{id}:done event (chat.DonePayload in Go; not in
// generated models because it only travels via events).
interface DonePayload {
  messageId: number;
  content: string;
  truncated: boolean;
  usage: { promptTokens: number; completionTokens: number } | null;
}

function Bubble({ msg }: { msg: MessageView }) {
  const isUser = msg.role === "user";
  return (
    <div className={isUser ? "flex justify-end" : "flex justify-start"}>
      <div
        className={
          "max-w-[85%] whitespace-pre-wrap rounded-lg px-3 py-2 text-sm " +
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
    </div>
  );
}

interface Props {
  // The chat opened by App via OpenChat/StartChat. The component is
  // keyed by chatId, so a character switch remounts it fresh.
  initial: chat.State;
}

export default function ChatScreen({ initial }: Props) {
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

  // Token deltas are batched to state once per animation frame rather
  // than per event (dev spec §2 streaming perf note).
  const pendingRef = useRef("");
  const rafRef = useRef(0);
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
    if (!state) return;
    const id = state.chatId;

    const offDelta = EventsOn(`chat:${id}:delta`, (delta: string) => {
      pendingRef.current += delta;
      if (!rafRef.current) {
        rafRef.current = requestAnimationFrame(() => {
          rafRef.current = 0;
          const chunk = pendingRef.current;
          pendingRef.current = "";
          setStreamText((t) => t + chunk);
        });
      }
    });
    const offDone = EventsOn(`chat:${id}:done`, (p: DonePayload) => {
      if (rafRef.current) cancelAnimationFrame(rafRef.current);
      rafRef.current = 0;
      pendingRef.current = "";
      setStreaming(false);
      setStreamText("");
      if (p.content) {
        setMessages((m) => [
          ...m,
          {
            id: p.messageId,
            role: "assistant",
            content: p.content,
            truncated: p.truncated,
          },
        ]);
      }
    });
    const offError = EventsOn(`chat:${id}:error`, (msg: string) => {
      setStreaming(false);
      setError(msg);
    });
    return () => {
      offDelta();
      offDone();
      offError();
    };
  }, [state]);

  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight });
  }, [messages, streamText]);

  const send = async () => {
    const text = input.trim();
    if (!text || !state || streaming) return;
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

  const pickModel = async (model: string) => {
    if (!state || !model) return;
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
  const selectedModel =
    state && providerSel === state.providerId ? state.model : "";

  return (
    <div className="mx-auto flex h-full max-w-2xl flex-col gap-3">
      <div className="flex items-center gap-2">
        <span className="text-sm font-medium">
          {state?.characterName ?? "…"}
        </span>
        <span className="flex-1" />
        <select
          className="h-8 rounded-md border border-input bg-background px-2 text-sm"
          value={providerSel}
          onChange={(e) => pickProvider(e.target.value)}
          disabled={!state || streaming}
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
          disabled={!state || streaming}
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
      </div>

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
        {messages.map((m) => (
          <Bubble key={m.id} msg={m} />
        ))}
        {streaming && (
          <Bubble
            msg={{
              id: -1,
              role: "assistant",
              content: streamText || "…",
              truncated: false,
            }}
          />
        )}
      </div>

      <div className="flex gap-2">
        <Input
          value={input}
          placeholder={
            state?.model ? "Say something…" : "Select a model to start"
          }
          disabled={!state || streaming || !state.model}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && !e.shiftKey && send()}
        />
        {streaming ? (
          <Button variant="outline" onClick={() => state && Stop(state.chatId)}>
            Stop
          </Button>
        ) : (
          <Button onClick={send} disabled={!state || !input.trim() || !state.model}>
            Send
          </Button>
        )}
      </div>
    </div>
  );
}
