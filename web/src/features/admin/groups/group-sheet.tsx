"use client";

import { Plus, X } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, FieldError, FieldHint, FieldLabel } from "@/components/ui/field";
import { Input, Textarea } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { toast } from "@/components/ui/toast-store";
import { api } from "@/lib/api/client";
import { isAbortError } from "@/lib/api/errors";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { IdPicker } from "../components/id-picker";
import { ModelIcon } from "../components/model-icon";
import { LoadError } from "../components/load-error";
import { Sheet } from "../components/sheet";
import { OptionSelect } from "../components/toolbar";
import { listAllUpstreams, listModels, listVendors, createGroup, getGroupModels, getGroupUsers, patchGroup, putGroupModels, putGroupUsers, type GroupRule, type Upstream, type Vendor } from "../llm-api";
import { loadAllPages, readPage } from "../lib/paging";
import { protocolLabel, PROTOCOLS } from "../lib/protocols";
import { invalidateModels } from "../lib/reference";
import { useSnapshot } from "../lib/use-snapshot";
import type { PermissionGroup, PlatformModel, UserRecord } from "../lib/wire";
import { listProviders } from "../login/api";
import type { IdentityProvider } from "../login/rules";
import { SUBSCRIPTION_STATUSES } from "../users/rules";
import { addRule, canAddRule, factorFromPercent, percentFromFactor, refreshAfter, removeRule, RULE_TYPES, ruleKey } from "./rules";

export interface GroupSheetProps {
  target: PermissionGroup | "new" | null;
  onOpenChange: (open: boolean) => void;
  /** Called after a save attempt with what must be refreshed. */
  onChanged: (reload: boolean) => void;
}

interface ModelFilters {
  upstream: string;
  vendor: string;
  protocol: string;
}
interface UserFilters {
  subscriptionStatus: string;
  provider: string;
}
