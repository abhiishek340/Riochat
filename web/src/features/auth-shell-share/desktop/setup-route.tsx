"use client";

import { useRouter } from "next/navigation";
import { bindShellServer } from "./gate";
import { shell } from "./bridge";
import { ServerSetup } from "./setup-page";

/** `/desktop/setup`: pick (or change) the server this install talks to. Browsers have nothing to set up. */
export function DesktopSetupPage() {
  const router = useRouter();
  return (
    <ServerSetup
      onBound={async () => {
        const server = await shell.getServer();
        if (server) await bindShellServer(server);
        router.replace("/chat");
      }}
    />
  );
}
