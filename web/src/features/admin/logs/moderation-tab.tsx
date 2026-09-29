"use client";

import { useEffect, useMemo, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { isAbortError } from "@/lib/api/errors";
import { useI18n, useT } from "@/lib/i18n/provider";
import type { Column } from "../components/data-table";
import { DetailList, DetailSection } from "../components/detail-list";
import { Mono } from "../components/page";
import { StatusBadge } from "../components/status";
import { OptionSelect, SearchBox } from "../components/toolbar";
import { formatDateTime, formatLatency } from "../lib/format";
import { useLabel } from "../lib/use-label";
import { useNotify } from "../lib/use-notify";
import { usePagedQuery } from "../lib/use-paged-query";
import { fetchEventImage, getEvent, listEvents, type EventDetail, type ModerationEvent } from "../moderation/api";
import { userText } from "./filters";
import { LogView } from "./log-view";
import type { TabProps } from "./tabs";

export function ModerationTab({ remountKey }: TabProps) {
  const t = useT();
  const { locale } = useI18n();
  const label = useLabel();
  const notify = useNotify();
  const [q, setQ] = useState("");
  const [result, setResult] = useState("");
  const [direction, setDirection] = useState("");
  const filters = useMemo(() => ({ q, result, direction, remountKey }), [q, result, direction, remountKey]);
  const list = usePagedQuery({
    filters,
    fetch: async ({ page, pageSize, filters: f, signal }) => {
      const out = await listEvents({ page, pageSize, query: f.q, result: f.result, direction: f.direction }, signal);
      return { total: out.total, results: out.items };
    },
    onError: (error) => notify.error("adminLogs.loadFailed.moderation", error),
  });
  const columns: Column<ModerationEvent>[] = [
    { id: "time", header: t("adminLogs.moderation.columns.time"), cell: (r) => <span className="whitespace-nowrap text-muted-foreground">{formatDateTime(r.createdAt, locale)}</span> },
    { id: "user", header: t("adminLogs.moderation.columns.user"), cell: (r) => userText(r) },
    { id: "direction", header: t("adminLogs.moderation.columns.direction"), cell: (r) => label("adminLogs.moderation.directions", r.direction) },
    { id: "kind", header: t("adminLogs.moderation.columns.kind"), cell: (r) => label("adminLogs.moderation.modalities", r.modality) },
    { id: "result", header: t("adminLogs.moderation.columns.result"), cell: (r) => <StatusBadge value={r.result} prefix="adminLogs.moderation.results" /> },
    {
      id: "categories",
      header: t("adminLogs.moderation.columns.categories"),
      cell: (r) => (
        <div className="flex max-w-56 flex-wrap gap-1">
          {r.categories.slice(0, 3).map((c) => (
            <Badge key={c} tone="outline">
              {c}
            </Badge>
          ))}
          {r.categories.length > 3 ? <Badge tone="outline">+{r.categories.length - 3}</Badge> : null}
        </div>
      ),
    },
    { id: "latency", header: t("adminLogs.moderation.columns.latency"), className: "whitespace-nowrap tabular-nums", cell: (r) => formatLatency(r.latencyMS, locale) },
  ];
  return (
    <LogView
      list={list}
      columns={columns}
      rowKey={(r) => r.publicID}
      label={t("adminLogs.tabs.moderation")}
      empty={t("adminLogs.empty")}
      detailTitle={(r) => r.publicID}
      filters={
        <>
          <SearchBox value={q} onChange={setQ} placeholder={t("adminLogs.moderation.search")} />
          <OptionSelect value={result} onChange={setResult} label={t("adminLogs.moderation.columns.result")} options={[{ value: "", label: t("adminLogs.moderation.anyResult") }, ...["hit", "passed", "failed_open"].map((v) => ({ value: v, label: label("adminLogs.moderation.results", v) }))]} />
          <OptionSelect value={direction} onChange={setDirection} label={t("adminLogs.moderation.columns.direction")} options={[{ value: "", label: t("adminLogs.moderation.anyDirection") }, ...["input", "output"].map((v) => ({ value: v, label: label("adminLogs.moderation.directions", v) }))]} />
        </>
      }
      detail={(r) => <ModerationDetail event={r} />}
    />
  );
}

/** Images are fetched with the session and kept as object URLs only while they are on screen. */
function ModerationDetail({ event }: { event: ModerationEvent }) {
  const t = useT();
  const { locale } = useI18n();
  const label = useLabel();
  const [detail, setDetail] = useState<EventDetail | null>(null);
  const [images, setImages] = useState<Array<{ index: number; url: string | null }>>([]);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    let urls: string[] = [];
    setFailed(false);
    setDetail(null);
    setImages([]);
    setLoading(true);
    (async () => {
      try {
        const loaded = await getEvent(event.publicID, controller.signal);
        setDetail(loaded);
        setLoading(false);
        const got: Array<{ index: number; url: string | null }> = [];
        for (const image of loaded.images ?? []) {
          try {
            const url = await fetchEventImage(event.publicID, image.index, controller.signal);
            urls.push(url);
            got.push({ index: image.index, url });
          } catch (error) {
            if (isAbortError(error)) return;
            got.push({ index: image.index, url: null });
          }
          setImages([...got]);
        }
      } catch (error) {
        if (isAbortError(error)) return;
        setLoading(false);
        setFailed(true);
      }
    })();
    return () => {
      controller.abort();
      for (const url of urls) URL.revokeObjectURL(url);
      urls = [];
    };
  }, [event.publicID]);

  const e = detail?.event ?? event;
  return (
    <div className="space-y-5">
      {failed ? (
        <p role="alert" className="text-sm font-medium text-destructive">
          {t("adminLogs.detailFailed")}
        </p>
      ) : null}
      <DetailList
        items={[
          [t("adminLogs.moderation.columns.time"), formatDateTime(e.createdAt, locale)],
          [t("adminLogs.moderation.columns.user"), `${userText(e)} (#${e.userID})`],
          [t("adminLogs.moderation.columns.direction"), label("adminLogs.moderation.directions", e.direction)],
          [t("adminLogs.moderation.columns.kind"), label("adminLogs.moderation.modalities", e.modality)],
          [t("adminLogs.moderation.columns.result"), label("adminLogs.moderation.results", e.result)],
          [t("adminLogs.moderation.model"), e.model],
          [t("adminLogs.moderation.policy"), e.policyVersion],
          [t("adminLogs.moderation.columns.latency"), formatLatency(e.latencyMS, locale)],
          [t("adminLogs.fields.conversation"), e.conversationID ? `#${e.conversationID}` : ""],
          [t("adminLogs.conversation.run"), e.runID ? <Mono key="r">{e.runID}</Mono> : ""],
          [t("adminLogs.moderation.summary"), e.contentSummary],
          e.errorCode ? [t("adminLogs.moderation.error"), `${e.errorCode} ${e.errorMessage}`.trim()] : null,
        ]}
      />
      {loading ? <Skeleton className="h-24 w-full" /> : null}
      {detail && Object.keys(detail.categoryScores ?? {}).length > 0 ? (
        <DetailSection title={t("adminLogs.moderation.scores")}>
          <ul className="space-y-1 text-sm">
            {Object.entries(detail.categoryScores)
              .sort((a, b) => b[1] - a[1])
              .map(([category, score]) => (
                <li key={category} className="flex items-center gap-3">
                  <span className="w-48 truncate font-mono text-[12.5px]">{category}</span>
                  <span className="h-1.5 flex-1 overflow-hidden rounded-full bg-muted">
                    <span className="block h-full bg-primary" style={{ width: `${Math.min(100, Math.max(0, score * 100))}%` }} />
                  </span>
                  <span className="w-12 text-right tabular-nums text-muted-foreground">{score.toFixed(2)}</span>
                </li>
              ))}
          </ul>
        </DetailSection>
      ) : null}
      {detail?.decryptedText ? (
        <DetailSection title={t("adminLogs.moderation.text")}>
          <pre className="max-h-64 overflow-auto whitespace-pre-wrap rounded-lg border bg-muted/40 p-3 text-[13px]">{detail.decryptedText}</pre>
        </DetailSection>
      ) : detail && detail.textAvailable === false && e.modality === "text" ? (
        <p className="text-sm text-muted-foreground">{t("adminLogs.moderation.textGone")}</p>
      ) : null}
      {images.length > 0 ? (
        <DetailSection title={t("adminLogs.moderation.images")}>
          <div className="grid gap-3 sm:grid-cols-2">
            {images.map((img) =>
              img.url ? (
                // eslint-disable-next-line @next/next/no-img-element
                <img key={img.index} src={img.url} alt={t("adminLogs.moderation.imageAlt", { index: img.index + 1 })} className="max-h-72 w-full rounded-lg border object-contain" />
              ) : (
                <p key={img.index} className="rounded-lg border p-4 text-sm text-muted-foreground">
                  {t("adminLogs.moderation.imageFailed")}
                </p>
              ),
            )}
          </div>
        </DetailSection>
      ) : detail && detail.imagesAvailable === false && e.modality === "image" ? (
        <p className="text-sm text-muted-foreground">{t("adminLogs.moderation.imagesGone")}</p>
      ) : null}
    </div>
  );
}
