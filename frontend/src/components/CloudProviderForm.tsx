import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { setSetting } from "@/lib/settings";
import { Get } from "../../wailsjs/go/settings/Service";
import { Catalog } from "../../wailsjs/go/chat/Service";
import { BrowserOpenURL } from "../../wailsjs/runtime/runtime";
import { chat, presets } from "../../wailsjs/go/models";

export const keySetting = (id: string) => `provider.${id}.api_key`;
export const urlSetting = (id: string) => `provider.${id}.base_url`;

// describeCatalog is the one-line outcome of a connection test, shared
// by onboarding and settings so the wording stays identical.
export function describeCatalog(c: presets.Catalog, label: string): string {
  if (c.keyRejected) return `${label} rejected the key: ${c.listError}`;
  if (c.listed) {
    const n = c.all.length;
    return `Connected to ${label}. ${n} chat model${n === 1 ? "" : "s"} available.`;
  }
  if (c.recommended.length > 0) {
    return `Connected to ${label}, but it didn't list its models (${c.listError}). Showing the recommended ones.`;
  }
  return `Couldn't reach ${label}: ${c.listError}`;
}

interface Props {
  provider: chat.ProviderInfo;
  // Called after a test that leaves the provider usable (key accepted,
  // or the host simply has no model list). The key is already saved.
  onConnected: (catalog: presets.Catalog) => void;
  onError: (msg: string) => void;
  // Button label; defaults to "Connect".
  action?: string;
  autoFocus?: boolean;
}

// CloudProviderForm is the key (and, for custom presets, base URL)
// entry for one cloud provider, with the connection test that doubles
// as key validation: the provider's model list is fetched, and a 401 or
// 403 unsaves the key so a bad one never lingers as "configured".
export default function CloudProviderForm({
  provider,
  onConnected,
  onError,
  action = "Connect",
  autoFocus,
}: Props) {
  const [apiKey, setApiKey] = useState("");
  const [baseUrl, setBaseUrl] = useState("");
  const [loaded, setLoaded] = useState(false);
  const [testing, setTesting] = useState(false);

  useEffect(() => {
    setLoaded(false);
    Promise.all([Get(keySetting(provider.id)), Get(urlSetting(provider.id))])
      .then(([k, u]) => {
        setApiKey(typeof k === "string" ? k : "");
        setBaseUrl(typeof u === "string" ? u : "");
      })
      .catch(() => {})
      .finally(() => setLoaded(true));
  }, [provider.id]);

  const test = async () => {
    const key = apiKey.trim();
    const url = baseUrl.trim().replace(/\/+$/, "");
    if (provider.custom && !url) {
      onError("Enter the server's base URL first.");
      return;
    }
    if (!provider.custom && !key) {
      onError("Paste an API key first.");
      return;
    }
    setTesting(true);
    onError("");
    try {
      // Save first: providers read settings fresh on every call. Only
      // custom presets carry a base URL; the others use the registry's.
      if (provider.custom) await setSetting(urlSetting(provider.id), url || null);
      await setSetting(keySetting(provider.id), key || null);
      const catalog = await Catalog(provider.id);
      if (catalog.keyRejected) {
        await setSetting(keySetting(provider.id), null);
        onError(describeCatalog(catalog, provider.label));
        return;
      }
      if (!catalog.listed && catalog.recommended.length === 0) {
        onError(describeCatalog(catalog, provider.label));
        return;
      }
      onConnected(catalog);
    } catch (err) {
      onError(String(err));
    } finally {
      setTesting(false);
    }
  };

  const keyId = `key-${provider.id}`;
  const urlId = `url-${provider.id}`;

  return (
    <div className="space-y-3">
      {provider.custom && (
        <div className="space-y-1.5">
          <Label htmlFor={urlId}>Base URL</Label>
          <Input
            id={urlId}
            placeholder={
              provider.format === "anthropic"
                ? "https://your-proxy.example"
                : "http://localhost:1234/v1"
            }
            value={baseUrl}
            disabled={!loaded}
            autoFocus={autoFocus}
            onChange={(e) => setBaseUrl(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && void test()}
          />
        </div>
      )}
      <div className="space-y-1.5">
        <div className="flex items-baseline justify-between">
          <Label htmlFor={keyId}>
            {provider.custom ? "API key (if the server needs one)" : "API key"}
          </Label>
          {provider.keyUrl && (
            <button
              type="button"
              className="text-xs text-muted-foreground underline decoration-gilt underline-offset-2 hover:text-gilt"
              onClick={() => BrowserOpenURL(provider.keyUrl)}
            >
              Get a key
            </button>
          )}
        </div>
        <Input
          id={keyId}
          type="password"
          autoComplete="off"
          value={apiKey}
          disabled={!loaded}
          autoFocus={autoFocus && !provider.custom}
          onChange={(e) => setApiKey(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && void test()}
        />
      </div>
      <Button onClick={() => void test()} disabled={!loaded || testing}>
        {testing ? "Connecting…" : action}
      </Button>
    </div>
  );
}
