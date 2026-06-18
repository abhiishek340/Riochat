export const announcements = {
  title: "Announcements",
  description: "Messages from the administrators of this server.",
  sortLabel: "Sort announcements",
  listLabel: "Announcement list",
  sort: {
    default: "Default",
    type: "By type",
    time: "By time",
  },
  pinned: "Pinned",
  unread: "Unread",
  loading: "Loading announcements…",
  empty: "There are no announcements.",
  dismissToday: "Not again today",
  openFailed: "Announcements could not be loaded.",
  dismissFailed: "Some announcements could not be hidden for today. Try again.",
  closeFailed: "Some announcements could not be marked as read. Try again.",
} as const;
