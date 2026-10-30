import { LogOut, KeyRound, UserRound } from "lucide-react";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
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
  const name = session.username || "admin";
  const role = session.role === "manager" ? "Менеджер" : "Администратор";
  const initial = (name[0] || "A").toUpperCase();
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          className="relative mb-1 mt-1.5 flex size-8 items-center justify-center rounded-full border border-border bg-transparent hover:border-ring focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring"
          aria-label={`Профиль ${name}`}
        >
          <Avatar className="size-full">
            <AvatarFallback className="bg-muted text-base font-medium text-foreground">
              {initial}
            </AvatarFallback>
          </Avatar>
          <span
            className="absolute -bottom-px -right-px z-10 size-2.5 rounded-full border-2 border-sidebar bg-ok"
            aria-hidden
          />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" side="right" className="w-[268px] p-1.5">
        <div className="flex items-center gap-2.5 px-2 pb-2.5 pt-2">
          <Avatar className="size-8 border border-border">
            <AvatarFallback className="bg-muted text-base font-medium text-foreground">
              {initial}
            </AvatarFallback>
          </Avatar>
          <div className="min-w-0">
            <div className="font-semibold">{name}</div>
            <div className="text-sm text-muted-foreground">{role}</div>
          </div>
        </div>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={() => onChangePassword()}>
          <UserRound aria-hidden /> Мой аккаунт
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => onChangePassword()}>
          <KeyRound aria-hidden /> Сменить пароль
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem variant="destructive" onSelect={() => onLogout()}>
          <LogOut aria-hidden /> Выйти
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
