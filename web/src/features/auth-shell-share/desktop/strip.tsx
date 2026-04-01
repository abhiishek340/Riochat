"use client";

import { Copy, Globe, Laptop, Minus, Plus, Square, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState, type PointerEvent as ReactPointerEvent, type ReactNode } from "react";
import { cn } from "@/lib/utils/cn";
import { invoke, listen } from "./bridge";
import {
  arrangeTabs,
  DRAG_THRESHOLD_PX,
  dropIndex,
  isClosable,
  readSnapshot,
  reorderIds,
  stripStrings,
  tabIcon,
  tabLabel,
  tabTooltip,
  type StripSnapshot,
  type StripTab,
} from "./strip-model";

interface Drag {
  id: string;
  startX: number;
  active: boolean;
}

const EMPTY: StripSnapshot = { tabs: [], active: null, platform: "linux" };

function report(error: unknown): void {
  console.warn("tab strip:", error);
}
