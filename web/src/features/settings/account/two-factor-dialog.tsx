"use client";

import { RefreshCw } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { toast } from "@/components/ui/toast-store";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { useI18n, useT } from "@/lib/i18n/provider";
import { CopyButton } from "../parts";
import { sanitizeCode } from "../lib/policy";
import { mfaDisable, mfaRegenerate, mfaSetupCancel, mfaSetupConfirm, type TotpSetup } from "./api";
import { QrCode } from "./qr-code";

export type TwoFactorMode = "setup" | "manage";

const MIN_CODE = 6;

/** Whole seconds until an ISO time; negative once passed. */
export function secondsUntil(iso: string, now: number): number {
  const t = Date.parse(iso);
  return Number.isNaN(t) ? 0 : Math.ceil((t - now) / 1000);
}
