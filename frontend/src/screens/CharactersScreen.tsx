import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Avatar,
  Create,
  Get,
  Import,
  List,
  Update,
} from "../../wailsjs/go/character/Service";
import { character } from "../../wailsjs/go/models";
import CharacterForm, { emptyForm, fileToBase64 } from "@/components/CharacterForm";
import CharacterDetail from "@/components/CharacterDetail";

interface Props {
  // Opens (or resumes) a chat with the character.
  onOpen: (characterId: number) => void;
  dev: boolean;
  // Reports unsaved edits so the app can confirm before leaving the tab.
  onDirtyChange: (dirty: boolean) => void;
  // A character was deleted: open chats and the chat list must re-resolve.
  onCharacterDeleted: () => void;
}

type Mode =
  | { kind: "grid" }
  | { kind: "create" }
  | { kind: "detail"; id: number }
  | { kind: "edit"; id: number; detail: character.Detail; avatarUri: string };

export default function CharactersScreen({ onOpen, dev, onDirtyChange, onCharacterDeleted }: Props) {
  const [characters, setCharacters] = useState<character.View[]>([]);
  const [avatars, setAvatars] = useState<Record<number, string>>({});
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [dragging, setDragging] = useState(false);
  const [mode, setMode] = useState<Mode>({ kind: "grid" });
  const dirtyRef = useRef(false);
  const fileRef = useRef<HTMLInputElement>(null);

  const setDirty = useCallback(
    (d: boolean) => {
      dirtyRef.current = d;
      onDirtyChange(d);
    },
    [onDirtyChange]
  );

  // leave guards every transition away from a dirty form.
  const leave = (next: Mode) => {
    if (dirtyRef.current && !window.confirm("Discard unsaved changes?")) return;
    setDirty(false);
    setError("");
    setNotice("");
    setMode(next);
  };

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

  const create = async (form: character.CardForm) => {
    const view = await Create(form);
    setNotice(`Created ${view.name}.`);
    setDirty(false);
    await load();
    setMode({ kind: "detail", id: view.id });
  };

  const update = async (id: number, form: character.CardForm) => {
    const view = await Update(id, form);
    setNotice(`Saved ${view.name}.`);
    setDirty(false);
    setAvatars((a) => {
      const next = { ...a };
      delete next[id]; // re-fetched by load if still present
      return next;
    });
    await load();
    setMode({ kind: "detail", id });
  };

  const header = (
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
      <Button onClick={() => leave(mode.kind === "create" ? { kind: "grid" } : { kind: "create" })}>
        {mode.kind === "create" ? "Cancel" : "Create"}
      </Button>
    </div>
  );

  const messages = (
    <>
      {error && (
        <div className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {error}
        </div>
      )}
      {notice && <p className="text-sm text-muted-foreground">{notice}</p>}
    </>
  );

  if (mode.kind === "detail") {
    return (
      <div className="mx-auto max-w-3xl space-y-4">
        {messages}
        <CharacterDetail
          id={mode.id}
          dev={dev}
          onChat={onOpen}
          onBack={() => leave({ kind: "grid" })}
          onEdit={(detail, avatarUri) => {
            setNotice("");
            setMode({ kind: "edit", id: mode.id, detail, avatarUri });
          }}
          onDuplicated={(newId) => {
            setNotice("");
            void load();
            Promise.all([Get(newId), Avatar(newId).catch(() => "")])
              .then(([detail, avatarUri]) =>
                setMode({ kind: "edit", id: newId, detail, avatarUri: avatarUri || "" })
              )
              .catch((err) => setError(String(err)));
          }}
          onDeleted={() => {
            void load();
            onCharacterDeleted();
            setNotice("Deleted. Existing chats stay readable.");
            setMode({ kind: "grid" });
          }}
          onError={setError}
          onNotice={setNotice}
        />
      </div>
    );
  }

  if (mode.kind === "edit") {
    return (
      <div className="mx-auto max-w-3xl space-y-4">
        {messages}
        <Card>
          <CardHeader>
            <CardTitle>Edit {mode.detail.name}</CardTitle>
          </CardHeader>
          <CardContent>
            <CharacterForm
              mode="edit"
              initial={mode.detail.form}
              avatarUri={mode.avatarUri}
              dev={dev}
              onSave={(form) => update(mode.id, form)}
              onCancel={() => leave({ kind: "detail", id: mode.id })}
              onDirty={setDirty}
            />
          </CardContent>
        </Card>
      </div>
    );
  }

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
      {header}

      <p className="text-sm text-muted-foreground">
        Import a character card (PNG or JSON, V2 or V3), or drop files
        anywhere on this screen.
      </p>

      {messages}

      {mode.kind === "create" && (
        <Card>
          <CardHeader>
            <CardTitle>New character</CardTitle>
          </CardHeader>
          <CardContent>
            <CharacterForm
              mode="create"
              initial={emptyForm()}
              dev={dev}
              onSave={create}
              onCancel={() => leave({ kind: "grid" })}
              onDirty={setDirty}
            />
          </CardContent>
        </Card>
      )}

      {/* The playbill: portrait tiles, name beneath in the title face.
          Clicking a tile opens its card; Chat on the tile is the fast path. */}
      <div className="grid grid-cols-2 gap-x-4 gap-y-6 pt-2 sm:grid-cols-3 md:grid-cols-4">
        {characters.map((c) => (
          <div
            key={c.id}
            role="button"
            tabIndex={0}
            aria-label={c.name}
            className="group relative cursor-pointer rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/70"
            onClick={() => leave({ kind: "detail", id: c.id })}
            onKeyDown={(e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                leave({ kind: "detail", id: c.id });
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
              className="absolute bottom-10 right-1.5 hidden rounded-sm bg-background/85 px-2 py-0.5 text-xs text-foreground hover:text-gilt focus-visible:block group-hover:block"
              aria-label={`Chat with ${c.name}`}
              onClick={(e) => {
                e.stopPropagation();
                onOpen(c.id);
              }}
            >
              Chat
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
