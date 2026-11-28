import type { RecordKind } from "./types";

/** Something opened or searched for recently, kept on this device for each user. */
export type RecentItem =
  | { type: "record"; kind: RecordKind; title: string; subtitle: string; link: string }
  | { type: "search"; text: string; link: string };

export const RECENT_LIMIT = 5;
const key = (userId: string) => `bms-search-recent:${userId}`;

export function readRecent(userId: string): RecentItem[] {
  try {
    const stored: unknown = JSON.parse(localStorage.getItem(key(userId)) ?? "[]");
    if (!Array.isArray(stored)) return [];
    return stored
      .filter(
        (item): item is RecentItem =>
          !!item &&
          typeof item.link === "string" &&
          // Only ever follow links inside the app.
          item.link.startsWith("/") &&
          !item.link.startsWith("//") &&
          typeof (item.type === "search" ? item.text : item.title) === "string",
      )
      .slice(0, RECENT_LIMIT);
  } catch {
    return [];
  }
}

function write(userId: string, items: RecentItem[]) {
  try {
    localStorage.setItem(key(userId), JSON.stringify(items));
  } catch {
    // Private browsing or a full disk: recents are a convenience.
  }
}

/** Puts an item first, dropping an older entry for the same place. */
export function addRecent(userId: string, item: RecentItem) {
  const next = [item, ...readRecent(userId).filter((other) => other.link !== item.link)].slice(
    0,
    RECENT_LIMIT,
  );
  write(userId, next);
  return next;
}

export function removeRecent(userId: string, link: string) {
  const next = readRecent(userId).filter((item) => item.link !== link);
  write(userId, next);
  return next;
}
