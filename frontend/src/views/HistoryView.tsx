import { useEffect, useState, type KeyboardEvent } from "react";
import { AlertCircle, Eye, List, Pencil, SlidersHorizontal, Trash2, Undo2, ChevronRight } from "lucide-react";
import { AgentService, type ConversationSummary, type JournalEntry } from "@/services";
import { useStore } from "@/store";
import { cn } from "@/lib/utils";
import { describeStep, kindOf } from "@/lib/steps";
import { Markdown } from "@/components/Markdown";

// Two panes: what was said, and what was done. Side by side because they
// answer each other, and a chat is only interesting alongside what it changed.
export function HistoryView({ active }: { active: boolean }) {
    const { conversations, refreshConversations } = useStore();
    const [entries, setEntries] = useState<JournalEntry[]>([]);

    // Fetched on every visit rather than kept: the journal changes with
    // every turn and a stale page of it would read as a lie.
    useEffect(() => {
        if (!active) return;
        let alive = true;
        // A Go slice with no elements crosses as null rather than [].
        AgentService.History().then((list) => { if (alive) setEntries(list ?? []); });
        refreshConversations();
        return () => { alive = false; };
    }, [active, refreshConversations]);

    return (
        <section className="settle flex h-full gap-6 px-5 pb-4">
            <div className="flex w-[clamp(220px,30%,340px)] flex-none flex-col gap-2">
                <h2 className="text-[15px] font-semibold tracking-tight">Chats</h2>
                <div className="flex min-h-0 flex-1 flex-col gap-1 overflow-y-auto">
                    {conversations.map((summary) => <Chip key={summary.ID} summary={summary} />)}
                </div>
            </div>

            <div className="flex min-w-0 flex-1 flex-col gap-2">
                <h2 className="text-[15px] font-semibold tracking-tight">Recent actions</h2>
                <div className="flex min-h-0 flex-1 flex-col gap-5 overflow-y-auto pr-1">
                    {entries.length === 0
                        ? <p className="text-[12.5px] text-faint">Nothing yet.</p>
                        : entries.map((entry, i) => <Turn key={i} entry={entry} />)}
                </div>
            </div>
        </section>
    );
}

// Rename and delete live on the row with explicit controls rather than a
// double-click nobody would guess at; shown on the row being pointed at,
// since a column of delete buttons reads as a column of hazards.
function Chip({ summary }: { summary: ConversationSummary }) {
    const { openConversation, renameConversation, deleteConversation } = useStore();
    const [editing, setEditing] = useState(false);
    const [draft, setDraft] = useState(summary.Title);

    const commit = async () => {
        setEditing(false);
        const chosen = draft.trim();
        if (chosen === "" || chosen === summary.Title) {
            setDraft(summary.Title);
            return;
        }
        await renameConversation(summary.ID, chosen);
    };

    const onKey = (event: KeyboardEvent<HTMLInputElement>) => {
        if (event.key === "Enter") commit();
        if (event.key === "Escape") { setDraft(summary.Title); setEditing(false); }
    };

    return (
        <div
            role="button"
            tabIndex={0}
            aria-pressed={summary.Active}
            onClick={() => { if (!editing) openConversation(summary.ID); }}
            onKeyDown={(event) => { if (event.key === "Enter" && !editing) openConversation(summary.ID); }}
            className={cn(
                "group flex cursor-pointer items-center gap-1.5 rounded-xl py-2 pr-2 pl-3 text-[13px] text-muted-foreground transition-colors hover:bg-card hover:text-foreground",
                summary.Active && "bg-card font-medium text-foreground",
            )}
        >
            {editing ? (
                <input
                    autoFocus
                    aria-label="Conversation name"
                    value={draft}
                    onChange={(event) => setDraft(event.target.value)}
                    onBlur={commit}
                    onKeyDown={onKey}
                    onClick={(event) => event.stopPropagation()}
                    className="min-w-0 flex-1 bg-transparent"
                />
            ) : (
                <span className="min-w-0 flex-1 truncate">{summary.Title}</span>
            )}
            <RowButton label={`Rename ${summary.Title}`} onClick={() => { setDraft(summary.Title); setEditing(true); }}>
                <Pencil className="size-3.5" />
            </RowButton>
            <RowButton label={`Delete ${summary.Title}`} onClick={() => deleteConversation(summary.ID)}>
                <Trash2 className="size-3.5" />
            </RowButton>
        </div>
    );
}

function RowButton({ label, onClick, children }: { label: string; onClick: () => void; children: React.ReactNode }) {
    return (
        <button
            type="button"
            aria-label={label}
            onClick={(event) => { event.stopPropagation(); onClick(); }}
            className="flex size-6 flex-none items-center justify-center rounded-full text-faint opacity-0 transition-opacity hover:bg-secondary hover:text-foreground group-hover:opacity-100 focus:opacity-100"
        >
            {children}
        </button>
    );
}

const icons = { list: List, read: Eye, set: SlidersHorizontal, undo: Undo2, other: SlidersHorizontal };

// Read as sentences, with the raw call behind a disclosure: someone reading
// their history wants to know what happened, and someone debugging wants
// the call. Showing the second to everyone is what made this unreadable.
function Turn({ entry }: { entry: JournalEntry }) {
    const steps = entry.Steps ?? [];
    return (
        <article className="flex flex-col gap-2">
            <div className="flex flex-wrap items-baseline gap-x-2.5 text-[13px]">
                <span className="tabular-nums text-faint">{entry.At}</span>
                <span className="font-medium">{entry.Command}</span>
                {entry.Preview && <span className="text-[12px] text-warning">proposed only</span>}
            </div>

            {steps.length > 0 && (
                <>
                    <ul className="flex flex-col gap-1 text-[13px] text-muted-foreground">
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
                : <Markdown text={entry.Answer} />}
        </article>
    );
}
