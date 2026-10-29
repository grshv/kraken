import { useState } from "react";
import { LogOut, KeyRound, UserRound } from "lucide-react";
import type { Session } from "../model";

// Avatar with profile menu. Closes on outside click, Escape, or action. The
// avatar initial is the first letter of the username (Latin fallback "A").
export function ProfileMenu({
  session,
  onChangePassword,
  onLogout,
}: {
  session: Session;
  onChangePassword: () => void;
  onLogout: () => void;
}) {
  const [open, setOpen] = useState(false);
  const name = session.username || "admin";
  const role = session.role === "manager" ? "Менеджер" : "Администратор";
  const initial = (name[0] || "A").toUpperCase();
  return (
    <div className="profile-menu">
      <button
        type="button"
        className={"avatar" + (open ? " active" : "")}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={`Профиль ${name}`}
        onClick={() => setOpen((v) => !v)}
        onKeyDown={(e) => {
          if (e.key === "Escape") setOpen(false);
        }}
      >
        <span className="avatar-initial">{initial}</span>
        <span className="avatar-online" aria-hidden />
      </button>
      {open && (
        <>
          <div
            className="popover-overlay"
            aria-hidden
            onClick={() => setOpen(false)}
          />
          <div role="menu" aria-label="Профиль" className="profile-popover">
            <div className="profile-head">
              <span className="avatar static" aria-hidden>
                <span className="avatar-initial">{initial}</span>
              </span>
              <div className="profile-meta">
                <div className="profile-name">{name}</div>
                <div className="muted-text small">{role}</div>
              </div>
            </div>
            <div className="menu-separator" />
            <button
              type="button"
              role="menuitem"
              className="menu-item"
              onClick={() => {
                setOpen(false);
                onChangePassword();
              }}
            >
              <UserRound aria-hidden />
              Мой аккаунт
            </button>
            <button
              type="button"
              role="menuitem"
              className="menu-item"
              onClick={() => {
                setOpen(false);
                onChangePassword();
              }}
            >
              <KeyRound aria-hidden />
              Сменить пароль
            </button>
            <div className="menu-separator" />
            <button
              type="button"
              role="menuitem"
              className="menu-item danger"
              onClick={() => {
                setOpen(false);
                onLogout();
              }}
            >
              <LogOut aria-hidden />
              Выйти
            </button>
          </div>
        </>
      )}
    </div>
  );
}
