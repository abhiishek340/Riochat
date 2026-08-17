import { Suspense } from "react";
import { ChatPage } from "@/features/chat";

// Reads search params, so it needs a suspense boundary in a static export.
export default function Page() {
  return (
    <Suspense>
      <ChatPage />
    </Suspense>
  );
}
