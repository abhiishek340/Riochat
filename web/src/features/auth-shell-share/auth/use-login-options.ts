"use client";

import { useEffect, useState } from "react";
import { fetchLoginOptions } from "@/lib/api/endpoints/auth";
import { fetchLoginPage } from "@/lib/api/endpoints/public";
import type { LoginOptions } from "@/lib/api/types";
import { safeNextPath } from "@/lib/utils/navigation";
import { DEFAULT_LOGIN_OPTIONS } from "../lib/login-options";

export interface LoginSetup {
  options: LoginOptions;
  /** Server-configured landing page for when `next` is missing or unsafe. */
  defaultNext: string;
  /** True once both requests settled (success or failure). */
  loaded: boolean;
}

/** Loads both documents in parallel; failures silently keep the defaults. */
export function useLoginOptions(): LoginSetup {
  const [state, setState] = useState<LoginSetup>({ options: DEFAULT_LOGIN_OPTIONS, defaultNext: "/chat", loaded: false });
  useEffect(() => {
    let live = true;
    void Promise.allSettled([fetchLoginOptions(), fetchLoginPage()]).then(([options, page]) => {
      if (!live) return;
      setState({
        options: options.status === "fulfilled" ? options.value : DEFAULT_LOGIN_OPTIONS,
        defaultNext: page.status === "fulfilled" ? safeNextPath(page.value.defaultNextPath, "/chat") : "/chat",
        loaded: true,
      });
    });
    return () => {
      live = false;
    };
  }, []);
  return state;
}
