"use client";

import { Button } from "@/components/ui/button";
import { Field, FieldHint, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { useT } from "@/lib/i18n/provider";
import { sanitizeCode, type VerificationMethod } from "../lib/policy";
import type { CodeSender } from "./use-send-code";

/** "Resend in 12s" / "Send code" / "Resend code". */
export function SendCodeButton({ sender, label, disabled, sentOnce }: { sender: CodeSender; label?: string; disabled?: boolean; sentOnce?: boolean }) {
  const t = useT();
  const text = sender.cooldown.active
    ? t("settings.account.verify.resendIn", { seconds: sender.cooldown.left })
    : (label ?? (sentOnce ? t("settings.account.verify.resend") : t("settings.account.verify.send")));
  return (
    <Button type="button" variant="outline" loading={sender.sending} disabled={disabled || sender.cooldown.active} onClick={() => void sender.send()}>
      {text}
    </Button>
  );
}

/**
 * The code box for one verification method: authenticator/recovery code or a
 * six-digit mail code with its send button. `none` renders nothing. When a
 * second real method exists a link switches to it.
 */
export function VerificationField({
  methods,
  method,
  onMethod,
  code,
  onCode,
  sender,
  autoFocus,
}: {
  methods: VerificationMethod[];
  method: VerificationMethod;
  onMethod: (m: VerificationMethod) => void;
  code: string;
  onCode: (code: string) => void;
  sender?: CodeSender;
  autoFocus?: boolean;
}) {
  const t = useT();
  if (method === "none") return null;
  const other = methods.find((m) => m !== method && m !== "none");
  const isEmail = method === "email";
  return (
    <div className="grid gap-2">
      <Field>
        <FieldLabel>{isEmail ? t("settings.account.verify.emailCode") : t("settings.account.verify.authCode")}</FieldLabel>
        <div className="flex gap-2">
          <Input
            value={code}
            inputMode={isEmail ? "numeric" : "text"}
            autoComplete="one-time-code"
            autoFocus={autoFocus}
            spellCheck={false}
            className="font-mono tracking-widest"
            placeholder={isEmail ? "000000" : "123456"}
            onChange={(e) => onCode(sanitizeCode(method, e.target.value))}
          />
          {isEmail && sender ? <SendCodeButton sender={sender} sentOnce={sender.sent} /> : null}
        </div>
        <FieldHint>{isEmail ? t("settings.account.verify.emailHint") : t("settings.account.verify.authHint")}</FieldHint>
      </Field>
      {isEmail && sender?.debugCode ? <p className="font-mono text-xs text-muted-foreground">{t("settings.account.verify.debugCode", { code: sender.debugCode })}</p> : null}
      {other ? (
        <Button type="button" variant="link" className="w-fit text-[13px]" onClick={() => onMethod(other)}>
          {other === "email" ? t("settings.account.verify.useEmail") : t("settings.account.verify.useAuthenticator")}
        </Button>
      ) : null}
    </div>
  );
}

