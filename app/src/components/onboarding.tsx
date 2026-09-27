import { BotIcon, CheckIcon, GlobeLockIcon, ServerIcon, ShieldCheckIcon } from "lucide-react";
import { useState } from "react";

import { CheckList } from "@/components/check-list";
import { ConnectBox } from "@/components/connect-box";
import { ToolIntegrations } from "@/components/tool-integrations";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardDescription, CardHeader, CardPanel, CardTitle } from "@/components/ui/card";
import { Spinner } from "@/components/ui/spinner";
import { toastManager } from "@/components/ui/toast";
import { useLoad } from "@/hooks/use-calport";
import { calport, type Network } from "@/lib/calport";
import { cn } from "@/lib/utils";

const steps = ["Welcome", "Stay running", "Connect a box", "Orca and agents", "Check"] as const;

interface OnboardingProps {
  networks: Network[];
  onNetworksChanged: () => void;
  onBoxConnected: (box: string) => void;
  onFinish: () => void;
}

export function Onboarding({ networks, onNetworksChanged, onBoxConnected, onFinish }: OnboardingProps) {
  const [step, setStep] = useState(0);
  return (
    <main className="flex min-h-svh flex-col items-center overflow-y-auto bg-muted/30 px-6 py-12">
      <div className="flex w-full max-w-xl flex-col gap-8">
        <ol className="flex items-center gap-2" aria-label="Setup progress">
          {steps.map((label, i) => (
            <li key={label} className="flex flex-1 flex-col gap-1.5" aria-current={i === step ? "step" : undefined}>
              <span className={cn("h-1 rounded-full", i <= step ? "bg-primary" : "bg-border")} />
              <span className={cn("text-xs", i === step ? "font-medium text-foreground" : "text-muted-foreground")}>{label}</span>
            </li>
          ))}
        </ol>

        {step === 0 && <Welcome onNext={() => setStep(1)} />}
        {step === 1 && <StayRunning onNext={() => setStep(2)} />}
        {step === 2 && (
          <section className="flex flex-col gap-6">
            <header className="flex flex-col gap-2">
              <h1 className="font-heading font-semibold text-2xl">Connect your first box</h1>
              <p className="text-muted-foreground">
                A box is any Linux machine where your code runs: a VPS, a dev server, a spare machine. Calport puts a
                small daemon, calportd, on it. It has no dependencies, starts at boot, and only answers laptops you
                pair.
              </p>
            </header>
            <ConnectBox
              networks={networks}
              onNetworksChanged={onNetworksChanged}
              onConnected={(box) => {
                onBoxConnected(box);
                setStep(3);
              }}
            />
            <div>
              <Button variant="ghost" onClick={() => setStep(3)}>
                Skip for now
              </Button>
            </div>
          </section>
        )}
        {step === 3 && <OrcaAndAgents onNext={() => setStep(4)} />}
        {step === 4 && <FinalCheck onFinish={onFinish} />}
      </div>
    </main>
  );
}

function Welcome({ onNext }: { onNext: () => void }) {
  const points = [
    { icon: ServerIcon, title: "Your boxes, one place", text: "Pair any VPS or dev machine, on a tailnet or not." },
    { icon: GlobeLockIcon, title: "Private by default", text: "Every dev server gets a private URL on this laptop. Nothing is public unless you share it." },
    { icon: BotIcon, title: "A URL for every worktree", text: "fix-login.cal.devl.localhost opens that worktree's dev server. Open it in Orca or Herdr with an agent." },
  ];
  return (
    <section className="flex flex-col gap-8">
      <header className="flex flex-col gap-2">
        <h1 className="font-heading font-semibold text-3xl">Welcome to Calport</h1>
        <p className="text-muted-foreground">Connect your development boxes to this laptop. Setup takes about two minutes.</p>
      </header>
      <ul className="flex flex-col gap-5">
        {points.map(({ icon: Icon, title, text }) => (
          <li key={title} className="flex gap-4">
            <span className="flex size-10 shrink-0 items-center justify-center rounded-lg border bg-background">
              <Icon className="size-5" />
            </span>
            <span className="flex flex-col gap-0.5">
              <span className="font-medium">{title}</span>
              <span className="text-muted-foreground text-sm">{text}</span>
            </span>
          </li>
        ))}
      </ul>
      <div>
        <Button size="lg" onClick={onNext}>
          Get started
        </Button>
      </div>
    </section>
  );
}

