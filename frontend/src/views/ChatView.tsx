import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import { ArrowUp, Square, Undo2, Plus, MessageSquare, ChevronDown } from "lucide-react";
import { AgentService, type AgentError, type AgentResponse, type ParamChange, type PlannedCall } from "@/services";
import { isQuotaRefusal, QuotaCard } from "@/components/QuotaCard";
import { useStore } from "@/store";
import { cn } from "@/lib/utils";
import { explain, format } from "@/lib/format";
import { Markdown } from "@/components/Markdown";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";

type Tone = "answer" | "problem" | "working";

interface Item {
    key: number;
    from: "you" | "tonelab";
    text: string;
    tone: Tone;
    changed?: ParamChange[] | null;
    plan?: PlannedCall[] | null;
    quota?: AgentError;
}

// An empty screen is the one place with room for a sentence rather than
// instructions. Chosen by the hour, because the same line every morning
// stops being read after the second day.
const greetings: Record<string, string[]> = {
    night: ["Let's make something at this hour.", "The quiet part of the day.", "Still going. Good."],
    morning: ["Fresh ears this morning.", "Let's hear it.", "Start where you left off."],
    afternoon: ["What are we shaping today?", "Let's get into it.", "Tell me what to move."],
    evening: ["Let's create through your night.", "The good hours.", "What needs fixing tonight?"],
};

function greet(): string {
    const hour = new Date().getHours();
    const part = hour < 5 ? "night" : hour < 12 ? "morning" : hour < 18 ? "afternoon" : "evening";
    const lines = greetings[part];
    return lines[Math.floor(Math.random() * lines.length)];
}

let keys = 0;

// A refusal for want of quota becomes a card with the reset time; any
// other failure is a line saying what to do next.
function problem(error: AgentError): Item {
    if (isQuotaRefusal(error)) return { key: ++keys, from: "tonelab", text: "", tone: "problem", quota: error };
    return { key: ++keys, from: "tonelab", text: explain(error.Code, error.Message), tone: "problem" };
}

