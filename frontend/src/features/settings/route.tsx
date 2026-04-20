import { useState } from "react"
import { toast } from "sonner"

import { PageHeader } from "@/components/layouts/page-header"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { config } from "@/config/env"
import { useAuthStore } from "@/stores/auth"

export function SettingsRoute() {
  const token = useAuthStore((s) => s.token)
  const setToken = useAuthStore((s) => s.setToken)
  const clear = useAuthStore((s) => s.clearToken)
  const [local, setLocal] = useState(token)

  return (
    <div className="flex flex-col gap-4">
      <PageHeader title="Settings" description="Local dashboard configuration." />
      <Card>
        <CardHeader>
          <CardTitle>API token</CardTitle>
          <CardDescription>
            Sent as Bearer on every request to the trader API. Persisted in
            localStorage. The backend reads <code>API_TOKEN</code> at startup —
            leave empty in dev if the server is unauthenticated.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          <div className="flex flex-col gap-1">
            <Label htmlFor="tok">Token</Label>
            <Input
              id="tok"
              type="password"
              value={local}
              onChange={(e) => setLocal(e.target.value)}
              placeholder="Bearer token"
            />
          </div>
          <div className="flex gap-2">
            <Button
              size="sm"
              onClick={() => {
                setToken(local)
                toast.success("API token saved")
              }}
            >
              Save
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={() => {
                clear()
                setLocal("")
                toast.success("API token cleared")
              }}
            >
              Clear
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Runtime</CardTitle>
          <CardDescription>Values from Vite env.</CardDescription>
        </CardHeader>
        <CardContent>
          <pre className="bg-muted/40 overflow-auto rounded-lg p-3 font-mono text-xs">
            {JSON.stringify(config, null, 2)}
          </pre>
        </CardContent>
      </Card>
    </div>
  )
}
