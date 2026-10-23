import { describe, expect, it } from "vitest";
import { formatAddress, formatClock, formatDailyHours, hoursStateFrom, locationCoordinates, locationOpenNow, parseDailyHours } from "./locations";

describe("parseDailyHours", () => {
  it("parses ranges and pads single-digit hours", () => {
    expect(parseDailyHours("8:00-17:30")).toEqual({ closed: false, open: "08:00", close: "17:30" });
  });

  it("recognizes closed in any case", () => {
    expect(parseDailyHours(" Closed ")).toEqual({ closed: true, open: "", close: "" });
  });

  it("rejects blank and malformed values", () => {
    expect(parseDailyHours("")).toBeNull();
    expect(parseDailyHours("9 to 5")).toBeNull();
    expect(parseDailyHours(42)).toBeNull();
  });
});

describe("hoursStateFrom", () => {
  it("defaults weekdays open and weekends closed", () => {
    const state = hoursStateFrom({ hoursMonday: "07:00-15:00" });
    expect(state.Monday).toEqual({ closed: false, open: "07:00", close: "15:00" });
    expect(state.Tuesday).toEqual({ closed: false, open: "08:00", close: "17:00" });
    expect(state.Sunday.closed).toBe(true);
  });
});

describe("formatting", () => {
  it("formats 24-hour clocks as 12-hour", () => {
    expect(formatClock("00:15")).toBe("12:15 AM");
    expect(formatClock("12:00")).toBe("12:00 PM");
    expect(formatClock("17:30")).toBe("5:30 PM");
    expect(formatClock("noon")).toBe("noon");
  });

  it("formats a day's hours", () => {
    expect(formatDailyHours("08:00-17:00")).toBe("8:00 AM – 5:00 PM");
    expect(formatDailyHours("closed")).toBe("Closed");
    expect(formatDailyHours(undefined)).toBe("—");
  });

  it("joins the address parts that are present", () => {
    expect(formatAddress({ addressLine1: "1 Main St", city: "Austin", state: "TX", postalCode: "78701" })).toBe("1 Main St · Austin, TX · 78701");
    expect(formatAddress({ city: "Austin" })).toBe("Austin");
  });
});

describe("locationCoordinates", () => {
  it("returns numeric coordinates", () => {
    expect(locationCoordinates({ latitude: "30.27", longitude: -97.74, geoPrecision: "city" })).toEqual({ latitude: 30.27, longitude: -97.74, precision: "city" });
  });

  it("returns null when either coordinate is missing or invalid", () => {
    expect(locationCoordinates({ latitude: 30.27 })).toBeNull();
    expect(locationCoordinates({ latitude: "north", longitude: 1 })).toBeNull();
  });
});

describe("locationOpenNow", () => {
  // Wednesday 2026-01-07 in UTC.
  const at = (time: string) => new Date(`2026-01-07T${time}:00Z`);
  const utc = { timezone: "UTC" };

  it("is open within today's hours", () => {
    expect(locationOpenNow({ ...utc, hoursWednesday: "08:00-17:00" }, at("12:00"))).toBe("open");
    expect(locationOpenNow({ ...utc, hoursWednesday: "08:00-17:00" }, at("17:00"))).toBe("closed");
  });

  it("stays open past midnight on overnight hours", () => {
    expect(locationOpenNow({ ...utc, hoursTuesday: "22:00-02:00", hoursWednesday: "closed" }, at("01:00"))).toBe("open");
  });

  it("uses the location's timezone", () => {
    // 15:00 UTC is 09:00 in Chicago (CST).
    expect(locationOpenNow({ timezone: "America/Chicago", hoursWednesday: "08:00-10:00" }, at("15:00"))).toBe("open");
  });

  it("is unknown without hours or with a bad timezone", () => {
    expect(locationOpenNow(utc, at("12:00"))).toBeNull();
    expect(locationOpenNow({ timezone: "Not/AZone", hoursWednesday: "08:00-17:00" }, at("12:00"))).toBeNull();
  });
});
