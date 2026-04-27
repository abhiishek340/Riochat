// Helpers for the settings component tests.
import { render } from "@testing-library/react";
import type { ReactElement } from "react";
import { dismiss, getToasts } from "@/components/ui/toast-store";
import { TooltipProvider } from "@/components/ui/tooltip";
import { I18nProvider } from "@/lib/i18n/provider";

function withProviders(ui: ReactElement) {
  return (
    <I18nProvider>
      <TooltipProvider>{ui}</TooltipProvider>
    </I18nProvider>
  );
}

export function renderWithApp(ui: ReactElement) {
  const view = render(withProviders(ui));
  return { ...view, rerenderWithApp: (next: ReactElement) => view.rerender(withProviders(next)) };
}

export function toastTitles(): string[] {
  return getToasts().map((t) => t.title);
}

export function clearToasts(): void {
  dismiss();
}
