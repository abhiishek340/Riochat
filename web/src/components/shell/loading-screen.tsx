import { Spinner } from "@/components/ui/spinner";

/** Full-area centred loading label, used while a guard decides. */
export function LoadingScreen({ label }: { label: string }) {
  return (
    <div role="status" className="flex min-h-dvh flex-1 flex-col items-center justify-center gap-3 text-muted-foreground">
      <Spinner className="size-5" />
      <span className="text-sm">{label}</span>
    </div>
  );
}
