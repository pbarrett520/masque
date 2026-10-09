import { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { setSetting as SetSetting } from "@/lib/settings";
import { Status } from "../../wailsjs/go/ollamamgr/Service";
import { Providers } from "../../wailsjs/go/chat/Service";
import { BrowserOpenURL } from "../../wailsjs/runtime/runtime";
import { chat, ollamamgr, presets } from "../../wailsjs/go/models";
import StarterModelList from "@/components/StarterModelList";
import CloudProviderForm, { describeCatalog } from "@/components/CloudProviderForm";
import ModelPicker from "@/components/ModelPicker";

type Step = "welcome" | "local" | "pick-model" | "cloud";

interface Props {
  // Called after setup finishes or is skipped; App re-resolves state.
  onDone: () => void;
}

// First-run onboarding (dev spec §9): welcome → local via Ollama (detect
// or walk through installing, then pull a starter model) or cloud via a
// pasted key. Existing installs never see this — App only shows it when
// neither onboarding.done nor a default model exists.
export default function OnboardingScreen({ onDone }: Props) {
  const [step, setStep] = useState<Step>("welcome");
  const [error, setError] = useState("");

  const finish = async (providerId?: string, model?: string) => {
    try {
      if (providerId && model) {
        await SetSetting("provider.default_id", providerId);
        await SetSetting("provider.default_model", model);
      }
      await SetSetting("onboarding.done", true);
      onDone();
    } catch (err) {
      setError(String(err));
    }
  };

  return (
    <div className="flex h-full items-start justify-center overflow-y-auto p-6">
      <div className="w-full max-w-lg space-y-4 py-10">
        {step === "welcome" && (
          <WelcomeStep
            onLocal={() => setStep("local")}
            onCloud={() => setStep("cloud")}
            onSkip={() => void finish()}
          />
        )}
        {step === "local" && (
          <LocalStep
            onReady={() => setStep("pick-model")}
            onBack={() => setStep("welcome")}
          />
        )}
        {step === "pick-model" && (
          <Card>
            <CardHeader>
              <CardTitle>Pick your first model</CardTitle>
              <CardDescription>
                These are community favorites for roleplay, sized for
                different machines. You can add or remove models later in
                Settings.
              </CardDescription>
            </CardHeader>
            <CardContent>
              <StarterModelList
                onUse={(ref) => void finish("ollama", ref)}
                onError={setError}
              />
            </CardContent>
          </Card>
        )}
        {step === "cloud" && (
          <CloudStep
            onFinish={(pid, model) => void finish(pid, model)}
            onBack={() => setStep("welcome")}
            onError={setError}
          />
        )}
        {error && (
          <p className="text-sm text-destructive">{error}</p>
        )}
      </div>
    </div>
  );
}

// The first thing a newcomer sees: the wordmark, one line on what this
// is, and one choice. Nothing else competes with it.
function WelcomeStep({
  onLocal,
  onCloud,
  onSkip,
}: {
  onLocal: () => void;
  onCloud: () => void;
  onSkip: () => void;
}) {
  const choice =
    "w-full rounded-md border border-border bg-card p-4 text-left transition-colors hover:border-gilt focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/70";
  return (
    <div>
      <h2 className="font-title text-[3.2rem] leading-none tracking-[-0.01em]">
        Masque
      </h2>
      <p className="mt-3 max-w-prose font-serif text-lg italic leading-snug text-muted-foreground">
        Chat with characters, entirely on your machine.
      </p>
      <p className="mt-8 text-sm">One quick choice: where should the AI run?</p>
      <div className="mt-3 space-y-3">
        <button className={choice} onClick={onLocal}>
          <div className="font-medium">On this computer</div>
          <div className="mt-0.5 text-sm text-muted-foreground">
            Private and free, powered by Ollama. Nothing ever leaves your
            machine. Recommended.
          </div>
        </button>
        <button className={choice} onClick={onCloud}>
          <div className="font-medium">In the cloud with my API key</div>
          <div className="mt-0.5 text-sm text-muted-foreground">
            OpenAI, Anthropic, OpenRouter, Gemini, and more. Bring an API key.
          </div>
        </button>
      </div>
      <div className="pt-3 text-right">
        <Button variant="ghost" size="sm" onClick={onSkip}>
          Skip for now
        </Button>
      </div>
    </div>
  );
}

function LocalStep({
  onReady,
  onBack,
}: {
  onReady: () => void;
  onBack: () => void;
}) {
  const [status, setStatus] = useState<ollamamgr.Status | null>(null);
  const [checking, setChecking] = useState(false);

  const probe = useCallback(async (thenAdvance: boolean) => {
    setChecking(true);
    try {
      const s = await Status();
      setStatus(s);
      if (s.reachable && thenAdvance) onReady();
    } finally {
      setChecking(false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    void probe(true); // Ollama already running: straight to model pick.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  if (!status) {
    return (
      <Card>
        <CardContent className="pt-6 text-sm text-muted-foreground">
          Looking for Ollama…
        </CardContent>
      </Card>
    );
  }

  if (status.reachable) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>Ollama found</CardTitle>
          <CardDescription>
            Version {status.version} is running at {status.baseUrl}.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Button onClick={onReady}>Continue</Button>
        </CardContent>
      </Card>
    );
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Install Ollama</CardTitle>
        <CardDescription>
          Masque uses Ollama to run models on your machine. It's a free,
          one-time install. Everything after this happens inside Masque.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <ol className="list-decimal space-y-2 pl-5 text-sm">
          <li>
            Download and run the installer from{" "}
            <button
              className="underline decoration-gilt underline-offset-2 hover:text-gilt"
              onClick={() => BrowserOpenURL("https://ollama.com/download")}
            >
              ollama.com/download
            </button>
            .
          </li>
          <li>Once it's installed and running, come back here.</li>
        </ol>
        <div className="flex gap-2">
          <Button onClick={() => void probe(true)} disabled={checking}>
            {checking ? "Checking…" : "I've installed it, check again"}
          </Button>
          <Button variant="ghost" onClick={onBack}>
            Back
          </Button>
        </div>
        <p className="text-xs text-muted-foreground">
          Still not found? Looked at {status.baseUrl}. If Ollama runs
          somewhere else, set its address later in Settings.
        </p>
      </CardContent>
    </Card>
  );
}

function CloudStep({
  onFinish,
  onBack,
  onError,
}: {
  onFinish: (providerId: string, model: string) => void;
  onBack: () => void;
  onError: (msg: string) => void;
}) {
  const [providers, setProviders] = useState<chat.ProviderInfo[]>([]);
  const [providerId, setProviderId] = useState("");
  const [catalog, setCatalog] = useState<presets.Catalog | null>(null);
  const [model, setModel] = useState("");

  useEffect(() => {
    Providers()
      .then((all) => {
        const cloud = (all ?? []).filter((p) => p.format !== "ollama");
        setProviders(cloud);
        if (cloud.length > 0) setProviderId(cloud[0].id);
      })
      .catch((err) => onError(String(err)));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const provider = providers.find((p) => p.id === providerId);
  const named = providers.filter((p) => !p.custom);
  const custom = providers.filter((p) => p.custom);

  return (
    <Card>
      <CardHeader>
        <CardTitle>Connect a cloud provider</CardTitle>
        <CardDescription>
          Your key is stored on this machine and only ever sent to the
          provider itself.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="space-y-1.5">
          <Label htmlFor="ob-provider">Provider</Label>
          <Select
            id="ob-provider"
            className="w-full"
            value={providerId}
            onChange={(e) => {
              setProviderId(e.target.value);
              setCatalog(null);
              setModel("");
              onError("");
            }}
          >
            {named.map((p) => (
              <option key={p.id} value={p.id}>
                {p.label}
              </option>
            ))}
            {custom.length > 0 && (
              <optgroup label="Something else">
                {custom.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.label}
                  </option>
                ))}
              </optgroup>
            )}
          </Select>
        </div>
        {provider && (
          <CloudProviderForm
            key={provider.id}
            provider={provider}
            onError={onError}
            onConnected={(c) => {
              setCatalog(c);
              setModel(c.default);
              onError("");
            }}
          />
        )}
        {catalog && provider && (
          <div className="space-y-1.5 border-t border-border pt-3">
            <p className="text-sm text-muted-foreground">
              {describeCatalog(catalog, provider.label)}
            </p>
            <Label htmlFor="ob-model">Model</Label>
            <ModelPicker
              id="ob-model"
              catalog={catalog}
              value={model}
              onChange={setModel}
            />
            <Button
              className="mt-2"
              disabled={!model}
              onClick={() => onFinish(providerId, model)}
            >
              Finish setup
            </Button>
          </div>
        )}
        <div>
          <Button variant="ghost" onClick={onBack}>
            Back
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}
