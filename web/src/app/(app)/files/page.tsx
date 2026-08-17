import { Suspense } from "react";
import { FilesPage } from "@/features/files-kb-library";

export default function Page() {
  return (
    <Suspense>
      <FilesPage />
    </Suspense>
  );
}
