import { useEffect, useState } from "react";
import { Bell, Search, SunMoon, UserRound } from "lucide-react";
import { Link, useNavigate } from "react-router-dom";
import { useVisibleRoutes } from "@/app/useVisibleRoutes";
import { useGet, useWrite } from "@/hooks/useApi";
import { formatDateTime } from "@/lib/formatters";
import { useAuth } from "@/features/auth/auth";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
export function SearchPalette() {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const navigate = useNavigate();
  const routes = useVisibleRoutes();
  useEffect(() => {
    const listener = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setOpen(true);
      }
    };
    window.addEventListener("keydown", listener);
    return () => window.removeEventListener("keydown", listener);
  }, []);
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="icon" variant="ghost" aria-label="Search pages">
          <Search size={19} />
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogTitle>Search pages</DialogTitle>
        <DialogDescription>
          Find a page by name or press Control K to open search.
        </DialogDescription>
        <Input
          autoFocus
          aria-label="Search pages"
          placeholder="Search pages…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
        <ul className="max-h-64 overflow-auto">
          {routes
            .filter((route) => route.navigation)
            .filter((route) => route.label.toLowerCase().includes(query.toLowerCase()))
            .map((route) => (
              <li key={route.path}>
                <button
                  className="w-full rounded p-3 text-left hover:bg-muted focus-visible:bg-muted"
                  onClick={() => {
                    setOpen(false);
                    setQuery("");
                    navigate(route.path);
                  }}
                >
                  {route.label}
                </button>
              </li>
            ))}
        </ul>
      </DialogContent>
    </Dialog>
  );
}
interface Notice {
  id: string;
  title?: string;
  body?: string;
  message?: string;
  is_read?: boolean;
  read_at?: string | null;
  created_at?: string;
}
export function NotificationBell() {
  const { data } = useGet<Notice[]>("/notifications");
  const readAll = useWrite("POST", "/notifications/read-all", { invalidate: ["/notifications"] });
  const items = data ?? [];
  const unread = items.filter((n) => !(n.is_read ?? Boolean(n.read_at))).length;
  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          aria-label={`Notifications, ${unread} unread`}
          className="relative"
        >
          <Bell size={19} />
          {unread > 0 && (
            <span
              className="absolute right-1 top-1 h-2 w-2 rounded-full bg-destructive"
              aria-hidden="true"
            />
          )}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-80">
        <h2 className="font-semibold">Notifications</h2>
        {items.length === 0 ? (
          <p className="mt-2 text-sm text-muted-foreground">You are all caught up.</p>
        ) : (
          <ul className="mt-2 max-h-72 space-y-2 overflow-auto text-sm">
            {items.slice(0, 20).map((n) => (
              <li key={n.id} className="rounded-md border p-2">
                <p className="font-medium">{n.title ?? n.message}</p>
                {n.body && <p className="text-muted-foreground">{n.body}</p>}
                <p className="mt-1 text-xs text-muted-foreground">{formatDateTime(n.created_at)}</p>
              </li>
            ))}
          </ul>
        )}
        {unread > 0 && (
          <Button
            variant="outline"
            size="sm"
            className="mt-3"
            onClick={() => readAll.mutate(undefined)}
          >
            Mark all read
          </Button>
        )}
      </PopoverContent>
    </Popover>
  );
}
export function ThemeToggle() {
  const [theme, setTheme] = useState(() => localStorage.getItem("theme") ?? "system");
  useEffect(() => {
    const update = () => setTheme(localStorage.getItem("theme") ?? "system");
    window.addEventListener("themechange", update);
    return () => window.removeEventListener("themechange", update);
  }, []);
  const toggle = () => {
    const next = theme === "system" ? "light" : theme === "light" ? "dark" : "system";
    localStorage.setItem("theme", next);
    setTheme(next);
    window.dispatchEvent(new Event("themechange"));
  };
  return (
    <Button
      variant="ghost"
      size="icon"
      onClick={toggle}
      aria-label={`Theme: ${theme}. Change theme`}
    >
      <SunMoon size={19} />
    </Button>
  );
}
export function UserMenu() {
  const { user, logout } = useAuth();
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" aria-label="Open user menu">
          <UserRound size={19} />
          <span className="hidden sm:inline">{user?.name}</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem asChild>
          <Link to="/profile">My account</Link>
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => void logout()}>Sign out</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
