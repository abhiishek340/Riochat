"use client";

import { useEffect, useState } from "react";
import { toast, TOAST_POSITIONS, TOAST_POSITION_EVENT, readToastPosition, setToastPosition, type ToastPosition } from "@/components/ui/toast-store";
import { useT } from "@/lib/i18n/provider";
import { ACCENTS, type Accent, type ThemeMode } from "@/lib/theme/config";
import { useTheme } from "@/lib/theme/provider";
import { cn } from "@/lib/utils/cn";
import { OptionCard, OptionGrid, SettingsSection } from "../parts";
import { FONT_SCALE, FONT_SIZES, type FontSize } from "../lib/appearance-prefs";
import { setFontSize, useAppearanceSaver, useFontSize } from "../lib/appearance-device";

/** Hue/chroma of each accent preset, for the swatch previews. */
const ACCENT_SWATCH: Record<Accent, { hue: number; chroma: number; tone: "cool" | "warm" }> = {
  tide: { hue: 200, chroma: 0.1, tone: "cool" },
  ember: { hue: 45, chroma: 0.15, tone: "warm" },
  moss: { hue: 150, chroma: 0.11, tone: "cool" },
  dusk: { hue: 290, chroma: 0.14, tone: "cool" },
};

function PresetPreview({ accent, dark }: { accent: Accent; dark: boolean }) {
  const { hue, chroma } = ACCENT_SWATCH[accent];
  const bg = dark ? "oklch(0.2 0.014 250)" : "oklch(0.985 0.005 85)";
  const surface = dark ? "oklch(0.26 0.016 250)" : "oklch(1 0 0)";
  const primary = dark ? `oklch(0.74 ${chroma} ${hue})` : `oklch(0.5 ${chroma} ${hue})`;
  const soft = dark ? `oklch(0.32 0.04 ${hue})` : `oklch(0.93 0.035 ${hue})`;
  return (
    <div className="grid h-14 grid-cols-[1fr_2fr] gap-1 p-1.5" style={{ background: bg }}>
      <div className="grid content-start gap-1 rounded-sm p-1" style={{ background: surface }}>
        <span className="h-1.5 rounded-full" style={{ background: primary }} />
        <span className="h-1.5 w-2/3 rounded-full" style={{ background: soft }} />
      </div>
      <div className="grid content-end gap-1 rounded-sm p-1" style={{ background: surface }}>
        <span className="h-1.5 w-4/5 rounded-full" style={{ background: soft }} />
        <span className="h-2.5 w-1/2 rounded-sm" style={{ background: primary }} />
      </div>
    </div>
  );
}

function ModePreview({ mode }: { mode: ThemeMode }) {
  const light = "oklch(0.985 0.005 85)";
  const dark = "oklch(0.2 0.014 250)";
  const background = mode === "light" ? light : mode === "dark" ? dark : `linear-gradient(135deg, ${light} 50%, ${dark} 50%)`;
  return (
    <div className="h-14 p-1.5" style={{ background }}>
      <div className="h-full rounded-sm border border-black/10" style={{ background: mode === "dark" ? "oklch(0.26 0.016 250)" : mode === "light" ? "#fff" : "transparent" }} />
    </div>
  );
}

function PositionPreview({ position }: { position: ToastPosition }) {
  const [vertical, horizontal] = position.split("-") as ["top" | "bottom", "left" | "center" | "right"];
  return (
    <div className="relative h-14 bg-muted">
      <span
        className={cn(
          "absolute h-2 w-1/3 rounded-full bg-primary",
          vertical === "top" ? "top-1.5" : "bottom-1.5",
          horizontal === "left" && "left-1.5",
          horizontal === "center" && "left-1/3",
          horizontal === "right" && "right-1.5",
        )}
      />
    </div>
  );
}

export function AppearanceSection() {
  const t = useT();
  const { mode, setMode, accent, setAccent, resolved } = useTheme();
  const fontSize = useFontSize();
  const { schedule } = useAppearanceSaver();
  const [position, setPosition] = useState<ToastPosition>("top-right");

  useEffect(() => {
    const read = () => setPosition(readToastPosition());
    read();
    window.addEventListener(TOAST_POSITION_EVENT, read);
    return () => window.removeEventListener(TOAST_POSITION_EVENT, read);
  }, []);

  const modes: ThemeMode[] = ["light", "system", "dark"];
  const dark = resolved === "dark";

  return (
    <SettingsSection title={t("settings.general.appearance.title")} description={t("settings.general.appearance.description")} bare>
      <div className="grid gap-6 rounded-xl border bg-card p-5 shadow-sm">
        <div className="grid gap-2.5">
          <h4 className="text-sm font-medium">{t("settings.general.appearance.preset")}</h4>
          <OptionGrid label={t("settings.general.appearance.preset")}>
            {ACCENTS.map((a) => (
              <OptionCard
                key={a}
                pressed={accent === a}
                title={t(`settings.general.appearance.presets.${a}`)}
                caption={t(`settings.general.appearance.tones.${ACCENT_SWATCH[a].tone}`)}
                onPress={() => {
                  setAccent(a);
                  schedule({ preset: a });
                }}
              >
                <PresetPreview accent={a} dark={dark} />
              </OptionCard>
            ))}
          </OptionGrid>
        </div>

        <div className="grid gap-2.5">
          <h4 className="text-sm font-medium">{t("settings.general.appearance.mode")}</h4>
          <OptionGrid label={t("settings.general.appearance.mode")} columns="sm:grid-cols-3">
            {modes.map((m) => (
              <OptionCard key={m} pressed={mode === m} title={t(`settings.general.appearance.modes.${m}`)} onPress={() => setMode(m)}>
                <ModePreview mode={m} />
              </OptionCard>
            ))}
          </OptionGrid>
          <p className="text-[13px] text-muted-foreground">{t("settings.general.appearance.deviceOnly")}</p>
        </div>

        <div className="grid gap-2.5">
          <h4 className="text-sm font-medium">{t("settings.general.appearance.fontSize")}</h4>
          <OptionGrid label={t("settings.general.appearance.fontSize")}>
            {FONT_SIZES.map((size: FontSize) => (
              <OptionCard
                key={size}
                pressed={fontSize === size}
                title={t(`settings.general.appearance.fontSizes.${size}`)}
                onPress={() => {
                  setFontSize(size);
                  schedule({ fontSize: size });
                }}
              >
                <div className="flex h-14 items-center justify-center bg-muted/60 font-medium" style={{ fontSize: `${FONT_SCALE[size] * 16}px` }}>
                  {t("settings.general.appearance.sample")}
                </div>
              </OptionCard>
            ))}
          </OptionGrid>
        </div>

        <div className="grid gap-2.5">
          <h4 className="text-sm font-medium">{t("settings.general.appearance.toastPosition")}</h4>
          <OptionGrid label={t("settings.general.appearance.toastPosition")} columns="sm:grid-cols-3">
            {TOAST_POSITIONS.map((p) => (
              <OptionCard
                key={p}
                pressed={position === p}
                title={t(`settings.general.appearance.positions.${p}`)}
                onPress={() => {
                  setToastPosition(p);
                  setPosition(p);
                  toast.success(t("settings.general.appearance.positionPreview"), { id: "settings-toast-position" });
                }}
              >
                <PositionPreview position={p} />
              </OptionCard>
            ))}
          </OptionGrid>
        </div>
      </div>
    </SettingsSection>
  );
}
