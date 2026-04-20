import { Component, type ReactNode } from "react"

import { Button } from "@/components/ui/button"

interface Props {
  children: ReactNode
  fallback?: (error: Error, reset: () => void) => ReactNode
}

interface State {
  error: Error | null
}

export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  componentDidCatch(error: Error, info: unknown) {
    // Surface React errors to the browser console so devs can dig in.
    // Avoid shipping to a remote reporter from the default component.
    console.error("ErrorBoundary:", error, info)
  }

  reset = () => this.setState({ error: null })

  render() {
    const { error } = this.state
    if (!error) return this.props.children
    if (this.props.fallback) return this.props.fallback(error, this.reset)
    return (
      <div className="flex min-h-[50vh] flex-col items-center justify-center gap-4 p-6 text-center">
        <div>
          <h2 className="text-lg font-semibold">Something went wrong</h2>
          <p className="text-muted-foreground text-sm">{error.message}</p>
        </div>
        <Button onClick={this.reset}>Try again</Button>
      </div>
    )
  }
}
