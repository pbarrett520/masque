import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import {
  Avatar,
  ChatCount,
  Delete,
  Duplicate,
  Get,
  SaveExport,
} from "../../wailsjs/go/character/Service";
import { character } from "../../wailsjs/go/models";

interface Props {
  id: number;
  dev: boolean;
  onChat: (id: number) => void;
  onEdit: (detail: character.Detail, avatarUri: string) => void;
  onBack: () => void;
  onDuplicated: (newId: number) => void;
  onDeleted: () => void;
  onError: (msg: string) => void;
  onNotice: (msg: string) => void;
}

// Field is one labelled block of card text. Empty fields render
// nothing: the view shows what the card has, not what it lacks.
function Field({ label, text, prose }: { label: string; text: string; prose?: boolean }) {
  if (!text.trim()) return null;
  return (
    <div>
      <p className="text-sm font-medium">{label}</p>
      <p
        className={
          "mt-1 max-w-prose whitespace-pre-wrap text-sm leading-relaxed " +
          (prose ? "font-serif text-foreground" : "text-muted-foreground")
        }
      >
        {text}
      </p>
    </div>
  );
}

// CharacterDetail is the read view of one card: portrait, name, the
// core fields at a glance, advanced fields under a hairline, and the
// actions (Chat, Edit, More: Duplicate / Export / Delete).
export default function CharacterDetail({
  id,
  dev,
  onChat,
  onEdit,
  onBack,
  onDuplicated,
  onDeleted,
  onError,
  onNotice,
}: Props) {
  const [detail, setDetail] = useState<character.Detail | null>(null);
  const [avatar, setAvatar] = useState("");
  const [menuOpen, setMenuOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    Get(id)
      .then(setDetail)
      .catch((err) => onError(String(err)));
    Avatar(id)
      .then(setAvatar)
      .catch(() => {});
  }, [id, onError]);

  // Click outside closes the menu.
  useEffect(() => {
    if (!menuOpen) return;
    const close = (e: MouseEvent) => {
      if (!menuRef.current?.contains(e.target as Node)) setMenuOpen(false);
    };
    document.addEventListener("mousedown", close);
    return () => document.removeEventListener("mousedown", close);
  }, [menuOpen]);

  if (!detail) {
    return <p className="text-sm text-muted-foreground">Loading…</p>;
  }
  const f = detail.form;

  const duplicate = async () => {
    setMenuOpen(false);
    try {
      const copy = await Duplicate(id);
      onDuplicated(copy.id);
    } catch (err) {
      onError(String(err));
    }
  };

  const exportJSON = async () => {
    setMenuOpen(false);
    try {
      const path = await SaveExport(id);
      if (path) onNotice(`Exported ${detail.name} to ${path}.`);
    } catch (err) {
      onError(String(err));
    }
  };

  const remove = async () => {
    setMenuOpen(false);
    try {
      const n = await ChatCount(id);
      const chats =
        n === 0
          ? "There are no chats with them."
          : `${n} chat${n === 1 ? "" : "s"} with them will stay readable but can't be continued.`;
      if (!window.confirm(`Delete ${detail.name}? ${chats}`)) return;
      await Delete(id);
      onDeleted();
    } catch (err) {
      onError(String(err));
    }
  };

  const advanced = [
    ["Nickname", f.nickname],
    ["System prompt", f.systemPrompt],
    ["Example dialogue", f.mesExample],
    ["Alternate greetings", (f.alternateGreetings ?? []).join("\n\n")],
    ["Post-history instructions", f.postHistoryInstructions],
    ["Creator notes", f.creatorNotes],
    ["Tags", (f.tags ?? []).join(", ")],
  ].filter(([, text]) => text && text.trim());

  return (
    <div className="space-y-6">
      <button
        className="text-sm text-muted-foreground hover:text-foreground"
        onClick={onBack}
      >
        All characters
      </button>

      <div className="flex gap-6">
        <div className="w-48 shrink-0 self-start overflow-hidden rounded-md bg-card ring-1 ring-border">
          {avatar ? (
            <img src={avatar} alt="" className="aspect-[2/3] w-full object-cover" />
          ) : (
            <div className="flex aspect-[2/3] w-full items-center justify-center font-title text-6xl text-muted-foreground">
              {detail.name.charAt(0).toUpperCase()}
            </div>
          )}
        </div>
        <div className="min-w-0 flex-1">
          <h2 className="font-title text-3xl leading-none">{detail.name}</h2>
          <p className="mt-2 text-xs text-muted-foreground">
            {[
              detail.spec === "chara_card_v3" ? "V3 card" : detail.spec === "chara_card_v2" ? "V2 card" : "V1 card",
              detail.hasLorebook ? "lorebook (not yet supported)" : "",
              detail.chatCount > 0
                ? `${detail.chatCount} chat${detail.chatCount === 1 ? "" : "s"}`
                : "",
            ]
              .filter(Boolean)
              .join(", ")}
          </p>
          <div className="mt-4 flex items-center gap-2">
            <Button onClick={() => onChat(id)}>Chat</Button>
            <Button variant="outline" onClick={() => onEdit(detail, avatar)}>
              Edit
            </Button>
            <div className="relative" ref={menuRef}>
              <Button
                variant="ghost"
                aria-haspopup="menu"
                aria-expanded={menuOpen}
                onClick={() => setMenuOpen((o) => !o)}
              >
                More
              </Button>
              {menuOpen && (
                <div
                  role="menu"
                  className="absolute left-0 top-full z-10 mt-1 min-w-40 rounded-md bg-card py-1 ring-1 ring-border"
                >
                  {[
                    ["Duplicate", duplicate, false],
                    ["Export as JSON", exportJSON, false],
                    ["Delete", remove, true],
                  ].map(([label, action, danger]) => (
                    <button
                      key={label as string}
                      role="menuitem"
                      className={
                        "block w-full px-3 py-1.5 text-left text-sm hover:bg-accent " +
                        (danger ? "text-destructive" : "")
                      }
                      onClick={() => void (action as () => Promise<void>)()}
                    >
                      {label as string}
                    </button>
                  ))}
                </div>
              )}
            </div>
          </div>
          <div className="mt-6 space-y-4">
            <Field label="Description" text={f.description} />
            <Field label="Personality" text={f.personality} />
            <Field label="Scenario" text={f.scenario} />
            <Field label="First message" text={f.greeting} prose />
          </div>
        </div>
      </div>

      {(advanced.length > 0 || detail.extra.length > 0) && (
        <details open={dev} className="border-t border-border pt-4">
          <summary className="cursor-pointer select-none text-sm font-medium text-muted-foreground hover:text-foreground">
            Advanced
          </summary>
          <div className="mt-4 space-y-4">
            {advanced.map(([label, text]) => (
              <Field key={label} label={label} text={text} />
            ))}
            {detail.extra.length > 0 && (
              <p className="text-xs text-muted-foreground">
                Also stored on this card: {detail.extra.join(", ")}.
              </p>
            )}
          </div>
        </details>
      )}
    </div>
  );
}