export function ChatView({ active, onAccount }: { active: boolean; onAccount: () => void }) {
    const { settings, thread, generation, conversations, refreshConversations, openConversation, startConversation } = useStore();
    // The thread lives in the backend; what is drawn is that thread plus
    // whatever this view has added since the store last handed one over
    // (a command just sent, a "Working…" line, a plan). The additions are
    // tied to the generation they were made in, so a redraw drops them.
    const base = useMemo<Item[]>(() => {
        const drawn: Item[] = [];
        for (const message of thread.messages) {
            if (message.Error) {
                drawn.push(problem(message.Error));
                continue;
            }
            // Plans are not redrawn: a plan is an offer made once, and one
            // reopened hours later would invite accepting something stale.
            drawn.push({ key: ++keys, from: message.From as Item["from"], text: message.Text || "Done.", tone: "answer", changed: message.Changed });
        }
        return drawn;
    }, [thread]);
    const [extras, setExtras] = useState<{ generation: number; list: Item[] }>({ generation: 0, list: [] });
    const items = extras.generation === generation ? [...base, ...extras.list] : base;
    // A new line each time the room empties; generation is the trigger.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    const greeting = useMemo(() => greet(), [generation]);

    const [running, setRunning] = useState(false);
    // The toggle starts from the saved default and follows it when the
    // settings are saved again, but a choice made here holds until then.
    const [chosen, setChosen] = useState<{ of: unknown; value: boolean } | null>(null);
    const preview = chosen && chosen.of === settings ? chosen.value : (settings?.PreviewByDefault ?? false);
    const setPreview = (value: boolean) => setChosen({ of: settings, value });
    const [text, setText] = useState("");
    const input = useRef<HTMLTextAreaElement>(null);
    const scroller = useRef<HTMLDivElement>(null);

    useEffect(() => {
        if (active) input.current?.focus();
    }, [active]);

    useEffect(() => {
        const box = scroller.current;
        if (box) box.scrollTop = box.scrollHeight;
    }, [items.length]);

    const append = useCallback((item: Omit<Item, "key">): number => {
        const key = ++keys;
        setExtras((held) => ({ generation, list: [...(held.generation === generation ? held.list : []), { ...item, key }] }));
        return key;
    }, [generation]);

    const remove = useCallback((key: number) => {
        setExtras((held) => ({ ...held, list: held.list.filter((item) => item.key !== key) }));
    }, []);

    const report = useCallback((response: AgentResponse) => {
        if (response.Error) {
            append(problem(response.Error));
            return;
        }
        append({ from: "tonelab", text: response.Message || "Done.", tone: "answer", changed: response.Changed, plan: response.Plan });
    }, [append]);

    const submit = async () => {
        const command = text.trim();
        if (command === "" || running) return;

        append({ from: "you", text: command, tone: "answer" });
        setText("");
        setRunning(true);
        const waiting = append({ from: "tonelab", text: "Working…", tone: "working" });
        const asked = thread.id;

        try {
            const response = preview
                ? await AgentService.PreviewCommand(command)
                : await AgentService.SendCommand(command);
            remove(waiting);
            // Drawn only if the window is still on the conversation that
            // asked. The answer is kept either way, waiting in that thread.
            if (response.Conversation === "" || response.Conversation === asked) report(response);
        } catch (error) {
            remove(waiting);
            // Reaching here means the call itself broke, rather than the
            // command failing, which the backend reports inside the response.
            const reason = (error instanceof Error ? error.message : String(error)).trim();
            append({ from: "tonelab", tone: "problem", text: reason
                ? `The backend could not be reached. ${reason}`
                : "The backend could not be reached. Restart the app if this persists." });
        } finally {
            setRunning(false);
            input.current?.focus();
            refreshConversations();
        }
    };

    const onSubmit = (event: FormEvent) => {
        event.preventDefault();
        submit();
    };

    // Enter sends, Shift+Enter makes a new line, which is what a text box
    // in a chat is expected to do.
    const onKey = (event: KeyboardEvent<HTMLTextAreaElement>) => {
        if (event.key === "Enter" && !event.shiftKey) {
            event.preventDefault();
            submit();
        }
    };

    const resize = () => {
        const box = input.current;
        if (!box) return;
        box.style.height = "auto";
        box.style.height = `${Math.min(box.scrollHeight, 160)}px`;
    };

    const applyPlan = async (key: number) => {
        setExtras((held) => ({ ...held, list: held.list.map((item) => (item.key === key ? { ...item, plan: null } : item)) }));
        report(await AgentService.ApplyPlan());
    };

    const activeTitle = conversations.find((c) => c.Active)?.Title ?? "New conversation";

    return (
        <section className="flex h-full flex-col px-5 pb-4">
            <div ref={scroller} className="flex min-h-0 flex-1 flex-col gap-5 overflow-y-auto py-4" data-thread={thread.id}>
                {items.length === 0 ? (
                    <div className="settle m-auto max-w-[22ch] text-center">
                        <p className="text-3xl font-semibold leading-tight tracking-tight">{greeting}</p>
                    </div>
                ) : items.map((item) => (
                    <Message key={item.key} item={item} onApply={() => applyPlan(item.key)} onAccount={onAccount} />
                ))}
            </div>

            <form onSubmit={onSubmit} className="flex items-end gap-2 rounded-[26px] bg-background py-1.5 pr-1.5 pl-4 shadow-lift dark:bg-card">
                <textarea
                    ref={input}
                    rows={1}
                    value={text}
                    onChange={(event) => { setText(event.target.value); resize(); }}
                    onKeyDown={onKey}
                    placeholder="Ask for a change"
                    aria-label="Command"
                    className="max-h-40 flex-1 resize-none bg-transparent py-1.5 text-[14.5px] leading-6 placeholder:text-faint"
                />
                {/* Stop replaces Send while a turn runs, so the button under
                    the cursor is always the one that applies. */}
                {running ? (
                    <Button type="button" size="icon" className="rounded-full" aria-label="Stop the agent" onClick={() => AgentService.Stop()}>
                        <Square />
                    </Button>
                ) : (
                    <Button type="submit" size="icon" className="rounded-full" aria-label="Send">
                        <ArrowUp />
                    </Button>
                )}
            </form>

            <div className="mt-2 flex flex-wrap items-center gap-x-1 gap-y-1 text-[13px] text-muted-foreground">
                {/* Not wrapped in the label: a label forwards its click to
                    the control inside it, which toggled the switch twice. */}
                <div className="flex items-center gap-2 rounded-full px-2.5 py-1 hover:text-foreground">
                    <Switch id="preview-mode" checked={preview} onCheckedChange={setPreview} />
                    <label htmlFor="preview-mode" className="cursor-pointer">Show me the plan first</label>
                </div>
                <Quiet onClick={async () => report(await AgentService.Undo())}>
                    <Undo2 className="size-3.5" /> Undo last change
                </Quiet>

                {/* Switching lives here rather than in a strip above the
                    thread: frequent enough that leaving the chat for it
                    would be a tax, rare enough not to deserve a row. */}
                {conversations.length >= 2 && (
                    <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                            <Quiet>
                                <MessageSquare className="size-3.5" />
                                <span className="max-w-48 truncate">{activeTitle}</span>
                                <ChevronDown className="size-3.5" />
                            </Quiet>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent side="top" align="start">
                            {conversations.map((summary) => (
                                <DropdownMenuItem key={summary.ID} aria-pressed={summary.Active} onSelect={() => openConversation(summary.ID)}>
                                    {summary.Title}
                                </DropdownMenuItem>
                            ))}
                        </DropdownMenuContent>
                    </DropdownMenu>
                )}

                <Quiet onClick={async () => { await startConversation(); input.current?.focus(); }}>
                    <Plus className="size-3.5" /> New conversation
                </Quiet>
            </div>
        </section>
    );
}

