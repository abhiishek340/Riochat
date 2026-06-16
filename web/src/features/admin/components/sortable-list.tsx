"use client";

import { ArrowDown, ArrowUp, GripVertical } from "lucide-react";
import { useState, type ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { moveItem } from "../lib/reorder";

export interface SortableListProps<T> {
  items: readonly T[];
  itemKey: (item: T) => string | number;
  onChange: (items: T[]) => void;
  render: (item: T, index: number) => ReactNode;
  /** Highlights one row (for example the selected server). */
  activeKey?: string | number | null;
  onSelect?: (item: T) => void;
  disabled?: boolean;
  className?: string;
  label?: string;
  /** Names an item for assistive technology (move buttons and the position announcement). */
  itemLabel?: (item: T) => string;
}

/**
 * A reorderable list. Rows can be dragged by their handle or moved with the
 * up/down buttons, which keeps the control usable from the keyboard.
 */
export function SortableList<T>({ items, itemKey, onChange, render, activeKey, onSelect, disabled, className, label, itemLabel }: SortableListProps<T>) {
  const t = useT();
  const [announce, setAnnounce] = useState("");
  const move = (from: number, to: number) => {
    const item = items[from];
    onChange(moveItem(items, from, to));
    if (item !== undefined && itemLabel) setAnnounce(t("adminCommon.reorder.moved", { name: itemLabel(item), position: to + 1, total: items.length }));
  };
  const named = (action: string, item: T) => (itemLabel ? `${action}: ${itemLabel(item)}` : action);
  const [dragIndex, setDragIndex] = useState<number | null>(null);
  const [overIndex, setOverIndex] = useState<number | null>(null);
  const end = () => {
    setDragIndex(null);
    setOverIndex(null);
  };
  return (
    <>
    <span role="status" aria-live="polite" className="sr-only">
      {announce}
    </span>
    <ul aria-label={label} className={cn("grid gap-1.5", className)}>
      {items.map((item, index) => {
        const key = itemKey(item);
        return (
          <li
            key={key}
            draggable={!disabled}
            onDragStart={(event) => {
              setDragIndex(index);
              event.dataTransfer.effectAllowed = "move";
              event.dataTransfer.setData("text/plain", String(key));
            }}
            onDragOver={(event) => {
              if (dragIndex === null) return;
              event.preventDefault();
              setOverIndex(index);
            }}
            onDrop={(event) => {
              event.preventDefault();
              if (dragIndex !== null && dragIndex !== index) onChange(moveItem(items, dragIndex, index));
              end();
            }}
            onDragEnd={end}
            className={cn(
              "flex items-center gap-2 rounded-lg border bg-card px-2 py-1.5 text-sm transition-colors",
              activeKey === key && "border-primary bg-accent/40",
              overIndex === index && dragIndex !== index && "border-primary border-dashed",
              dragIndex === index && "opacity-50",
              onSelect && "cursor-pointer",
            )}
            onClick={onSelect ? () => onSelect(item) : undefined}
            tabIndex={onSelect ? 0 : undefined}
            aria-current={onSelect && activeKey === key ? "true" : undefined}
            onKeyDown={
              onSelect
                ? (event) => {
                    if (event.target === event.currentTarget && (event.key === "Enter" || event.key === " ")) {
                      event.preventDefault();
                      onSelect(item);
                    }
                  }
                : undefined
            }
          >
            <GripVertical aria-hidden className="size-4 shrink-0 cursor-grab text-muted-foreground" />
            <div className="min-w-0 flex-1">{render(item, index)}</div>
            <div className="flex shrink-0 items-center" onClick={(event) => event.stopPropagation()}>
              <Button variant="ghost" size="icon-xs" disabled={disabled || index === 0} aria-label={named(t("adminCommon.reorder.moveUp"), item)} onClick={() => move(index, index - 1)}>
                <ArrowUp />
              </Button>
              <Button variant="ghost" size="icon-xs" disabled={disabled || index === items.length - 1} aria-label={named(t("adminCommon.reorder.moveDown"), item)} onClick={() => move(index, index + 1)}>
                <ArrowDown />
              </Button>
            </div>
          </li>
        );
      })}
    </ul>
    </>
  );
}
