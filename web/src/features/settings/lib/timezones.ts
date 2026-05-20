const FALLBACK = [
  "Etc/UTC",
  "America/Los_Angeles",
  "America/Denver",
  "America/Chicago",
  "America/New_York",
  "America/Sao_Paulo",
  "Europe/London",
  "Europe/Paris",
  "Europe/Berlin",
  "Europe/Moscow",
  "Africa/Cairo",
  "Asia/Dubai",
  "Asia/Kolkata",
  "Asia/Bangkok",
  "Asia/Shanghai",
  "Asia/Tokyo",
  "Asia/Seoul",
  "Australia/Sydney",
  "Pacific/Auckland",
];

/** Zones to offer: the runtime's list when it has one, always including `current`. */
export function timeZoneOptions(current: string): string[] {
  let list: string[] = FALLBACK;
  try {
    const intl = Intl as unknown as { supportedValuesOf?: (key: string) => string[] };
    const all = intl.supportedValuesOf?.("timeZone");
    if (all && all.length > 0) list = all.includes("Etc/UTC") ? all : ["Etc/UTC", ...all];
  } catch {
    /* keep the fallback list */
  }
  return current && !list.includes(current) ? [current, ...list] : list;
}
