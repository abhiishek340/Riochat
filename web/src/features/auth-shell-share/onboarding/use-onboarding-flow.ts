"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { toast } from "@/components/ui/toast-store";
import { fetchMe } from "@/lib/api/endpoints/auth";
import { isApiError } from "@/lib/api/errors";
import { endSession } from "@/lib/api/session-store";
import type { AuthUser } from "@/lib/api/types";
import { normalizeLocale, type Locale } from "@/lib/i18n/config";
import { useI18n } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { publishUserUpdate } from "@/lib/session/provider";
import { useTheme } from "@/lib/theme/provider";
import type { Accent } from "@/lib/theme/config";
import { CODE_LENGTH, digitsOnly, isValidPassword, normalizeUsername } from "../lib/password-policy";
import { cancelTwoFactorSetup, completeOnboarding, confirmTwoFactorSetup, patchProfile, patchUsername, startTwoFactorSetup, type TwoFactorSetup } from "./api";
import {
  appearanceWithPreset,
  detectTimezone,
  initialTimezone,
  isBootstrap,
  LAST_STEP,
  personalizePatch,
  planAccountSave,
  previousStep,
  storedPreset,
  validateAccount,
  type AccountProblem,
} from "./model";

export type SetupState = "unavailable" | "enabled" | "starting" | "ready" | "failed";

const PROBLEM_KEY: Record<AccountProblem, "problemUsername" | "problemShape" | "problemName" | "problemPassword"> = {
  changeUsername: "problemUsername",
  usernameShape: "problemShape",
  displayName: "problemName",
  password: "problemPassword",
};
