"use client";

import { TriangleAlert } from "lucide-react";
import { useCallback, useMemo } from "react";
import { Button } from "@/components/ui/button";
import { toast } from "@/components/ui/toast-store";
import { useT } from "@/lib/i18n/provider";
import { AdminPage } from "../components/page";
import { AdminSection } from "../components/section-guard";
import { SettingsGroup } from "../components/settings-group";
import { problemText } from "../lib/problem";
import { fieldId, type Draft, type FieldDef } from "../lib/settings-form";
import { useNotify } from "../lib/use-notify";
import { useSettingsForm } from "../lib/use-settings-form";
import type { RuntimeName } from "./api";
import { ALL_FILE_FIELDS, EXTRACTION_FIELDS, FULLTEXT_FIELDS, MINERU_CLOUD_URL, MINERU_SELF_HOSTED_URL, RAG_FIELDS, SEMANTIC_FIELDS, UPLOAD_FIELDS, VECTOR_FIELDS } from "./fields";
import { IndexPanel } from "./index-panel";
import { RuntimeProbe } from "./runtime-probe";
import {
  changesVectorSpace,
  companionKeys,
  embeddingReady,
  F,
  mergeMimes,
  missingMineruMimes,
  normalizeRetrieval,
  ocrActive,
  ocrEngineOf,
  retrievalChangeProblem,
  validateEmbedding,
  validateExtraction,
} from "./rules";

const BY_ID = new Map(ALL_FILE_FIELDS.map((f) => [fieldId(f), f]));

/** Cross-field rules and defaults; also run on load and before every save. */
export function normalizeFiles(next: Draft, changedId: string | null): Draft {
  let out = normalizeRetrieval(next);
  if (changedId === F.mineruSource) {
    const target = out[F.mineruSource] === "self_hosted" ? MINERU_SELF_HOSTED_URL : MINERU_CLOUD_URL;
    const other = target === MINERU_CLOUD_URL ? MINERU_SELF_HOSTED_URL : MINERU_CLOUD_URL;
    const current = (out[F.mineruUrl] ?? "").trim();
    if (current === "" || current === other) out = { ...out, [F.mineruUrl]: target };
  }
  if (changedId === F.ocrEngine && out[F.ocrEngine] === "mistral") {
    // Choosing Mistral fills what it needs when those fields are empty.
    for (const id of ["extract.mistral_ocr_base_url", "extract.mistral_ocr_model", "extract.mistral_ocr_timeout_seconds"]) {
      const def = BY_ID.get(id)?.def;
      if (def && (out[id] ?? "").trim() === "") out = { ...out, [id]: def };
    }
  }
  if (changedId === null) {
    // Blank values that have a default are written with the default.
    for (const field of ALL_FILE_FIELDS) {
      const id = fieldId(field);
      if (field.blankDefault && field.def !== undefined && field.type !== "password" && (out[id] ?? "").trim() === "") out = { ...out, [id]: field.def };
    }
  }
  return out;
}

export function AdminFilesPage() {
  return (
    <AdminSection>
      <FilesContent />
    </AdminSection>
  );
}

const SERVICE_PREFIX: Record<Exclude<RuntimeName, "embedding">, string> = { tika: "tika_", docling: "docling_", mineru: "mineru_", tesseract: "tesseract_", rapidocr: "rapidocr_" };

