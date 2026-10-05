import type { GraphNode, Properties } from "./types";

// Hours of operation for location CIs are stored one property per weekday
// (hoursMonday … hoursSunday) as "HH:MM-HH:MM" or "closed", read in the
// location's IANA timezone property. These helpers are shared by the record
// form, the record list, and the location map.

export const weekdays = ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"];
export const hoursKey = (day: string) => `hours${day}`;
export const hoursClosed = "closed";

export type DailyHours = { closed: boolean; open: string; close: string };
export type HoursState = Record<string, DailyHours>;

export function parseDailyHours(value: unknown): DailyHours | null {
  if (typeof value !== "string" || !value.trim()) return null;
  const text = value.trim().toLowerCase();
  if (text === hoursClosed) return { closed: true, open: "", close: "" };
  const match = /^(\d{1,2}:\d{2})-(\d{1,2}:\d{2})$/.exec(text);
  return match ? { closed: false, open: match[1].padStart(5, "0"), close: match[2].padStart(5, "0") } : null;
}

export function hoursStateFrom(properties: Properties): HoursState {
  return Object.fromEntries(weekdays.map((day) => {
    const stored = parseDailyHours(properties[hoursKey(day)]);
    const weekend = day === "Saturday" || day === "Sunday";
    return [day, stored ?? { closed: weekend, open: "08:00", close: "17:00" }];
  }));
}

function minutesOf(clock: string) {
  const [hour, minute] = clock.split(":").map(Number);
  return hour * 60 + minute;
}

// "HH:MM" (24-hour, as stored) → "h:MM AM/PM" for display.
export function formatClock(clock: string) {
  const match = /^(\d{1,2}):(\d{2})$/.exec(clock.trim());
  if (!match) return clock;
  const hour = Number(match[1]) % 24;
  return `${hour % 12 || 12}:${match[2]} ${hour < 12 ? "AM" : "PM"}`;
}

// Display text for one day's hours, e.g. "8:00 AM – 5:00 PM" or "Closed".
export function formatDailyHours(value: unknown, fallback = "—") {
  const hours = parseDailyHours(value);
  if (!hours) return fallback;
  return hours.closed ? "Closed" : `${formatClock(hours.open)} – ${formatClock(hours.close)}`;
}

// Local weekday and time at a location, or null when the timezone is unusable.
export function localClock(properties: Properties, at = new Date()): { weekday: string; minute: number; time: string } | null {
  const timeZone = typeof properties.timezone === "string" && properties.timezone.trim() ? properties.timezone.trim() : undefined;
  let parts: Intl.DateTimeFormatPart[];
  try {
    parts = new Intl.DateTimeFormat("en-US", { timeZone, weekday: "long", hour: "2-digit", minute: "2-digit", hourCycle: "h23" }).formatToParts(at);
  } catch {
    return null;
  }
  const part = (type: Intl.DateTimeFormatPartTypes) => parts.find((item) => item.type === type)?.value ?? "";
  const hour = Number(part("hour")) % 24;
  const minute = Number(part("minute"));
  return { weekday: part("weekday"), minute: hour * 60 + minute, time: formatClock(`${hour}:${String(minute).padStart(2, "0")}`) };
}

// "open" or "closed" for the location right now, or null when its hours are unknown.
export function locationOpenNow(properties: Properties, at = new Date()): "open" | "closed" | null {
  const clock = localClock(properties, at);
  if (!clock) return null;
  const index = weekdays.indexOf(clock.weekday);
  if (index < 0) return null;
  const today = parseDailyHours(properties[hoursKey(clock.weekday)]);
  const yesterday = parseDailyHours(properties[hoursKey(weekdays[(index + 6) % 7])]);
  if (today && !today.closed) {
    const open = minutesOf(today.open);
    const close = minutesOf(today.close);
    if (close > open ? clock.minute >= open && clock.minute < close : clock.minute >= open) return "open";
  }
  if (yesterday && !yesterday.closed && minutesOf(yesterday.close) <= minutesOf(yesterday.open) && clock.minute < minutesOf(yesterday.close)) return "open";
  return today ? "closed" : null;
}

export function isLocationCI(node: GraphNode) {
  return node.kind === "ci" && node.properties?.ciType === "location";
}

// Plottable coordinates stored on a location CI, if any.
export function locationCoordinates(properties: Properties): { latitude: number; longitude: number; precision: string } | null {
  const latitude = Number(properties.latitude);
  const longitude = Number(properties.longitude);
  if (!Number.isFinite(latitude) || !Number.isFinite(longitude) || properties.latitude === undefined || properties.longitude === undefined) return null;
  return { latitude, longitude, precision: typeof properties.geoPrecision === "string" ? properties.geoPrecision : "" };
}

export function formatAddress(properties: Properties) {
  const line = [properties.city, properties.state].filter((part) => typeof part === "string" && part).join(", ");
  return [properties.addressLine1, line, properties.postalCode].filter((part) => typeof part === "string" && part).join(" · ");
}
