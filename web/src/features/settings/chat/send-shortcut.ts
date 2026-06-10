import { isApplePlatform, type SendShortcut } from "../lib/chat-settings";

export interface ShortcutOption {
  /** Value written to the settings store. */
  value: SendShortcut;
  label: string;
}

/** Modifier label of the current platform. */
export function modifierLabel(apple: boolean = isApplePlatform()): string {
  return apple ? "Command" : "Ctrl";
}

/** The two choices offered: plain Enter, and the platform modifier plus Enter. */
export function shortcutOptions(apple: boolean = isApplePlatform()): ShortcutOption[] {
  return [
    { value: "enter", label: "Enter" },
    { value: apple ? "meta_enter" : "ctrl_enter", label: `${modifierLabel(apple)}+Enter` },
  ];
}

export function shortcutLabel(value: SendShortcut, apple: boolean = isApplePlatform()): string {
  return value === "enter" ? "Enter" : `${modifierLabel(apple)}+Enter`;
}
