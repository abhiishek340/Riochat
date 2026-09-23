"use client";

import { useT } from "@/lib/i18n/provider";
import { GenerationCard } from "./message/assistant-message";

/** Internal preview of the image-generation loading card at its three shapes. */
export function ImageLoadingPreview() {
  const t = useT();
  return (
    <div className="mx-auto grid max-w-4xl gap-6 p-6 sm:grid-cols-3">
      {(["wide", "square", "portrait"] as const).map((shape) => (
        <GenerationCard key={shape} shape={shape} video={false} text={t("chatMsg.media.runningImage")} />
      ))}
    </div>
  );
}
