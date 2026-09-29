import { TriangleAlertIcon } from "lucide-react";
import { Component, type ErrorInfo, type ReactNode } from "react";

import { Button } from "@/components/ui/button";
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty";

type Props = { children: ReactNode };
type State = { error?: Error; stack?: string };

// ErrorBoundary keeps a render that throws from unmounting the whole app.
// Without it React replaces the tree with nothing, so the window goes white
// and the one thing worth knowing - what threw - is left in a console the
// person looking at it has no way to open: release builds ship without
// devtools. Showing the error on screen is what makes a report from someone
// else's machine actionable.
export class ErrorBoundary extends Component<Props, State> {
  state: State = {};

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    this.setState({ error, stack: info.componentStack ?? undefined });
    // Kept for anyone who does have a console attached, such as a dev build.
    console.error("Calport hit an error it could not render through:", error, info.componentStack);
  }

  render() {
    const { error, stack } = this.state;
    if (!error) return this.props.children;
    const detail = [error.stack || `${error.name}: ${error.message}`, stack && `Component stack:${stack}`].filter(Boolean).join("\n\n");
    return (
      <main className="flex min-h-svh items-center justify-center p-6">
        <Empty className="max-w-3xl">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <TriangleAlertIcon />
            </EmptyMedia>
            <EmptyTitle>Calport hit an error</EmptyTitle>
            <EmptyDescription>{error.message || String(error)}</EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <pre className="max-h-64 w-full overflow-auto rounded-md bg-muted p-3 text-left text-xs leading-relaxed whitespace-pre-wrap select-text">{detail}</pre>
            <div className="flex gap-2">
              <Button onClick={() => this.setState({})}>Try again</Button>
              <Button variant="outline" onClick={() => navigator.clipboard?.writeText(detail)}>
                Copy details
              </Button>
              <Button variant="ghost" onClick={() => location.reload()}>
                Reload
              </Button>
            </div>
          </EmptyContent>
        </Empty>
      </main>
    );
  }
}
