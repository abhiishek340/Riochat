"use client";

import { useEffect, useMemo, useRef } from "react";

/**
 * The bridge a sandboxed component sees. It is injected ahead of the
 * component's own markup: `riochat.props` holds the current props,
 * `riochat.onProps(fn)` subscribes to later changes, and `riochat.resize()`
 * is called by the host when the size changes. The frame has no network and no
 * same-origin access.
 */
export function previewDocument(source: string): string {
  const bridge = `<script>(function(){var l=[];var r={props:{},onProps:function(f){l.push(f);try{f(r.props)}catch(e){}}};window.riochat=r;window.addEventListener("message",function(e){var d=e.data;if(!d||d.type!=="riochat:props")return;r.props=d.props||{};l.forEach(function(f){try{f(r.props)}catch(x){}})});parent.postMessage({type:"riochat:ready"},"*")})();</script>`;
  const csp = `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data: blob:; style-src 'unsafe-inline'; script-src 'unsafe-inline'; font-src data:">`;
  return `<!doctype html><html><head><meta charset="utf-8">${csp}<style>html,body{margin:0;padding:12px;font:14px system-ui,sans-serif;color:#222;background:transparent}</style>${bridge}</head><body>${source}</body></html>`;
}

/** Renders a component's HTML in a sandboxed frame and feeds it the sample props. */
export function ComponentPreview({ source, props, title }: { source: string; props: Record<string, unknown>; title: string }) {
  const frame = useRef<HTMLIFrameElement>(null);
  const doc = useMemo(() => previewDocument(source), [source]);
  const latest = useRef(props);
  latest.current = props;

  useEffect(() => {
    const send = () => frame.current?.contentWindow?.postMessage({ type: "riochat:props", props: latest.current }, "*");
    const onMessage = (event: MessageEvent) => {
      if (event.source === frame.current?.contentWindow && (event.data as { type?: unknown })?.type === "riochat:ready") send();
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, []);

  useEffect(() => {
    frame.current?.contentWindow?.postMessage({ type: "riochat:props", props }, "*");
  }, [props]);

  return <iframe ref={frame} title={title} sandbox="allow-scripts" srcDoc={doc} referrerPolicy="no-referrer" className="h-64 w-full rounded-lg border bg-white" />;
}
