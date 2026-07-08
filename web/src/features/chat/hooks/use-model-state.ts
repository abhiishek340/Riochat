"use client";

/**
 * Which model a conversation uses and the parameters chosen for it: the model
 * (with the defaulting rules for new and existing chats), its composer
 * options and the administrator-declared control choices, both remembered per
 * model in browser storage.
 */
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { toast } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { defaultModelCandidate, listRuns } from "../api";
import { normalizeControls, pruneChoices, requestControls, type ControlChoices, type ControlValue } from "../lib/controls";
import { modelDefaultOptions, modelFacts, parseModelCaps, type ModelCaps } from "../lib/model-caps";
import { cloneJson, payloadSignature, sameJson, stripReserved, type Options } from "../lib/options";
import { browserIO, controlsStorageKey, optionsStorageKey, readJsonRecord, writeJsonRecord } from "../lib/persist";
import type { ControlDTO, ConversationDTO, ModelDTO, ModelOptionPolicyDTO, ProjectDTO } from "../types";
import { DRAFT_KEY, TEMP_KEY } from "../state/engine-types";
import { modelsResource } from "../state/resources";
import type { ModelFacts } from "../lib/task-decision";

export interface ModelState {
  model: ModelDTO | null;
  caps: ModelCaps | null;
  facts: ModelFacts;
  /** Models are still being resolved. */
  resolving: boolean;
  manual: boolean;
  select: (name: string) => void;
  options: Options;
  setOptions: (next: Options | ((prev: Options) => Options)) => void;
  resetOptions: () => void;
  /** Re-fetches the catalog and returns the fresh model defaults. */
  restoreBackendDefaults: () => Promise<void>;
  defaults: Options;
  controls: ControlDTO[];
  choices: ControlChoices;
  setChoice: (id: string, value: ControlValue | null) => void;
  resetChoices: () => void;
  requestControls: ControlChoices | undefined;
}

let candidateCache: string | null | undefined;

