import { useEffect, useState } from "react";
import { AlertCircle, ChevronRight, Eye, List, SlidersHorizontal, Undo2, X } from "lucide-react";
import { AgentService, type JournalEntry } from "@/services";
import { useStore } from "@/store";
import { cn } from "@/lib/utils";
import { describeStep, kindOf } from "@/lib/steps";
import { Markdown } from "@/components/Markdown";
import { Button } from "@/components/ui/button";

// What the agent did, turn by turn, beside the thread it did it for. An
// agent that changes someone's project has to be answerable, and the
// model's own summary is the one account that cannot be checked.
export function ActionsPanel({ onClose }: { onClose: () => void }) {
    const { thread } = useStore();
    const [entries, setEntries] = useState<JournalEntry[]>([]);

    // Fetched whenever the thread changes: the journal grows with every
    // turn and a stale page of it would read as a lie.
    useEffect(() => {
        let alive = true;
        // A Go slice with no elements crosses as null rather than [].
        AgentService.History().then((list) => { if (alive) setEntries(list ?? []); });
        return () => { alive = false; };
    }, [thread]);

    return (
        // Beside the thread when there is room, over it when there is not:
        // squeezing both into a narrow window left neither readable.
        <aside aria-label="Recent actions" className="settle flex w-[22rem] max-w-full flex-none flex-col border-l border-border bg-background @max-3xl:absolute @max-3xl:inset-y-0 @max-3xl:right-0 @max-3xl:z-20 @max-3xl:shadow-lift">
            <div className="flex h-10 flex-none items-center justify-between pr-1 pl-4">
                <h2 className="text-sm font-medium">Recent actions</h2>
                <Button variant="ghost" size="icon-sm" aria-label="Close recent actions" onClick={onClose}><X /></Button>
            </div>
            <div className="flex min-h-0 flex-1 flex-col gap-5 overflow-y-auto px-4 pb-4">
                {entries.length === 0
                    ? <p className="text-[12.5px] text-faint">Nothing yet.</p>
                    : entries.map((entry, i) => <Turn key={i} entry={entry} />)}
            </div>
        </aside>
    );
}

const icons = { list: List, read: Eye, set: SlidersHorizontal, undo: Undo2, other: SlidersHorizontal };

// Read as sentences, with the raw call behind a disclosure: someone reading
// their history wants to know what happened, and someone debugging wants
// the call. Showing the second to everyone is what made this unreadable.
export function Turn({ entry }: { entry: JournalEntry }) {
    const steps = entry.Steps ?? [];
    return (
        <article className="flex flex-col gap-2 text-[13px]">
            <div className="flex flex-wrap items-baseline gap-x-2">
                <span className="tabular-nums text-faint">{entry.At}</span>
                <span className="font-medium">{entry.Command}</span>
                {entry.Preview && <span className="text-[12px] text-warning">proposed only</span>}
            </div>

            {steps.length > 0 && (
                <>
                    <ul className="flex flex-col gap-1 text-muted-foreground">
                        {steps.map((step, i) => {
                            const Icon = step.Failed ? AlertCircle : icons[kindOf(step.Tool)];
                            return (
                                <li key={i} className={cn("flex items-baseline gap-2", step.Failed && "text-destructive")}>
                                    <Icon className={cn("relative top-0.5 size-3.5 flex-none", step.Failed ? "text-destructive" : "text-faint")} aria-hidden />
                                    <span className="min-w-0 [overflow-wrap:anywhere]">{describeStep(step.Tool, step.Arguments, step.Outcome, step.Failed)}</span>
                                </li>
                            );
                        })}
                    </ul>
                    <details className="group/raw text-[12.5px] text-faint">
                        <summary className="flex cursor-pointer list-none items-center gap-1 [&::-webkit-details-marker]:hidden">
                            <ChevronRight className="size-3 transition-transform group-open/raw:rotate-90" aria-hidden />
                            Exact calls
                        </summary>
                        <pre className="mt-1.5 overflow-x-auto whitespace-pre-wrap rounded-xl bg-secondary p-3 font-mono text-[11.5px] leading-relaxed">
                            {steps.map((step) => `${step.Tool} ${step.Arguments}\n  ${step.Outcome}`).join("\n\n")}
                        </pre>
                    </details>
                </>
            )}

            {entry.Error
                ? <p className="text-destructive">{entry.Error.Code}: {entry.Error.Message}</p>
                : entry.Answer && <Markdown text={entry.Answer} className="text-muted-foreground" />}
        </article>
    );
}
