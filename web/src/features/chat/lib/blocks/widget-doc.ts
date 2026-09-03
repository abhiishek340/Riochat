import { escapeScript, htmlDocument, type PreviewTheme } from "../sandbox-doc";

export const WIDGET_HEIGHT_MESSAGE = "riochat-widget-height";
export const WIDGET_MIN_HEIGHT = 60;
export const WIDGET_MAX_HEIGHT = 900;

const REPORTER = `(function(){var last=0;function send(){var h=Math.ceil(document.documentElement.getBoundingClientRect().height);if(h!==last){last=h;parent.postMessage({type:'${WIDGET_HEIGHT_MESSAGE}',height:h},'*')}}window.addEventListener('load',send);window.addEventListener('resize',send);if(window.ResizeObserver){new ResizeObserver(send).observe(document.documentElement)}setTimeout(send,50);setInterval(send,500)})();`;

/** A widget document plus the script that reports its content height to the host page. */
export function widgetDocument(code: string, theme: PreviewTheme): string {
  const doc = htmlDocument(code, theme);
  const script = `<script>${escapeScript(REPORTER)}</script>`;
  const at = doc.toLowerCase().lastIndexOf("</body>");
  return at >= 0 ? doc.slice(0, at) + script + doc.slice(at) : doc + script;
}

export function clampHeight(v: unknown): number | null {
  if (typeof v !== "number" || !Number.isFinite(v)) return null;
  return Math.min(WIDGET_MAX_HEIGHT, Math.max(WIDGET_MIN_HEIGHT, Math.ceil(v)));
}
