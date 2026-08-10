"use client";

import { Plus, RefreshCw, Settings2, Wand2 } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Field, FieldHint, FieldLabel } from "@/components/ui/field";
import { Input, NativeSelect, Textarea } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast-store";
import { isAbortError } from "@/lib/api/errors";
import { useCapabilities } from "@/lib/capabilities/provider";
import { useI18n, useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { loadOfficial } from "../billing/api";
import { openRouterWindow } from "./context";
import { MultiSelectMenu, SelectMenu } from "../components/menu-select";
import { Sheet } from "../components/sheet";
import { formatDateTime } from "../lib/format";
import { problemText, type Problem } from "../lib/problem";
import { KINDS, parseKinds, parseStringList } from "../lib/protocols";
import { invalidateModels, loadPermissionGroups } from "../lib/reference";
import { useSnapshot } from "../lib/use-snapshot";
import type { PermissionGroup, PlatformModel } from "../lib/wire";
import { bindSource, createModel, getModelGroups, putModelGroups, resolveCatalog, updateModel, type CatalogResolve, type CatalogStatus, type DisplayGroup, type Vendor } from "../llm-api";
import { loadFallbackWindow, syncCatalogs, touchCatalog } from "./api";
import { CapsEditor, ModalitiesField } from "./caps-editor";
import { useCapsDoc } from "./caps-doc";
import { parseCaps, stringifyCaps } from "./capabilities";
import { finalizeCaps, needsOpenRouter } from "./finalize";
import { IconField } from "./icon-picker";
import { PresetsDialog } from "./presets-dialog";
import { ReasoningField } from "./reasoning-field";
import { blankModelForm, blankSource, createModelBody, formOfModel, hasSelection, sourceBody, sourcesToBind, syncOutcome, updateModelBody, validateModelForm, validateSources, type ModelForm, type SourceDraft } from "./rules";
import { SourcePicker, useUpstreamChoices } from "./source-picker";
import { Labeled } from "./ui";
import { WindowField } from "./window-field";

export interface ModelSheetProps {
  target: PlatformModel | "new" | null;
  onOpenChange: (open: boolean) => void;
  vendors: Vendor[];
  groups: DisplayGroup[];
  onSaved: (model: PlatformModel, created: boolean) => void;
  onManageSources: (model: PlatformModel) => void;
  onManagePresentation: () => void;
}

let draftSeq = 0;
const nextDraft = () => blankSource(`d${++draftSeq}`);