function Quiet({ className, ...props }: React.ComponentProps<"button">) {
    return (
        <button
            type="button"
            className={cn("flex items-center gap-1.5 rounded-full px-2.5 py-1 transition-colors hover:bg-secondary hover:text-foreground", className)}
            {...props}
        />
    );
}

function Message({ item, onApply, onAccount }: { item: Item; onApply: () => void; onAccount: () => void }) {
    const you = item.from === "you";
    return (
        <div className={cn("rise flex max-w-[64ch] flex-col gap-2", you && "items-end self-end")}>
            {item.quota ? (
                <QuotaCard error={item.quota} onAccount={onAccount} />
            ) : you ? (
                <div className="whitespace-pre-wrap rounded-[20px_20px_7px_20px] bg-primary px-4 py-2.5 text-primary-foreground shadow-lift">{item.text}</div>
            ) : item.tone === "answer" ? (
                <Markdown text={item.text} />
            ) : (
                // Failure is a colour, nothing else: the same shape as an
                // answer, read differently at a glance.
                <div className={cn("whitespace-pre-wrap", item.tone === "problem" ? "text-destructive" : "text-faint")}>{item.text}</div>
            )}
            <Changes changed={item.changed} />
            {item.plan && item.plan.length > 0 && (
                // A plan is offered for acceptance: nothing has happened yet,
                // and the steps applied are the ones shown rather than a
                // second answer to the same question.
                <div className="flex flex-col items-start gap-2">
                    <ul className="text-[13px] text-warning">
                        {item.plan.map((step, i) => <li key={i} className="py-0.5">{step.Description}</li>)}
                    </ul>
                    <Button type="button" size="sm" className="rounded-full" onClick={onApply}>Do it</Button>
                </div>
            )}
        </div>
    );
}

// What the DAW confirmed, shown apart from what the model said about it:
// the model is the one account of a turn that cannot check itself.
export function Changes({ changed }: { changed?: ParamChange[] | null }) {
    if (!changed || changed.length === 0) return null;
    return (
        <ul className="flex flex-col items-start gap-0.5 text-[13px] tabular-nums">
            {changed.map((change, i) => {
                const target = `track ${change.Track} ${change.Param}`;
                const unconfirmed = change.NewValue === null || change.NewValue === undefined;
                return (
                    <li key={i} className={cn("flex items-baseline gap-2 py-0.5", unconfirmed ? "text-warning" : "text-success")}>
                        <span className="font-semibold">{unconfirmed ? "?" : "✓"}</span>
                        {unconfirmed ? `${target}: sent ${format(change.Requested)}, not confirmed` : `${target}: ${format(change.NewValue)}`}
                    </li>
                );
            })}
        </ul>
    );
}
