import type { ReactNode } from "react";
import {
  Activity,
  Moon,
  Sun,
  FileClock,
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
import { useTheme } from "./ThemeProvider";

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
  const { setTheme, dark } = useTheme();
  return (
    <>
      <a className="skip-link" href="#main-content">
        К содержимому
      </a>
      <Sidebar collapsible="icon">
        <SidebarHeader className="sidebar-brand-row">
          <span className="panel-title group-data-[collapsible=icon]:hidden">
            Панель управления
          </span>

          <SidebarTrigger
            aria-label="Открыть или скрыть меню"
            aria-expanded={isMobile ? openMobile : open}
          />
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
                                tooltip={p}
                                isActive={page === p}
                                aria-current={page === p ? "page" : undefined}
                                onClick={() => {
                                  onNavigate(p);
                                  setOpenMobile(false);
                                }}
                              >
                                <Icon aria-hidden="true" />
                                <span>{p}</span>
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
        <SidebarFooter className="sidebar-footer border-t">
          <div className="flex min-w-0 items-center gap-1 group-data-[collapsible=icon]:flex-col">
            <div className="min-w-0 flex-1 group-data-[collapsible=icon]:hidden">
              <p className="truncate text-sm font-medium">{username}</p>
              <p className="text-xs text-muted-foreground">{role}</p>
            </div>
            <Button
              variant="ghost"
              size="icon"
              aria-label="Тёмная тема"
              title="Переключить тему"
              aria-pressed={dark}
              onClick={() => setTheme(dark ? "light" : "dark")}
            >
              <Sun className="hidden dark:block" aria-hidden="true" />
              <Moon className="dark:hidden" aria-hidden="true" />
            </Button>
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
        <header className="mobile-navigation">
          <SidebarTrigger
            aria-label="Открыть или скрыть меню"
            aria-expanded={openMobile}
          />
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
