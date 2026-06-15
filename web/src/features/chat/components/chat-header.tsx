"use client";

import { Camera, Download, FolderInput, MoreHorizontal, Pencil, Share2, Star, Tags, Trash2, VenetianMask, Wand2 } from "lucide-react";
import { HeaderActions } from "@/components/shell/header-context";
import { Button, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuSub, DropdownMenuSubContent, DropdownMenuSubTrigger, DropdownMenuTrigger, Tooltip } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import type { ConversationDTO } from "../types";

export type HeaderAction = "star" | "rename" | "autoRename" | "labels" | "move" | "share" | "export" | "delete" | "captureLatest" | "captureSelect";

export function ChatHeader({
  conversation,
  temporary,
  canToggleTemporary,
  onToggleTemporary,
  onAction,
  projectName,
}: {
  conversation: ConversationDTO | null;
  temporary: boolean;
  canToggleTemporary: boolean;
  onToggleTemporary: () => void;
  onAction: (a: HeaderAction) => void;
  projectName: string;
}) {
  const t = useT();
  return (
    <HeaderActions>
      {projectName && conversation ? <span className="hidden max-w-40 truncate text-xs text-muted-foreground md:inline">{t("chatNav.header.inProject", { name: projectName })}</span> : null}
      {canToggleTemporary ? (
        <Tooltip label={temporary ? t("chatMisc.temp.exit") : t("chatMisc.temp.start")}>
          <Button variant={temporary ? "secondary" : "ghost"} size="icon-sm" aria-pressed={temporary} aria-label={temporary ? t("chatMisc.temp.exit") : t("chatMisc.temp.start")} onClick={onToggleTemporary}>
            <VenetianMask />
          </Button>
        </Tooltip>
      ) : null}
      {conversation && !temporary ? (
        <>
          <Tooltip label={conversation.isStarred ? t("chatNav.header.unstar") : t("chatNav.header.star")}>
            <Button variant="ghost" size="icon-sm" aria-pressed={conversation.isStarred} aria-label={conversation.isStarred ? t("chatNav.header.unstar") : t("chatNav.header.star")} onClick={() => onAction("star")}>
              <Star className={cn(conversation.isStarred && "fill-current text-warning")} />
            </Button>
          </Tooltip>
          <DropdownMenu>
            <Tooltip label={t("chatNav.header.actions")}>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon-sm" aria-label={t("chatNav.header.actions")}>
                  <MoreHorizontal />
                </Button>
              </DropdownMenuTrigger>
            </Tooltip>
            <DropdownMenuContent align="end">
              <DropdownMenuItem onSelect={() => onAction("rename")}>
                <Pencil /> {t("chatNav.header.rename")}
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => onAction("autoRename")}>
                <Wand2 /> {t("chatNav.header.autoRename")}
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => onAction("labels")}>
                <Tags /> {t("chatNav.header.labels")}
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => onAction("move")}>
                <FolderInput /> {t("chatNav.header.move")}
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem onSelect={() => onAction("share")}>
                <Share2 /> {conversation.shareStatus === "active" && conversation.shareID ? t("chatNav.header.manageShare") : t("chatNav.header.share")}
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => onAction("export")}>
                <Download /> {t("chatNav.header.export")}
              </DropdownMenuItem>
              <DropdownMenuSub>
                <DropdownMenuSubTrigger>
                  <Camera /> {t("chatNav.header.screenshot")}
                </DropdownMenuSubTrigger>
                <DropdownMenuSubContent>
                  <DropdownMenuLabel>{t("chatNav.header.screenshot")}</DropdownMenuLabel>
                  <DropdownMenuItem onSelect={() => onAction("captureLatest")}>{t("chatNav.screenshot.latest")}</DropdownMenuItem>
                  <DropdownMenuItem onSelect={() => onAction("captureSelect")}>{t("chatNav.screenshot.select")}</DropdownMenuItem>
                </DropdownMenuSubContent>
              </DropdownMenuSub>
              <DropdownMenuSeparator />
              <DropdownMenuItem destructive onSelect={() => onAction("delete")}>
                <Trash2 /> {t("chatNav.header.delete")}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </>
      ) : null}
    </HeaderActions>
  );
}
