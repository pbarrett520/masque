import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Label } from "@/components/ui/label";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Avatar,
  Create,
  Delete,
  Import,
  List,
} from "../../wailsjs/go/character/Service";
import { character } from "../../wailsjs/go/models";

interface Props {
  onOpen: (characterId: number) => void;
}

async function fileToBase64(file: File): Promise<string> {
  const bytes = new Uint8Array(await file.arrayBuffer());
  let binary = "";
  const chunk = 0x8000;
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunk));
  }
  return btoa(binary);
}

export default function CharactersScreen({ onOpen }: Props) {
  const [characters, setCharacters] = useState<character.View[]>([]);
  const [avatars, setAvatars] = useState<Record<number, string>>({});
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [dragging, setDragging] = useState(false);
  const [showCreate, setShowCreate] = useState(false);
  const [form, setForm] = useState({
    name: "",
    description: "",
    personality: "",
    scenario: "",
    greeting: "",
  });
  const fileRef = useRef<HTMLInputElement>(null);

  const load = useCallback(async () => {
    try {
      const list = (await List()) ?? [];
      setCharacters(list);
      for (const c of list) {
        if (!c.hasAvatar) continue;
        Avatar(c.id)
          .then((uri) => uri && setAvatars((a) => ({ ...a, [c.id]: uri })))
          .catch(() => {});
      }
    } catch (err) {
      setError(`Failed to load characters: ${err}`);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const importFiles = async (files: FileList | File[]) => {
    setError("");
    setNotice("");
    for (const file of Array.from(files)) {
      try {
        const view = await Import(await fileToBase64(file), file.name);
        setNotice(
          `Imported ${view.name}.` +
            (view.hasLorebook
              ? " This card contains a lorebook; not yet supported."
              : "")
        );
      } catch (err) {
        setError(String(err));
      }
    }
    await load();
  };

  const create = async () => {
    setError("");
    try {
      const view = await Create(
        character.CreateForm.createFrom({ ...form, avatarB64: "" })
      );
      setNotice(`Created ${view.name}.`);
      setShowCreate(false);
      setForm({ name: "", description: "", personality: "", scenario: "", greeting: "" });
      await load();
    } catch (err) {
      setError(String(err));
    }
  };

  const remove = async (c: character.View) => {
    if (!window.confirm(`Delete ${c.name} and all their chats?`)) return;
    try {
      await Delete(c.id);
      await load();
    } catch (err) {
      setError(String(err));
    }
  };

  const field = (
    key: keyof typeof form,
    label: string,
    placeholder: string,
    multiline = true
  ) => {
    const Control = multiline ? Textarea : Input;
    return (
      <div className="space-y-1.5">
        <Label htmlFor={`create-${key}`}>{label}</Label>
        <Control
          id={`create-${key}`}
          className={multiline ? "max-h-64" : undefined}
          value={form[key]}
          placeholder={placeholder}
          onChange={(e) => setForm((f) => ({ ...f, [key]: e.target.value }))}
        />
      </div>
    );
  };

  return (
    <div
      className={
        "mx-auto max-w-3xl space-y-4" + (dragging ? " opacity-60" : "")
      }
      onDragOver={(e) => {
        e.preventDefault();
        setDragging(true);
      }}
      onDragLeave={() => setDragging(false)}
      onDrop={(e) => {
        e.preventDefault();
        setDragging(false);
        if (e.dataTransfer.files.length) void importFiles(e.dataTransfer.files);
      }}
    >
      <div className="flex items-center gap-2">
        <h2 className="font-title text-2xl leading-none">Characters</h2>
        <span className="flex-1" />
        <input
          ref={fileRef}
          type="file"
          accept=".png,.json,image/png,application/json"
          multiple
          className="hidden"
          onChange={(e) => {
            if (e.target.files?.length) void importFiles(e.target.files);
            e.target.value = "";
          }}
        />
        <Button variant="outline" onClick={() => fileRef.current?.click()}>
          Import card…
        </Button>
        <Button onClick={() => setShowCreate((v) => !v)}>
          {showCreate ? "Cancel" : "Create"}
        </Button>
      </div>

      <p className="text-sm text-muted-foreground">
        Import a character card (PNG or JSON, V2 or V3), or drop files
        anywhere on this screen.
      </p>

      {error && (
        <div className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {error}
        </div>
      )}
      {notice && <p className="text-sm text-muted-foreground">{notice}</p>}

      {showCreate && (
        <Card>
          <CardHeader>
            <CardTitle>New character</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            {field("name", "Name", "Required", false)}
            {field("description", "Description", "Who are they?")}
            {field("personality", "Personality", "A few traits")}
            {field("scenario", "Scenario", "Where does the story start?")}
            {field("greeting", "Greeting", "Their first message ({{user}} works here)")}
            <Button onClick={create} disabled={!form.name.trim()}>
              Create character
            </Button>
          </CardContent>
        </Card>
      )}

      {/* The playbill: portrait tiles, name beneath in the title face. */}
      <div className="grid grid-cols-2 gap-x-4 gap-y-6 pt-2 sm:grid-cols-3 md:grid-cols-4">
        {characters.map((c) => (
          <div
            key={c.id}
            role="button"
            tabIndex={0}
            className="group relative cursor-pointer rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/70"
            onClick={() => onOpen(c.id)}
            onKeyDown={(e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                onOpen(c.id);
              }
            }}
          >
            <div className="overflow-hidden rounded-md bg-card ring-1 ring-border transition-shadow group-hover:ring-gilt">
              {avatars[c.id] ? (
                <img
                  src={avatars[c.id]}
                  alt=""
                  className="aspect-[2/3] w-full object-cover"
                />
              ) : (
                <div className="flex aspect-[2/3] w-full items-center justify-center font-title text-5xl text-muted-foreground">
                  {c.name.charAt(0).toUpperCase()}
                </div>
              )}
            </div>
            <div className="flex items-baseline gap-1.5 px-0.5 pt-2">
              <span className="truncate font-title text-base">{c.name}</span>
              {c.hasLorebook && (
                <span
                  className="text-xs text-muted-foreground"
                  title="This card contains a lorebook; not yet supported."
                >
                  lorebook
                </span>
              )}
            </div>
            <button
              className="absolute right-1.5 top-1.5 hidden rounded-sm bg-background/85 px-1.5 py-0.5 text-xs text-muted-foreground hover:text-destructive group-hover:block"
              onClick={(e) => {
                e.stopPropagation();
                void remove(c);
              }}
            >
              Delete
            </button>
          </div>
        ))}
        {characters.length === 0 && (
          <p className="col-span-full py-16 text-center text-sm text-muted-foreground">
            No characters yet. Import a card or create one.
          </p>
        )}
      </div>
    </div>
  );
}
