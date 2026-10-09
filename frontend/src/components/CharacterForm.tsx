import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Label } from "@/components/ui/label";
import { character } from "../../wailsjs/go/models";

export async function fileToBase64(file: File): Promise<string> {
  const bytes = new Uint8Array(await file.arrayBuffer());
  let binary = "";
  const chunk = 0x8000;
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunk));
  }
  return btoa(binary);
}

// emptyForm is a blank CardForm for creation.
export function emptyForm(): character.CardForm {
  return character.CardForm.createFrom({
    name: "",
    nickname: "",
    description: "",
    personality: "",
    scenario: "",
    greeting: "",
    alternateGreetings: [],
    mesExample: "",
    systemPrompt: "",
    postHistoryInstructions: "",
    creatorNotes: "",
    tags: [],
    avatarB64: "",
    removeAvatar: false,
  });
}

// Multi-line list fields travel as arrays; the form edits them one
// item per line.
const lines = (items: string[] | undefined) => (items ?? []).join("\n");
const unlines = (text: string) =>
  text
    .split("\n")
    .map((s) => s.trim())
    .filter(Boolean);

interface Props {
  mode: "create" | "edit";
  initial: character.CardForm;
  // Current avatar (edit) as a data URI, "" for none.
  avatarUri?: string;
  dev: boolean;
  onSave: (form: character.CardForm) => Promise<void>;
  onCancel: () => void;
  // Reports whether the form differs from initial, so the screen can
  // guard navigation.
  onDirty?: (dirty: boolean) => void;
}

// CharacterForm is the one card form for create and edit: the core
// fields always visible, everything else under Advanced (open by
// default in dev mode). Validation matches the Go side: a name is
// required.
export default function CharacterForm({
  mode,
  initial,
  avatarUri = "",
  dev,
  onSave,
  onCancel,
  onDirty,
}: Props) {
  const [form, setForm] = useState<character.CardForm>(initial);
  const [alternates, setAlternates] = useState(lines(initial.alternateGreetings));
  const [tags, setTags] = useState((initial.tags ?? []).join(", "));
  const [avatarPreview, setAvatarPreview] = useState(avatarUri);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const fileRef = useRef<HTMLInputElement>(null);

  const current = (): character.CardForm =>
    character.CardForm.createFrom({
      ...form,
      alternateGreetings: unlines(alternates),
      tags: tags
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean),
    });

  const dirty =
    JSON.stringify({ ...current(), avatarB64: "", removeAvatar: false }) !==
      JSON.stringify({ ...initial, avatarB64: "", removeAvatar: false }) ||
    form.avatarB64 !== "" ||
    form.removeAvatar;

  useEffect(() => {
    onDirty?.(dirty);
  }, [dirty, onDirty]);

  const pickAvatar = async (file: File) => {
    if (file.type !== "image/png") {
      setError("The avatar must be a PNG image.");
      return;
    }
    setError("");
    const b64 = await fileToBase64(file);
    setForm((f) => character.CardForm.createFrom({ ...f, avatarB64: b64, removeAvatar: false }));
    setAvatarPreview(`data:image/png;base64,${b64}`);
  };

  const save = async () => {
    const f = current();
    if (!f.name.trim()) {
      setError("A name is required.");
      return;
    }
    setSaving(true);
    setError("");
    try {
      await onSave(f);
    } catch (err) {
      setError(String(err));
    } finally {
      setSaving(false);
    }
  };

  const set = (key: keyof character.CardForm, value: string) =>
    setForm((f) => character.CardForm.createFrom({ ...f, [key]: value }));

  const field = (
    key: keyof character.CardForm,
    label: string,
    placeholder: string,
    multiline = true
  ) => {
    const Control = multiline ? Textarea : Input;
    return (
      <div className="space-y-1.5">
        <Label htmlFor={`card-${key}`}>{label}</Label>
        <Control
          id={`card-${key}`}
          className={multiline ? "max-h-64" : undefined}
          value={(form[key] as string) ?? ""}
          placeholder={placeholder}
          onChange={(e) => set(key, e.target.value)}
        />
      </div>
    );
  };

  return (
    <div className="space-y-3">
      {mode === "edit" && (
        <p className="text-sm text-muted-foreground">
          Changes apply to future replies in existing chats. The first
          message only affects new chats.
        </p>
      )}
      {field("name", "Name", "Required", false)}
      <div className="space-y-1.5">
        <Label>Avatar</Label>
        <div className="flex items-center gap-3">
          {avatarPreview ? (
            <img src={avatarPreview} alt="" className="h-20 w-[3.333rem] rounded-sm object-cover ring-1 ring-border" />
          ) : (
            <div className="flex h-20 w-[3.333rem] items-center justify-center rounded-sm bg-card font-title text-2xl text-muted-foreground ring-1 ring-border">
              {form.name.trim().charAt(0).toUpperCase() || "?"}
            </div>
          )}
          <input
            ref={fileRef}
            type="file"
            accept=".png,image/png"
            className="hidden"
            onChange={(e) => {
              const file = e.target.files?.[0];
              if (file) void pickAvatar(file);
              e.target.value = "";
            }}
          />
          <Button variant="outline" size="sm" onClick={() => fileRef.current?.click()}>
            {avatarPreview ? "Replace PNG…" : "Choose PNG…"}
          </Button>
          {avatarPreview && (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                setAvatarPreview("");
                setForm((f) => character.CardForm.createFrom({ ...f, avatarB64: "", removeAvatar: true }));
              }}
            >
              Remove
            </Button>
          )}
        </div>
      </div>
      {field("description", "Description", "Who are they?")}
      {field("personality", "Personality", "A few traits")}
      {field("scenario", "Scenario", "Where does the story start?")}
      {field("greeting", "First message", "Their opening line ({{user}} works here)")}

      <details open={dev} className="group">
        <summary className="cursor-pointer select-none text-sm font-medium text-muted-foreground hover:text-foreground">
          Advanced
        </summary>
        <div className="mt-3 space-y-3 border-l border-border pl-3">
          {field("nickname", "Nickname", "Replaces {{char}} when set", false)}
          {field("systemPrompt", "System prompt", "Replaces the default instructions; {{original}} inserts them")}
          {field("mesExample", "Example dialogue", "<START>\n{{user}}: …\n{{char}}: …")}
          <div className="space-y-1.5">
            <Label htmlFor="card-alternates">Alternate greetings</Label>
            <Textarea
              id="card-alternates"
              className="max-h-64"
              value={alternates}
              placeholder="One per line; offered as swipes on the first message"
              onChange={(e) => setAlternates(e.target.value)}
            />
          </div>
          {field("postHistoryInstructions", "Post-history instructions", "Sent after the chat history")}
          {field("creatorNotes", "Creator notes", "Notes for people, not the model")}
          <div className="space-y-1.5">
            <Label htmlFor="card-tags">Tags</Label>
            <Input
              id="card-tags"
              value={tags}
              placeholder="Comma-separated"
              onChange={(e) => setTags(e.target.value)}
            />
          </div>
        </div>
      </details>

      {error && <p className="text-sm text-destructive">{error}</p>}
      <div className="flex gap-2 pt-1">
        <Button onClick={() => void save()} disabled={saving || !form.name.trim()}>
          {mode === "create" ? "Create character" : "Save"}
        </Button>
        <Button variant="ghost" onClick={onCancel} disabled={saving}>
          Cancel
        </Button>
      </div>
    </div>
  );
}