function FilesContent() {
  const t = useT();
  const notify = useNotify();
  const form = useSettingsForm({ fields: ALL_FILE_FIELDS, normalize: normalizeFiles });
  const draft = form.draft;

  const group = (key: "upload" | "extraction" | "fullText" | "vector" | "semantic" | "rag") => ({ title: t(`adminFiles.groups.${key}.title`), description: t(`adminFiles.groups.${key}.description`) });

  const guarded = useCallback(
    (id: string, value: string) => {
      const found = retrievalChangeProblem(draft, id, value);
      if (found) {
        toast.error(t("adminCommon.settings.saveFailed"), { description: problemText(t, found) });
        return;
      }
      form.set(id, value);
    },
    [draft, form, t],
  );

  // --- extraction
  const engine = draft[F.engine] ?? "builtin";
  const services = useMemo(() => {
    const list: Exclude<RuntimeName, "embedding">[] = [];
    if (engine === "tika" || engine === "docling" || engine === "mineru") list.push(engine);
    if (ocrActive(draft)) {
      const ocr = ocrEngineOf(draft);
      if (ocr === "tesseract" || ocr === "rapidocr") list.push(ocr);
    }
    return list;
  }, [engine, draft]);
  const forceExtract = companionKeys(draft)
    .map((id) => BY_ID.get(id))
    .filter((f): f is FieldDef => f !== undefined);
  const missing = engine === "mineru" ? missingMineruMimes(draft[F.mime] ?? "", draft[F.mineruSource] ?? "cloud", draft[F.mineruTypes] ?? "") : [];

  // --- embedding
  const ready = embeddingReady(draft);
  const forceRetrieval = ready ? [] : SEMANTIC_FIELDS.concat(RAG_FIELDS.slice(0, 1));

  return (
    <AdminPage title={t("adminFiles.title")} description={t("adminFiles.description")}>
      <SettingsGroup form={form} fields={UPLOAD_FIELDS} {...group("upload")} />

      <SettingsGroup
        form={form}
        fields={EXTRACTION_FIELDS}
        {...group("extraction")}
        save={{ force: forceExtract, validate: (d, configured) => validateExtraction(d, configured) }}
      >
        {missing.length > 0 ? (
          <div role="alert" className="mt-4 flex flex-wrap items-start gap-3 rounded-lg border border-warning/50 bg-warning/10 p-3 text-sm">
            <TriangleAlert aria-hidden className="mt-0.5 size-4 shrink-0 text-warning" />
            <div className="min-w-0 flex-1 space-y-1">
              <div className="font-medium">{t("adminFiles.mime.title")}</div>
              <p className="text-muted-foreground">{t("adminFiles.mime.body")}</p>
              <ul className="font-mono text-[12px]">
                {missing.map((m) => (
                  <li key={m} className="break-all">
                    {m}
                  </li>
                ))}
              </ul>
            </div>
            <Button size="sm" loading={form.saving} onClick={() => void form.saveValues({ [F.mime]: mergeMimes(draft[F.mime] ?? "", missing) })}>
              {t("adminFiles.mime.fix")}
            </Button>
          </div>
        ) : null}
        {services.map((name) => (
          <RuntimeProbe key={name} name={name} resetKey={form.saved} dirty={form.dirtyOf(EXTRACTION_FIELDS.filter((f) => f.key.startsWith(SERVICE_PREFIX[name]))).length > 0} />
        ))}
      </SettingsGroup>

      <SettingsGroup form={form} fields={FULLTEXT_FIELDS} {...group("fullText")} />

      <SettingsGroup
        form={form}
        fields={VECTOR_FIELDS}
        {...group("vector")}
        save={{
          silent: true,
          force: forceRetrieval,
          validate: (d) => validateEmbedding(d),
        }}
        onSaved={(sent) => {
          if (changesVectorSpace(sent.map(fieldId))) toast.warning(t("adminFiles.toast.staleWarning"), { description: t("adminFiles.toast.staleWarningBody"), duration: 8000 });
          else notify.success("adminCommon.settings.saved");
        }}
      >
        {draft[F.embeddingOn] === "true" ? (
          <>
            <RuntimeProbe name="embedding" resetKey={form.saved} dirty={form.dirtyOf(VECTOR_FIELDS).length > 0} />
            <IndexPanel resetKey={form.saved} />
          </>
        ) : null}
      </SettingsGroup>

      <SettingsGroup form={form} fields={SEMANTIC_FIELDS} {...group("semantic")} onChange={guarded} />
      <SettingsGroup form={form} fields={RAG_FIELDS} {...group("rag")} onChange={guarded} />
    </AdminPage>
  );
}
