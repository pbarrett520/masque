import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
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
    placeholder: string
  ) => (
    <div className="space-y-1.5">
      <Label htmlFor={`create-${key}`}>{label}</Label>
      <Input
        id={`create-${key}`}
        value={form[key]}
        placeholder={placeholder}
        onChange={(e) => setForm((f) => ({ ...f, [key]: e.target.value }))}
      />
    </div>
  );

  return (
    <div
      className={
        "mx-auto max-w-3xl space-y-4" + (dragging ? " opacity-70" : "")
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
        <h2 className="text-lg font-medium">Characters</h2>
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

      <p className="text-xs text-muted-foreground">
        Import a character card (PNG or JSON, V2/V3) — or drag files anywhere
        on this screen.
      </p>

      {error && (
        <div className="rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive">
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
            {field("name", "Name", "Required")}
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

      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4">
        {characters.map((c) => (
          <div
            key={c.id}
            className="group relative cursor-pointer overflow-hidden rounded-lg border border-border bg-card transition-colors hover:border-primary"
            onClick={() => onOpen(c.id)}
          >
            {avatars[c.id] ? (
              <img
                src={avatars[c.id]}
                alt=""
                className="aspect-[2/3] w-full object-cover"
              />
            ) : (
              <div className="flex aspect-[2/3] w-full items-center justify-center bg-muted text-4xl font-semibold text-muted-foreground">
                {c.name.charAt(0).toUpperCase()}
              </div>
            )}
            <div className="flex items-center gap-1 px-2 py-1.5">
              <span className="truncate text-sm font-medium">{c.name}</span>
              {c.hasLorebook && (
                <span
                  className="rounded bg-muted px-1 text-[10px] uppercase text-muted-foreground"
                  title="This card contains a lorebook; not yet supported."
                >
                  lore
                </span>
              )}
            </div>
            <button
              className="absolute right-1 top-1 hidden rounded bg-background/80 px-1.5 py-0.5 text-xs text-destructive group-hover:block"
              onClick={(e) => {
                e.stopPropagation();
                void remove(c);
              }}
            >
              delete
            </button>
          </div>
        ))}
        {characters.length === 0 && (
          <p className="col-span-full py-10 text-center text-sm text-muted-foreground">
            No characters yet. Import a card or create one.
          </p>
        )}
      </div>
    </div>
  );
}
