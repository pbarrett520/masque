import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Health,
  ListModels,
  Send,
  SetModel,
  StartChat,
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

export default function ChatScreen() {
  const [state, setState] = useState<chat.State | null>(null);
  const [messages, setMessages] = useState<MessageView[]>([]);
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
  const initRef = useRef(false);

  const connect = useCallback(async () => {
    setHealthErr(await Health());
    try {
      setModels((await ListModels()) ?? []);
    } catch {
      setModels([]);
    }
  }, []);

  useEffect(() => {
    if (initRef.current) return; // StrictMode double-mount guard
    initRef.current = true;
    StartChat()
      .then((s) => {
        setState(s);
        setMessages(s.messages ?? []);
      })
      .catch((err) => setError(`Failed to load chat: ${err}`));
    void connect();
  }, [connect]);

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
      await SetModel(state.chatId, model);
      setState(chat.State.createFrom({ ...state, model }));
      setError("");
    } catch (err) {
      setError(String(err));
    }
  };

  return (
    <div className="mx-auto flex h-full max-w-2xl flex-col gap-3">
      <div className="flex items-center gap-2">
        <span className="text-sm font-medium">
          {state?.characterName ?? "…"}
        </span>
        <span className="flex-1" />
        <select
          className="h-8 rounded-md border border-input bg-background px-2 text-sm"
          value={state?.model ?? ""}
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
          {/* Keep a stale selection visible even if it's not installed anymore. */}
          {state?.model && !models.some((m) => m.id === state.model) && (
            <option value={state.model}>{state.model} (missing)</option>
          )}
        </select>
        <Button variant="outline" size="sm" onClick={connect}>
          Refresh
        </Button>
      </div>

      {healthErr && (
        <div className="rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          Ollama is not reachable: {healthErr}
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