export function useModelState(args: {
  key: string;
  conversation: ConversationDTO | null;
  project: ProjectDTO | null;
  models: ModelDTO[];
  modelsLoaded: boolean;
  policy: ModelOptionPolicyDTO | null;
  userDefault: string;
  reuseOptions: boolean;
  authed: boolean;
  /** Grows with every "new chat" request; a manual choice does not outlive it. */
  newChatRevision?: number;
}): ModelState {
  const { key, conversation, project, models, modelsLoaded, policy, userDefault, reuseOptions, authed, newChatRevision = 0 } = args;
  const t = useT();
  const [sel, setSel] = useState<{ key: string; name: string; manual: boolean } | null>(null);
  const [latestRunModel, setLatestRunModel] = useState<{ key: string; name: string } | null>(null);
  const [candidate, setCandidate] = useState<string | null>(candidateCache ?? null);
  const warned = useRef(new Set<string>());

  const isNew = key === DRAFT_KEY || key === TEMP_KEY;
  const projectId = project?.publicID ?? "";
  const contextKey = isNew ? `${key}:${projectId}:${newChatRevision}` : key;

  // The server's suggestion for a new chat (once).
  useEffect(() => {
    if (!authed || !isNew || candidateCache !== undefined) return;
    let live = true;
    defaultModelCandidate().then(
      (res) => {
        candidateCache = res.platformModelName || null;
        if (live) setCandidate(candidateCache);
      },
      () => {
        candidateCache = null;
      },
    );
    return () => {
      live = false;
    };
  }, [authed, isNew]);

  // The newest run's model for an existing conversation.
  useEffect(() => {
    if (isNew || !authed) return;
    let live = true;
    listRuns(key).then(
      (res) => {
        const name = res.results[0]?.platformModelName ?? "";
        if (live && name) setLatestRunModel({ key, name });
      },
      () => undefined,
    );
    return () => {
      live = false;
    };
  }, [key, isNew, authed]);

  const byName = useMemo(() => new Map(models.map((m) => [m.platformModelName, m] as const)), [models]);

  // Warn once per new-chat context when the project's default model is gone.
  useEffect(() => {
    if (!isNew || !modelsLoaded || models.length === 0 || !project?.defaultModel) return;
    if (byName.has(project.defaultModel) || warned.current.has(contextKey)) return;
    warned.current.add(contextKey);
    toast.warning(t("chatModel.projectDefaultMissing", { model: project.defaultModel }));
  }, [isNew, modelsLoaded, models.length, project, byName, contextKey, t]);

  const resolvedName = useMemo(() => {
    const manual = sel && sel.key === contextKey && sel.manual && byName.has(sel.name) ? sel.name : "";
    if (manual) return manual;
    if (isNew) {
      for (const name of [project?.defaultModel ?? "", userDefault, candidate ?? ""]) if (name && byName.has(name)) return name;
      return models[0]?.platformModelName ?? "";
    }
    const fromRun = latestRunModel && latestRunModel.key === key ? latestRunModel.name : "";
    for (const name of [fromRun, conversation?.model ?? ""]) if (name && byName.has(name)) return name;
    return models[0]?.platformModelName ?? "";
  }, [sel, contextKey, byName, isNew, project, userDefault, candidate, models, latestRunModel, key, conversation?.model]);

  const model = resolvedName ? (byName.get(resolvedName) ?? null) : null;
  const caps = useMemo(() => (model ? parseModelCaps(model, policy) : null), [model, policy]);
  const facts = useMemo(() => modelFacts(model, caps), [model, caps]);
  const manual = !!(sel && sel.key === contextKey && sel.manual);

  const select = useCallback((name: string) => setSel({ key: contextKey, name, manual: true }), [contextKey]);

  // --- options -------------------------------------------------------------------------
  const defaults = useMemo(() => (caps ? modelDefaultOptions(caps) : {}), [caps]);
  const defaultsSig = payloadSignature(defaults);
  const [optionsState, setOptionsState] = useState<{ model: string; options: Options; defaultsSig: string } | null>(null);

  const initialOptions = useCallback(
    (name: string): Options => {
      if (reuseOptions) {
        const cached = readJsonRecord(browserIO, optionsStorageKey(name));
        if (cached) return stripReserved(cached);
      }
      return cloneJson(defaults);
    },
    [defaults, reuseOptions],
  );

  useEffect(() => {
    if (!model) return;
    setOptionsState((cur) => {
      if (!cur || cur.model !== model.platformModelName) return { model: model.platformModelName, options: initialOptions(model.platformModelName), defaultsSig };
      if (cur.defaultsSig !== defaultsSig) {
        // The model's defaults changed under the user: follow them unless options were edited.
        const edited = payloadSignature(cur.options) !== cur.defaultsSig && !sameJson(cur.options, defaults);
        if (!edited) {
          writeJsonRecord(browserIO, optionsStorageKey(model.platformModelName), null);
          return { model: model.platformModelName, options: cloneJson(defaults), defaultsSig };
        }
        return { ...cur, defaultsSig };
      }
      return cur;
    });
  }, [model, defaultsSig, defaults, initialOptions]);

  const options = optionsState && model && optionsState.model === model.platformModelName ? optionsState.options : model ? initialOptions(model.platformModelName) : {};

  const setOptions = useCallback(
    (next: Options | ((prev: Options) => Options)) => {
      if (!model) return;
      setOptionsState((cur) => {
        const prev = cur && cur.model === model.platformModelName ? cur.options : initialOptions(model.platformModelName);
        const value = typeof next === "function" ? next(prev) : next;
        const clean = stripReserved(value);
        writeJsonRecord(browserIO, optionsStorageKey(model.platformModelName), sameJson(clean, defaults) ? null : clean);
        return { model: model.platformModelName, options: clean, defaultsSig };
      });
    },
    [model, initialOptions, defaults, defaultsSig],
  );

  const resetOptions = useCallback(() => {
    if (!model) return;
    writeJsonRecord(browserIO, optionsStorageKey(model.platformModelName), null);
    setOptionsState({ model: model.platformModelName, options: cloneJson(defaults), defaultsSig });
  }, [model, defaults, defaultsSig]);

  const restoreBackendDefaults = useCallback(async () => {
    await modelsResource.refresh();
    resetOptions();
  }, [resetOptions]);

  // --- control choices -------------------------------------------------------------------
  const controls = useMemo(() => normalizeControls(model?.controls), [model]);
  const [choicesState, setChoicesState] = useState<{ model: string; choices: ControlChoices } | null>(null);
  const readChoices = useCallback(
    (name: string): ControlChoices => pruneChoices(controls, readJsonRecord(browserIO, controlsStorageKey(name))),
    [controls],
  );
  useEffect(() => {
    if (!model) return;
    setChoicesState((cur) => (cur && cur.model === model.platformModelName ? { ...cur, choices: pruneChoices(controls, cur.choices) } : { model: model.platformModelName, choices: readChoices(model.platformModelName) }));
  }, [model, controls, readChoices]);
  const choices = useMemo(
    () => (choicesState && model && choicesState.model === model.platformModelName ? choicesState.choices : model ? readChoices(model.platformModelName) : {}),
    [choicesState, model, readChoices],
  );

  const setChoice = useCallback(
    (id: string, value: ControlValue | null) => {
      if (!model) return;
      setChoicesState((cur) => {
        const prev = cur && cur.model === model.platformModelName ? cur.choices : readChoices(model.platformModelName);
        const next: ControlChoices = { ...prev };
        if (value === null) delete next[id];
        else next[id] = value;
        const pruned = pruneChoices(controls, next);
        writeJsonRecord(browserIO, controlsStorageKey(model.platformModelName), pruned);
        return { model: model.platformModelName, choices: pruned };
      });
    },
    [model, readChoices, controls],
  );

  const resetChoices = useCallback(() => {
    if (!model) return;
    writeJsonRecord(browserIO, controlsStorageKey(model.platformModelName), null);
    setChoicesState({ model: model.platformModelName, choices: {} });
  }, [model]);

  const reqControls = useMemo(() => requestControls(controls, choices), [controls, choices]);

  return {
    model,
    caps,
    facts,
    resolving: !modelsLoaded,
    manual,
    select,
    options,
    setOptions,
    resetOptions,
    restoreBackendDefaults,
    defaults,
    controls,
    choices,
    setChoice,
    resetChoices,
    requestControls: reqControls,
  };
}
