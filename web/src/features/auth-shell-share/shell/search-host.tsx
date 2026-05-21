"use client";

import { useEffect, useState } from "react";
import { usePathname, useRouter } from "next/navigation";
import { Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Tooltip } from "@/components/ui/tooltip";
import { useSession } from "@/lib/session/provider";
import { useI18n } from "@/lib/i18n/provider";
import { isApplePlatform, matchShortcut, modifierLabel, NEW_CHAT_EVENT, OPEN_SEARCH_EVENT, requestOpenSearch } from "../lib/shortcuts";
import { ConversationSearchDialog } from "./search-dialog";
import { clearPreviewCache } from "./search-preview";
import { resetSearchCaches } from "./use-conversation-search";

/** Mounts the search dialog and the shell's global shortcuts (search, new chat). */
export function SearchHost() {
  const router = useRouter();
  const pathname = usePathname();
  const [open, setOpen] = useState(false);
  const { accessToken } = useSession();

  // Lists cached for one account must never be shown to the next one.
  useEffect(() => {
    if (accessToken === "") {
      resetSearchCaches();
      clearPreviewCache();
    }
  }, [accessToken]);

  useEffect(() => {
    const apple = isApplePlatform(navigator.platform);
    const onKey = (event: KeyboardEvent) => {
      const action = matchShortcut(event, apple);
      if (!action) return;
      event.preventDefault();
      if (action === "search") setOpen(true);
      else {
        // Chat listens for this to reset its view; the route change covers the rest.
        window.dispatchEvent(new Event(NEW_CHAT_EVENT));
        if (pathname !== "/chat" || window.location.search) router.push("/chat");
      }
    };
    const onOpen = () => setOpen(true);
    window.addEventListener("keydown", onKey);
    window.addEventListener(OPEN_SEARCH_EVENT, onOpen);
    return () => {
      window.removeEventListener("keydown", onKey);
      window.removeEventListener(OPEN_SEARCH_EVENT, onOpen);
    };
  }, [pathname, router]);

  return <ConversationSearchDialog open={open} onOpenChange={setOpen} />;
}

/** Header button that opens the search dialog; shows the platform's shortcut in its tooltip. */
export function SearchTrigger() {
  const { t } = useI18n();
  const [hint, setHint] = useState("Ctrl K");
  useEffect(() => setHint(`${modifierLabel(isApplePlatform(navigator.platform))} K`), []);
  return (
    <Tooltip label={`${t("shellX.search.title")} (${hint})`}>
      <Button variant="ghost" size="icon-sm" aria-label={t("shellX.search.title")} onClick={requestOpenSearch}>
        <Search />
      </Button>
    </Tooltip>
  );
}
