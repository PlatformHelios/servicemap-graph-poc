import type { AgreementClock, AgreementStatus } from "./types";

const statusLabel: Record<AgreementStatus, string> = {
  none: "Not measured",
  "on-track": "In",
  "due-today": "Due soon",
  overdue: "Out",
  met: "Met",
  missed: "Missed",
};

export const businessDayChoices = [1, 2, 3, 4, 5, 7, 10, 15, 20, 25, 30, 45, 60, 90];

function meterFill(clock: AgreementClock) {
  if (clock.status === "met" || clock.status === "overdue" || clock.status === "missed") return 1;
  return Math.max(0, Math.min(1, clock.progress || 0));
}

export function AgreementMeter({ clock, compact }: { clock?: AgreementClock | null; compact?: boolean }) {
  if (!clock || !clock.enabled) return null;
  const fill = meterFill(clock);
  const overdue = clock.status === "overdue" || clock.status === "missed";
  const due = clock.status === "due-today";
  const met = clock.status === "met";
  const size = compact ? 22 : 36;
  const stroke = compact ? 3 : 4;
  const radius = (size - stroke) / 2;
  const circumference = 2 * Math.PI * radius;
  const offset = circumference * (1 - fill);
  const title = `${clock.kind.toUpperCase()} · ${statusLabel[clock.status]}${clock.dueAt ? ` · due ${clock.dueAt}` : ""} · ${clock.elapsedDays}/${clock.businessDays} business days · ${Math.round(fill * 100)}% of window used (8-hour steps)`;
  return <span className={`agreement-meter ${clock.kind} status-${clock.status}${compact ? " compact" : ""}`} title={title} role="img" aria-label={title}>
    <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} aria-hidden="true">
      <circle className="agreement-track" cx={size / 2} cy={size / 2} r={radius} fill="none" strokeWidth={stroke} />
      <circle className="agreement-fill" cx={size / 2} cy={size / 2} r={radius} fill="none" strokeWidth={stroke} strokeDasharray={circumference} strokeDashoffset={offset} transform={`rotate(-90 ${size / 2} ${size / 2})`} />
    </svg>
    <span className="agreement-copy">
      <strong>{clock.kind.toUpperCase()} {statusLabel[clock.status]}</strong>
      {!compact && <small>{overdue ? `${Math.max(0, clock.elapsedDays - (clock.businessDays ?? 0))} business day${Math.max(0, clock.elapsedDays - (clock.businessDays ?? 0)) === 1 ? "" : "s"} over` : due ? "Final 8 hours" : met ? `Completed in ${clock.elapsedDays} of ${clock.businessDays} business days` : `${clock.remainingDays} business day${clock.remainingDays === 1 ? "" : "s"} left`}{clock.dueAt ? ` · ${clock.dueAt}` : ""}</small>}
    </span>
  </span>;
}

export function SLANotice({ enabled, days }: { enabled: boolean; days: number }) {
  if (!enabled || days < 1) return null;
  return <p className="field-hint sla-notice">This request has an SLA of <strong>{days} business day{days === 1 ? "" : "s"}</strong> to complete (Monday–Friday, skipping platform holidays). The meter advances every 8 business hours and does not move on weekends.</p>;
}
