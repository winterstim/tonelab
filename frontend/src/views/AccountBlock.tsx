import { useCallback, useEffect, useRef, useState } from "react";
import { HostedService, type HostedStatus, type SignInState, type UsageWindow } from "@/services";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Progress } from "@/components/ui/progress";
import { Group, Hint } from "@/views/SettingsView";

// Three faces: signed out, waiting for the browser, signed in. Which one
// shows is the backend's answer, never a guess made here, so a reload
// lands on the truth.
export function AccountBlock({ active, onModels, onChanged }: {
    active: boolean;
    onModels: (names: string[]) => void;
    onChanged: () => void;
}) {
    const [status, setStatus] = useState<HostedStatus | null>(null);
    const [pending, setPending] = useState<SignInState | null>(null);
    const [note, setNote] = useState("");
    const timer = useRef<number | undefined>(undefined);

    const load = useCallback(async () => {
        const read = await HostedService.Status();
        setStatus(read);
        onModels(read.Models ?? []);
    }, [onModels]);

    useEffect(() => {
        // The state arrives from the backend asynchronously, which is what
        // effects are for; the rule sees only a setter behind a call.
        // eslint-disable-next-line react-hooks/set-state-in-effect
        if (active) load();
    }, [active, load]);

    // The browser leg can take minutes; the window asks every two seconds
    // whether it is done rather than holding a call open.
    const watch = useCallback(async () => {
        const state = await HostedService.State();
        if (state.Running) {
            setPending(state);
            return;
        }
        window.clearInterval(timer.current);
        timer.current = undefined;
        setPending(null);
        if (state.Error) {
            setNote(state.Error);
        } else {
            await load();
            // Signing in adopts the hosted endpoint into the settings.
            if (state.Done) onChanged();
        }
    }, [load, onChanged]);

    useEffect(() => () => window.clearInterval(timer.current), []);

    const signIn = async () => {
        setNote("");
        const state = await HostedService.SignIn("");
        if (state.Error) {
            setNote(state.Error);
            return;
        }
        await watch();
        timer.current = window.setInterval(watch, 2000);
    };

    const cancel = async () => {
        await HostedService.Cancel();
        await watch();
    };

    const signOut = async () => {
        await HostedService.SignOut();
        await load();
        onChanged();
    };

    if (!status) return null;

    return (
        <Group title="Tonelab account">
            <div aria-live="polite" className="flex flex-col gap-2.5">
                {pending ? (
                    <>
                        <p>Approve this device in the browser. The code shown there must be</p>
                        <p className="font-mono text-2xl font-semibold tracking-[0.2em]">{pending.UserCode}</p>
                        <Hint>{pending.VerifyURL}</Hint>
                        <div><Button type="button" variant="secondary" className="rounded-full" onClick={cancel}>Cancel</Button></div>
                    </>
                ) : status.SignedIn ? (
                    <>
                        <p>
                            {status.Email || "Signed in"}{" "}
                            <span className="text-faint">{status.Plan ? `on the ${status.Plan} plan` : "no plan yet"}</span>
                        </p>
                        {status.Active && (
                            <div className="flex flex-col gap-2">
                                <Meter label="This month" window={status.Month} />
                                <Meter label="Today" window={status.Day} />
                                <Meter label="Searches" window={status.Searches} />
                            </div>
                        )}
                        {(status.Error || (!status.Active && status.Reason)) && <Hint>{status.Error || status.Reason}</Hint>}
                        <div><Button type="button" variant="secondary" className="rounded-full" onClick={signOut}>Sign out</Button></div>
                    </>
                ) : (
                    <>
                        <Hint>Sign in to use the hosted model and web search on a subscription, with nothing to set up. Your own endpoint below stays available either way.</Hint>
                        <div className="flex items-center gap-3">
                            <Button type="button" className="rounded-full" onClick={signIn}>Sign in with Tonelab</Button>
                            {note && <span role="status" className="text-[12.5px] text-destructive">{note}</span>}
                        </div>
                    </>
                )}
            </div>
        </Group>
    );
}

export function whenResets(at: string, now = Date.now()): string {
    const ms = new Date(at).getTime() - now;
    if (!Number.isFinite(ms) || ms <= 0) return "resets now";
    const hours = Math.round(ms / 3600000);
    if (hours < 48) return `resets in ${Math.max(1, hours)} h`;
    return `resets on ${new Date(at).toLocaleDateString(undefined, { day: "numeric", month: "long" })}`;
}

// The bar changes colour with how full it is, since the number alone is
// read only after the colour has already said it.
function Meter({ label, window }: { label: string; window: UsageWindow }) {
    const share = window.limit > 0 ? window.used / window.limit : 0;
    const percent = Math.min(100, Math.round(share * 100));
    const level = share >= 1 ? "full" : share >= 0.8 ? "high" : "ok";
    return (
        <div className="grid grid-cols-[7rem_1fr_auto] items-center gap-3 text-[13px]" data-level={level}>
            <span>{label}</span>
            <Progress value={percent} aria-label={label} className="bg-secondary" indicatorClassName={cn(level === "full" ? "bg-destructive" : level === "high" ? "bg-warning" : "bg-success")} />
            <span className="tabular-nums text-faint">{percent}%, {whenResets(window.resets_at)}</span>
        </div>
    );
}
