"use client";

import { Check, Sparkles } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { ICON_GRID_LIMIT, iconFor, searchIcons } from "./icons";

/** Built-in icon chooser. An empty value means "automatic" (follows the endpoint). */
export function IconPicker({
  value,
  onChange,
  automaticSlug,
  disabled,
}: {
  value: string;
  onChange: (slug: string) => void;
  automaticSlug?: string;
  disabled?: boolean;
}) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const Current = iconFor(value, automaticSlug);
  const matches = searchIcons(query);
  const shown = matches.slice(0, ICON_GRID_LIMIT);

  const close = () => {
    setOpen(false);
    setQuery("");
  };

  return (
    <div className="grid gap-2">
      <div className="flex items-center gap-2">
        <Button
          type="button"
          variant="outline"
          size="icon"
          disabled={disabled}
          aria-expanded={open}
          aria-label={t("settings.models.icon.choose")}
          onClick={() => (open ? close() : setOpen(true))}
        >
          <Current />
        </Button>
        {value === "" ? (
          <span className="inline-flex items-center gap-1 text-[13px] text-muted-foreground">
            <Sparkles className="size-3.5" />
            {t("settings.models.icon.automatic")}
          </span>
        ) : null}
      </div>
      {open ? (
        <div className="grid gap-2 rounded-lg border bg-popover p-2.5">
          <Input
            value={query}
            placeholder={t("settings.models.icon.search")}
            aria-label={t("settings.models.icon.search")}
            autoComplete="off"
            onChange={(e) => setQuery(e.target.value)}
          />
          <div className="grid max-h-44 grid-cols-6 gap-1 overflow-y-auto sm:grid-cols-8">
            {query.trim() === "" ? (
              <button
                type="button"
                title={t("settings.models.icon.automatic")}
                aria-label={t("settings.models.icon.automatic")}
                aria-pressed={value === ""}
                onClick={() => {
                  onChange("");
                  close();
                }}
                className={cn("flex size-9 items-center justify-center rounded-md border border-dashed text-muted-foreground outline-none hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring", value === "" && "border-primary text-primary")}
              >
                <Sparkles className="size-4" />
              </button>
            ) : null}
            {shown.map(({ id, name, Icon }) => (
              <button
                key={id}
                type="button"
                title={name}
                aria-label={name}
                aria-pressed={value === id}
                onClick={() => {
                  onChange(id);
                  close();
                }}
                className={cn("relative flex size-9 items-center justify-center rounded-md border outline-none hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring", value === id && "border-primary bg-accent text-primary")}
              >
                <Icon className="size-4" />
                {value === id ? <Check className="absolute -right-1 -top-1 size-3 rounded-full bg-primary p-px text-primary-foreground" /> : null}
              </button>
            ))}
          </div>
          {matches.length > shown.length ? <p className="text-xs text-muted-foreground">{t("settings.models.icon.refine")}</p> : null}
          {matches.length === 0 ? <p className="text-xs text-muted-foreground">{t("settings.models.icon.none")}</p> : null}
        </div>
      ) : null}
    </div>
  );
}
