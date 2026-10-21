import { useEffect, useRef, useState } from "react";
import { ChevronDown, House, Undo2, UserRound } from "lucide-react";
import RecordPicker from "./RecordPicker";
import { superAdmin, type SessionUser } from "./session";
import type { GraphNode } from "./types";

// The signed-in user, shown at the top of the rail. Opening it shows who is
// signed in and, for the super admin, lets them act as any configured identity
// so that identity's profile and queue can be seen.
export default function UserMenu({ user, identities, identitiesError, onActAs, onGoHome }: {
  user: SessionUser;
  identities: GraphNode[];
  identitiesError: string;
  onActAs: (identityId: string) => void;
  onGoHome: () => void;
}) {
  const [open, setOpen] = useState(false);
  const menu = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const close = (event: MouseEvent) => { if (menu.current && !menu.current.contains(event.target as Node)) setOpen(false); };
    const escape = (event: KeyboardEvent) => { if (event.key === "Escape") setOpen(false); };
    document.addEventListener("mousedown", close);
    document.addEventListener("keydown", escape);
    return () => { document.removeEventListener("mousedown", close); document.removeEventListener("keydown", escape); };
  }, [open]);

  return <div className="user-menu" ref={menu}>
    <button type="button" className="user-chip" aria-haspopup="dialog" aria-expanded={open} onClick={() => setOpen((value) => !value)} title="Signed-in user">
      <span className="user-chip-avatar" aria-hidden="true"><UserRound size={18} strokeWidth={1.8} /></span>
      <span className="user-chip-text"><strong>{user.name}</strong><small>{user.superAdmin ? "Platform super admin" : `Acting as · ${user.id}`}</small></span>
      <ChevronDown aria-hidden="true" size={15} className={open ? "open" : ""} />
    </button>
    {open && <section className="user-panel" role="dialog" aria-label="Signed-in user">
      <p className="eyebrow">SIGNED IN</p>
      <strong className="user-panel-name">{superAdmin.name}</strong>
      <p className="field-hint">Development sign-in with access to everything. Single sign-on from Entra ID will replace this; until then the super admin may act as any configured identity.</p>
      {identitiesError ? <p className="form-error" role="alert">{identitiesError}</p>
        : <RecordPicker id="act-as" label="Act as identity" hint="Their profile and work queue show across the portal until you return." placeholder="Find an identity by name or ID" single options={identities.filter((identity) => identity.id !== "platform-super-admin")} selected={user.superAdmin ? [] : [user.id]} onChange={(ids) => { onActAs(ids[0] ?? ""); setOpen(false); }} />}
      <div className="user-panel-actions">
        <button type="button" className="button button-quiet" onClick={() => { onGoHome(); setOpen(false); }}><House size={14} />My home</button>
        {!user.superAdmin && <button type="button" className="button button-quiet" onClick={() => { onActAs(""); setOpen(false); }}><Undo2 size={14} />Back to super admin</button>}
      </div>
    </section>}
  </div>;
}
