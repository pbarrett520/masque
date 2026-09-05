import { Delete, Set } from "../../wailsjs/go/settings/Service";

// setSetting writes a setting, or removes it when value is null. A null
// can't be sent through the bridge (Wails v2 rejects a JSON null bound
// to an `any` parameter and the promise never settles), so clears go
// through the dedicated Delete binding.
export function setSetting(key: string, value: unknown): Promise<void> {
  return value === null || value === undefined ? Delete(key) : Set(key, value);
}
