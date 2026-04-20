import { Outlet } from "react-router-dom"

import { Sidebar } from "@/components/layouts/sidebar"
import { Topbar } from "@/components/layouts/topbar"
import { ErrorBoundary } from "@/components/errors/error-boundary"

export function DashboardLayout() {
  return (
    <div className="bg-muted/30 flex min-h-svh">
      <Sidebar />
      <div className="flex min-w-0 flex-1 flex-col">
        <Topbar />
        <main className="flex-1 overflow-x-hidden p-4 md:p-6">
          <ErrorBoundary>
            <Outlet />
          </ErrorBoundary>
        </main>
      </div>
    </div>
  )
}
