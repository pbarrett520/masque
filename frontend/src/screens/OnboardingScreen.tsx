import { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Set as SetSetting } from "../../wailsjs/go/settings/Service";
import { Status } from "../../wailsjs/go/ollamamgr/Service";
import { Health, ListModels } from "../../wailsjs/go/chat/Service";
import { BrowserOpenURL } from "../../wailsjs/runtime/runtime";
import { ollamamgr, provider } from "../../wailsjs/go/models";
import StarterModelList from "@/components/StarterModelList";

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
      <div className="w-full max-w-lg space-y-4 py-8">
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

function WelcomeStep({
  onLocal,
  onCloud,
  onSkip,
}: {
  onLocal: () => void;
  onCloud: () => void;
  onSkip: () => void;
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Welcome to Masque</CardTitle>
        <CardDescription>
          Chat with characters, entirely on your machine. One quick choice:
          where should the AI run?
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <button
          className="w-full rounded-md border border-primary/60 p-3 text-left hover:bg-muted/50"
          onClick={onLocal}
        >
          <div className="font-medium">On this computer (recommended)</div>
          <div className="text-sm text-muted-foreground">
            Private and free, powered by Ollama. Nothing ever leaves your
            machine.
          </div>
        </button>
        <button
          className="w-full rounded-md border border-border p-3 text-left hover:bg-muted/50"
          onClick={onCloud}
        >
          <div className="font-medium">In the cloud with my API key</div>
          <div className="text-sm text-muted-foreground">
            Use OpenRouter, OpenAI, Anthropic, or any compatible service.
          </div>
        </button>
        <div className="pt-1 text-right">
          <Button variant="ghost" size="sm" onClick={onSkip}>
            Skip for now
          </Button>
        </div>
      </CardContent>
    </Card>
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
          one-time install — everything after this happens inside Masque.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <ol className="list-decimal space-y-2 pl-5 text-sm">
          <li>
            Download and run the installer from{" "}
            <button
              className="text-primary underline"
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
            {checking ? "Checking…" : "I've installed it — check again"}
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
  const [providerId, setProviderId] = useState("openai");
  const [baseUrl, setBaseUrl] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [models, setModels] = useState<provider.ModelInfo[]>([]);
  const [model, setModel] = useState("");
  const [testing, setTesting] = useState(false);
  const [tested, setTested] = useState(false);

  const keySetting = `provider.${providerId}.api_key`;
  const urlSetting = `provider.${providerId}.base_url`;

  const test = async () => {
    setTesting(true);
    setTested(false);
    setModels([]);
    setModel("");
    onError("");
    try {
      // Save first: providers read settings fresh on every call.
      await SetSetting(urlSetting, baseUrl.trim() === "" ? null : baseUrl.trim());
      await SetSetting(keySetting, apiKey.trim() === "" ? null : apiKey.trim());
      const health = await Health(providerId);
      if (health) {
        onError(`Connection failed: ${health}`);
        return;
      }
      const list = (await ListModels(providerId)) ?? [];
      setModels(list);
      setTested(true);
      if (list.length > 0) setModel(list[0].id);
    } catch (err) {
      onError(String(err));
    } finally {
      setTesting(false);
    }
  };

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
          <select
            id="ob-provider"
            className="h-9 w-full rounded-md border border-input bg-background px-2 text-sm"
            value={providerId}
            onChange={(e) => {
              setProviderId(e.target.value);
              setTested(false);
              setModels([]);
              setModel("");
            }}
          >
            <option value="openai">OpenAI-compatible (OpenRouter, LM Studio, …)</option>
            <option value="anthropic">Anthropic</option>
          </select>
        </div>
        {providerId === "openai" && (
          <div className="space-y-1.5">
            <Label htmlFor="ob-url">Base URL</Label>
            <Input
              id="ob-url"
              placeholder="https://openrouter.ai/api/v1"
              value={baseUrl}
              onChange={(e) => setBaseUrl(e.target.value)}
            />
          </div>
        )}
        <div className="space-y-1.5">
          <Label htmlFor="ob-key">API key</Label>
          <Input
            id="ob-key"
            type="password"
            placeholder={providerId === "anthropic" ? "sk-ant-…" : "sk-…"}
            value={apiKey}
            onChange={(e) => setApiKey(e.target.value)}
          />
        </div>
        <div className="flex gap-2">
          <Button onClick={() => void test()} disabled={testing}>
            {testing ? "Testing…" : "Test connection"}
          </Button>
          <Button variant="ghost" onClick={onBack}>
            Back
          </Button>
        </div>
        {tested && (
          <div className="space-y-1.5">
            <Label htmlFor="ob-model">Model</Label>
            <select
              id="ob-model"
              className="h-9 w-full rounded-md border border-input bg-background px-2 text-sm"
              value={model}
              onChange={(e) => setModel(e.target.value)}
            >
              {models.map((m) => (
                <option key={m.id} value={m.id}>
                  {m.id}
                </option>
              ))}
            </select>
            <Button
              className="mt-2"
              disabled={!model}
              onClick={() => onFinish(providerId, model)}
            >
              Finish setup
            </Button>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
