import { useState } from "react";
import { LogOut, KeyRound, Moon, Sun, UserRound, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import type { Session } from "../model";
import { useTheme } from "../ThemeProvider";
import { PasswordDialog } from "./PasswordDialog";

// Avatar with profile menu (account, password, sessions, theme, logout).
// Closes on outside click, Escape, or action. The avatar initial is the first letter of the username (Latin fallback "A").
export function ProfileMenu({
  session,
  onOpenAccount,
  onChangePassword,
  onLogout,
  onEndOtherSessions,
}: {
  session: Session;
  onOpenAccount: () => void;
  onChangePassword: (current: string, next: string) => Promise<void>;
  onLogout: () => void;
  onEndOtherSessions: () => void;
}) {
  const name = session.username || "admin";
  const role = session.role === "manager" ? "Менеджер" : "Администратор";
  const initial = (name[0] || "A").toUpperCase();
  const [pwdOpen, setPwdOpen] = useState(false);
  const { dark, setTheme } = useTheme();
  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            variant="ghost"
            size="icon"
            className="relative mt-1.5 mb-1 size-8 rounded-full border border-border p-0 hover:border-ring"
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
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align="end"
          side="right"
          className="w-[268px] p-1.5"
        >
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
          <DropdownMenuItem onSelect={() => onOpenAccount()}>
            <UserRound aria-hidden /> Мой аккаунт
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => setPwdOpen(true)}>
            <KeyRound aria-hidden /> Сменить пароль
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => onEndOtherSessions()}>
            <X aria-hidden /> Завершить другие сессии
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => setTheme(dark ? "light" : "dark")}>
            {dark ? <Sun aria-hidden /> : <Moon aria-hidden />}
            {dark ? "Светлая тема" : "Тёмная тема"}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem variant="destructive" onSelect={() => onLogout()}>
            <LogOut aria-hidden /> Выйти
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <PasswordDialog
        open={pwdOpen}
        onOpenChange={setPwdOpen}
        onSubmit={onChangePassword}
      />
    </>
  );
}
