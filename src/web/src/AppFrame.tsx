import type { ReactNode } from "react";
import {
  Activity,
  ArrowUpRight,
  FileClock,
  Globe2,
  KeyRound,
  LayoutDashboard,
  LogOut,
  Network,
  Settings2,
  ShieldCheck,
  Users,
} from "lucide-react";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarInset,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarProvider,
  SidebarTrigger,
  useSidebar,
} from "@/components/ui/sidebar";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";

const icons: Record<string, typeof Activity> = {
  Обзор: LayoutDashboard,
  Клиенты: Network,
  Настройки: Settings2,
  Изменения: FileClock,
  События: Activity,
  Аудит: ShieldCheck,
  Пользователи: Users,
  "Логины прокси": KeyRound,
};
const groups = [
  { label: "Прокси", pages: ["Обзор", "Клиенты", "События"] },
  { label: "Управление", pages: ["Настройки", "Изменения", "Аудит"] },
  { label: "Доступ", pages: ["Пользователи", "Логины прокси"] },
];
type Props = {
  navigation: string[];
  page: string;
  onNavigate: (page: string) => void;
  username: string;
  role: string;
  onLogout: () => void;
  busy: boolean;
  children: ReactNode;
};
function Frame({
  navigation,
  page,
  onNavigate,
  username,
  role,
  onLogout,
  busy,
  children,
}: Props) {
  const { setOpenMobile, isMobile, openMobile, open } = useSidebar();
  return (
    <>
      <a className="skip-link" href="#main-content">
        К содержимому
      </a>
      <Sidebar collapsible="offcanvas">
        <SidebarHeader className="px-5 py-6">
          <a href="/" className="console-brand">
            <span className="brand-mark">
              <Globe2 size={21} aria-hidden="true" />
            </span>
            <span>
              Squid Control<small>Панель управления</small>
            </span>
          </a>
        </SidebarHeader>
        <SidebarContent>
          <nav aria-label="Разделы панели">
            {groups.map((group) => {
              const pages = group.pages.filter((p) => navigation.includes(p));
              return (
                pages.length > 0 && (
                  <SidebarGroup key={group.label} className="px-3 pb-5">
                    <SidebarGroupLabel>{group.label}</SidebarGroupLabel>
                    <SidebarGroupContent>
                      <SidebarMenu>
                        {pages.map((p) => {
                          const Icon = icons[p];
                          return (
                            <SidebarMenuItem key={p}>
                              <SidebarMenuButton
                                isActive={page === p}
                                aria-current={page === p ? "page" : undefined}
                                onClick={() => {
                                  onNavigate(p);
                                  setOpenMobile(false);
                                }}
                              >
                                <Icon aria-hidden="true" />
                                <span>{p}</span>
                                {page === p && (
                                  <ArrowUpRight
                                    className="ml-auto !size-3.5 opacity-50"
                                    aria-hidden="true"
                                  />
                                )}
                              </SidebarMenuButton>
                            </SidebarMenuItem>
                          );
                        })}
                      </SidebarMenu>
                    </SidebarGroupContent>
                  </SidebarGroup>
                )
              );
            })}
          </nav>
        </SidebarContent>
        <SidebarFooter className="border-t p-4">
          <div className="flex min-w-0 items-center gap-3">
            <span className="user-avatar" aria-hidden="true">
              {username.slice(0, 1).toUpperCase()}
            </span>
            <div className="min-w-0 flex-1">
              <p className="truncate text-sm font-medium">{username}</p>
              <p className="text-xs text-muted-foreground">{role}</p>
            </div>
            <Button
              variant="ghost"
              size="icon"
              onClick={onLogout}
              disabled={busy}
              aria-label="Выйти"
            >
              <LogOut aria-hidden="true" />
            </Button>
          </div>
        </SidebarFooter>
      </Sidebar>
      <SidebarInset className="min-w-0 bg-background">
        <header className="console-toolbar">
          <SidebarTrigger
            aria-label="Открыть или скрыть меню"
            aria-expanded={isMobile ? openMobile : open}
          />
          <Separator orientation="vertical" className="!h-4" />
          <span>Управление прокси</span>
          <span className="ml-auto text-xs">Squid Control</span>
        </header>
        {children}
      </SidebarInset>
    </>
  );
}
export function AppFrame(props: Props) {
  return (
    <SidebarProvider
      style={{ "--sidebar-width": "15rem" } as React.CSSProperties}
    >
      <Frame {...props} />
    </SidebarProvider>
  );
}
