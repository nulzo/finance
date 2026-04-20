import { AlertCircle } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"

interface Props {
  error: unknown
  title?: string
}

export function QueryError({ error, title = "Failed to load" }: Props) {
  const message =
    (error as { message?: string } | null)?.message ??
    (typeof error === "string" ? error : "Unknown error")
  return (
    <Alert variant="destructive">
      <AlertCircle className="size-4" />
      <AlertTitle>{title}</AlertTitle>
      <AlertDescription>{message}</AlertDescription>
    </Alert>
  )
}
