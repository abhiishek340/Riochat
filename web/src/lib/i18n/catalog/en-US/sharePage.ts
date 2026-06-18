export const sharePage = {
  untitled: "Shared conversation",
  count: "{count, plural, one {# snapshot message} other {# snapshot messages}}",
  unavailable: {
    title: "Share unavailable",
    body: "This link may have been turned off, or the conversation no longer exists.",
  },
  systemRole: "System",
  replyFailed: "This reply did not finish.",
  blocked: "This message is not available in the shared copy.",
  branch: {
    label: "Other versions of this message",
    previous: "Previous version",
    next: "Next version",
  },
  meta: {
    tokens: "{input} in, {output} out",
  },
  cta: {
    continue: "Continue conversation",
    continuing: "Continuing…",
    signIn: "Sign in to continue",
    failed: "Failed to continue conversation",
  },
} as const;
