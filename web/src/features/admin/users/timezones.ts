const FALLBACK = ["Etc/UTC", "Asia/Shanghai", "Asia/Tokyo", "Asia/Singapore", "Asia/Kolkata", "Europe/London", "Europe/Berlin", "Europe/Paris", "America/New_York", "America/Chicago", "America/Los_Angeles", "Australia/Sydney"];

/** IANA zones the browser knows, with `Etc/UTC` first; a short list where the browser cannot enumerate them. */
export function timezoneList(): string[] {
  let zones: string[] = [];
  try {
    const intl = Intl as unknown as { supportedValuesOf?: (key: string) => string[] };
    zones = intl.supportedValuesOf?.("timeZone") ?? [];
  } catch {
    zones = [];
  }
  const all = zones.length > 0 ? zones : FALLBACK;
  return ["Etc/UTC", ...all.filter((z) => z !== "Etc/UTC" && z !== "UTC")];
}
