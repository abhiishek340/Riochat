import {
  Archive,
  Code2,
  FileAudio,
  FileImage,
  FileSpreadsheet,
  FileText,
  FileVideo,
  Presentation,
  type LucideIcon,
} from "lucide-react";
import { categoryOf, type FileCategory } from "../file-kind";

const ICONS: Record<FileCategory, LucideIcon> = {
  image: FileImage,
  pdf: FileText,
  audio: FileAudio,
  video: FileVideo,
  spreadsheet: FileSpreadsheet,
  presentation: Presentation,
  document: FileText,
  code: Code2,
};

export function iconForFile(file: { fileName: string; mimeType: string }): LucideIcon {
  if (!file.fileName && !file.mimeType) return Archive;
  return ICONS[categoryOf(file.fileName, file.mimeType)];
}

export function FileIcon({ file, className }: { file: { fileName: string; mimeType: string }; className?: string }) {
  const Icon = iconForFile(file);
  return <Icon aria-hidden className={className ?? "size-4"} />;
}
