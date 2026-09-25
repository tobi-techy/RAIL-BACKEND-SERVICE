/**
 * Photon webhook deliveries name the platform in lowercase ("imessage").
 * spectrum-ts registers the provider as "iMessage" and looks that string up
 * exactly, then acknowledges the webhook without calling our handler.
 * Alias each registered name to its lowercase form on the same runtime map
 * the SDK closes over.
 */
export function aliasProviderPlatformKeys(
  platforms: Map<string, unknown>,
): string[] {
  const added: string[] = [];
  for (const [name, runtime] of [...platforms.entries()]) {
    const lower = name.toLowerCase();
    if (lower === name || platforms.has(lower)) continue;
    platforms.set(lower, runtime);
    added.push(lower);
  }
  return added;
}
