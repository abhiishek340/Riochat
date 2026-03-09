// Shared helpers for component tests in this feature.
import { render } from "@testing-library/react";
import type { ReactElement } from "react";
import { getToasts, dismiss } from "@/components/ui/toast-store";
import { TooltipProvider } from "@/components/ui/tooltip";
import { I18nProvider } from "@/lib/i18n/provider";

export function renderWithI18n(ui: ReactElement) {
  return render(
    <I18nProvider>
      <TooltipProvider>{ui}</TooltipProvider>
    </I18nProvider>,
  );
}

export function toastTitles(): string[] {
  return getToasts().map((t) => t.title);
}

export function clearToasts(): void {
  dismiss();
}
