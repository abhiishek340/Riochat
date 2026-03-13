export const THEME_MODES = ["light", "dark", "system"] as const;
export type ThemeMode = (typeof THEME_MODES)[number];

/** Accent presets. Each one re-tints the same neutral surfaces. */
export const ACCENTS = ["tide", "ember", "moss", "dusk"] as const;
export type Accent = (typeof ACCENTS)[number];

export const DEFAULT_MODE: ThemeMode = "system";
export const DEFAULT_ACCENT: Accent = "tide";

export const MODE_STORAGE_KEY = "theme";
export const ACCENT_STORAGE_KEY = "theme-preset";

export function isMode(value: unknown): value is ThemeMode {
  return typeof value === "string" && (THEME_MODES as readonly string[]).includes(value);
}

export function isAccent(value: unknown): value is Accent {
  return typeof value === "string" && (ACCENTS as readonly string[]).includes(value);
}

/**
 * Runs in <head> before first paint so a stored dark theme never flashes
 * light. Must stay dependency-free and swallow every error.
 */
export const THEME_BOOTSTRAP_SCRIPT = `(function(){try{
var d=document.documentElement,s=window.localStorage;
var m=s.getItem(${JSON.stringify(MODE_STORAGE_KEY)});
if(m!=="light"&&m!=="dark"&&m!=="system")m="system";
var a=s.getItem(${JSON.stringify(ACCENT_STORAGE_KEY)});
if(${JSON.stringify(ACCENTS)}.indexOf(a)<0)a=${JSON.stringify(DEFAULT_ACCENT)};
var dark=m==="dark"||(m==="system"&&window.matchMedia("(prefers-color-scheme: dark)").matches);
d.classList.remove("light","dark");d.classList.add(dark?"dark":"light");
d.setAttribute("data-accent",a);d.style.colorScheme=dark?"dark":"light";
var fs=s.getItem("riochat:font-size");if(fs==="small"||fs==="medium"||fs==="large")d.setAttribute("data-font-size",fs);
var cf=s.getItem("riochat:chat-font");if(cf==="songti"||cf==="heiti"||cf==="mono")d.setAttribute("data-chat-font",cf);
var cw=s.getItem("riochat:chat-font-weight");if(cw==="medium"||cw==="semibold"||cw==="bold")d.setAttribute("data-chat-font-weight",cw);
}catch(e){}})();`;
