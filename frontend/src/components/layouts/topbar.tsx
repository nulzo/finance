import { useEffect, useState } from "react"
import { useLocation, useNavigate } from "react-router-dom"
import {
  BookOpenText,
  Command as CommandIcon,
  Moon,
  PanelLeft,
  Search,
  Sun,
} from "lucide-react"
import { useTheme } from "@/components/theme-provider"
import { Button } from "@/components/ui/button"
import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command"
import { Kbd } from "@/components/ui/kbd"
import { paths } from "@/config/paths"
import { useUIStore } from "@/stores/ui"

export function Topbar() {
  const toggleSidebar = useUIStore((s) => s.toggleSidebar)
  const { theme, setTheme } = useTheme()
  const isDark =
    theme === "dark" ||
    (theme === "system" &&
      typeof window !== "undefined" &&
      document.documentElement.classList.contains("dark"))
  const [open, setOpen] = useState(false)
  const navigate = useNavigate()
  const location = useLocation()

  useEffect(() => {
    const down = (e: KeyboardEvent) => {
      if ((e.key === "k" && (e.metaKey || e.ctrlKey)) || e.key === "/") {
        e.preventDefault()
        setOpen((v) => !v)
      }
    }
    document.addEventListener("keydown", down)
    return () => document.removeEventListener("keydown", down)
  }, [])

  const crumb = humanizePath(location.pathname)

  return (
    <header className="bg-background/80 sticky top-0 z-20 flex h-14 items-center gap-2 border-b px-4 backdrop-blur">
      <Button
        variant="ghost"
        size="icon-sm"
        onClick={toggleSidebar}
        aria-label="Toggle sidebar"
      >
        <PanelLeft />
      </Button>
      <div className="text-sm font-medium">{crumb}</div>

      <div className="ml-auto flex items-center gap-2">
        <Button
          variant="outline"
          size="sm"
          onClick={() => setOpen(true)}
          className="text-muted-foreground w-[220px] justify-between"
        >
          <span className="flex items-center gap-2">
            <Search className="size-3.5" /> Search…
          </span>
          <Kbd>⌘K</Kbd>
        </Button>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Toggle theme"
          onClick={() => setTheme(isDark ? "light" : "dark")}
        >
          {isDark ? <Sun /> : <Moon />}
        </Button>
      </div>

      <CommandDialog open={open} onOpenChange={setOpen}>
        <CommandInput placeholder="Jump to…" />
        <CommandList>
          <CommandEmpty>No results.</CommandEmpty>
          <CommandGroup heading="Navigation">
            {Object.entries(paths).map(([key, p]) => {
              if ("path" in p && !p.path.includes(":")) {
                return (
                  <CommandItem
                    key={key}
                    value={key}
                    onSelect={() => {
                      setOpen(false)
                      navigate(p.path)
                    }}
                  >
                    <CommandIcon className="mr-2 size-3.5" />
                    <span className="capitalize">{key.replace(/([A-Z])/g, " $1")}</span>
                  </CommandItem>
                )
              }
              return null
            })}
          </CommandGroup>
          <CommandGroup heading="Docs">
            <CommandItem
              onSelect={() =>
                window.open("https://github.com/alan2207/bulletproof-react", "_blank")
              }
            >
              <BookOpenText className="mr-2 size-3.5" />
              bulletproof-react
            </CommandItem>
          </CommandGroup>
        </CommandList>
      </CommandDialog>
    </header>
  )
}

function humanizePath(p: string): string {
  if (p === "/" || p === "") return "Overview"
  const parts = p.replace(/^\//, "").split("/").filter(Boolean)
  return parts
    .map((s) => s.replace(/-/g, " "))
    .map((s) => s.charAt(0).toUpperCase() + s.slice(1))
    .join(" / ")
}