function StayRunning({ onNext }: { onNext: () => void }) {
  const status = useLoad(() => calport.agentStatus(), []);
  const [busy, setBusy] = useState(false);
  const installed = status.data?.installed;
  return (
    <section className="flex flex-col gap-6">
      <header className="flex flex-col gap-2">
        <h1 className="font-heading font-semibold text-2xl">Keep Calport running</h1>
        <p className="text-muted-foreground">
          Calport keeps your connections, forwards, and private URLs alive in the background, even when this window is
          closed. Let it start when you log in and restart itself if it ever crashes.
        </p>
      </header>
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <ShieldCheckIcon className="size-4" />
            Background agent
          </CardTitle>
          <CardDescription>Runs as your user only. Stop it any time from the menu.</CardDescription>
        </CardHeader>
        <CardPanel className="flex items-center justify-between gap-4">
          {status.loading && !status.data ? (
            <Spinner />
          ) : installed ? (
            <Badge variant="success">
              <CheckIcon />
              Starts at login
            </Badge>
          ) : (
            <Badge variant="secondary">Only while the app is open</Badge>
          )}
          {!installed && (
            <Button
              disabled={busy}
              onClick={async () => {
                setBusy(true);
                try {
                  await calport.installAgent();
                  status.reload();
                } catch (err) {
                  toastManager.add({ title: "Could not install the agent", description: String(err), type: "error" });
                } finally {
                  setBusy(false);
                }
              }}
            >
              {busy && <Spinner />}
              Start at login
            </Button>
          )}
        </CardPanel>
      </Card>
      <div className="flex gap-2">
        <Button onClick={onNext}>Continue</Button>
      </div>
    </section>
  );
}

function OrcaAndAgents({ onNext }: { onNext: () => void }) {
  return (
    <section className="flex flex-col gap-6">
      <header className="flex flex-col gap-2">
        <h1 className="font-heading font-semibold text-2xl">Orca and your agents</h1>
        <p className="text-muted-foreground">Calport works alongside the tools you already use. Nothing here is required.</p>
      </header>
      <Card>
        <CardHeader>
          <CardTitle>Orca on a box</CardTitle>
          <CardDescription>
            Calport reads the projects Orca knows and their setup and archive scripts. Import them from a box's
            Worktrees tab. Worktrees you create there with Orca run Orca's setup, just as they do in Orca itself, and
            open in Orca with one click. Nothing in Orca's settings needs to change.
          </CardDescription>
        </CardHeader>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Scripts without Orca</CardTitle>
          <CardDescription>
            Worktrees made with git or Herdr run the same scripts, with Orca's environment variables, so an Orca setup
            script works unchanged. Set them per location from its menu.
          </CardDescription>
        </CardHeader>
      </Card>
      <div className="flex flex-col gap-3">
        <h2 className="font-medium">Agents on this computer</h2>
        <ToolIntegrations />
      </div>
      <div>
        <Button onClick={onNext}>Continue</Button>
      </div>
    </section>
  );
}

function FinalCheck({ onFinish }: { onFinish: () => void }) {
  const checks = useLoad(() => calport.doctor(), []);
  const problems = (checks.data ?? []).filter((c) => c.status === "warn" || c.status === "fail").length;
  return (
    <section className="flex flex-col gap-6">
      <header className="flex flex-col gap-2">
        <h1 className="font-heading font-semibold text-2xl">Check everything</h1>
        <p className="text-muted-foreground">
          What Calport sees on this computer. Run these again any time from Settings, and check a box from its menu.
        </p>
      </header>
      {checks.loading && !checks.data ? (
        <Spinner />
      ) : checks.error ? (
        <p className="text-destructive-foreground text-sm">{checks.error}</p>
      ) : (
        <CheckList checks={checks.data ?? []} />
      )}
      <div className="flex gap-2">
        <Button size="lg" onClick={onFinish}>
          {problems > 0 ? "Finish, fix later" : "Finish"}
        </Button>
        <Button size="lg" variant="ghost" onClick={checks.reload}>
          Check again
        </Button>
      </div>
    </section>
  );
}
