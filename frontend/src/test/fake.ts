// A stand-in for the three Wails services, shaped like the backend the
// window really talks to. It is asynchronous the way the real one is, it
// hands back null where a Go slice with nothing in it crosses as null, and
// it refuses what the backend refuses. A fake kinder than the backend is
// worse than none, so each behaviour here mirrors one in backend/app.
import type {
    AgentResponse,
    ChatMessage,
    Conversation,
    ConversationSummary,
    DAWStatus,
    HostedStatus,
    JournalEntry,
    PlannedCall,
    Settings,
    SettingsResult,
    SignInState,
} from "../../bindings/tonelab/backend/app/models";

type Thread = Conversation & { Messages: ChatMessage[] };

// What the next command should do, set by the test that sends it.
export interface Scripted {
    reply?: string;
    plan?: PlannedCall[];
    changed?: AgentResponse["Changed"];
    error?: { Code: string; Message: string };
    // Held open until the test releases it, for anything that watches a
    // turn in flight.
    hold?: Promise<void>;
}

export const world = {
    threads: [] as Thread[],
    active: "",
    journal: [] as JournalEntry[],
    settings: null as unknown as Settings,
    apiKey: "",
    searchKey: "",
    saves: [] as { settings: Settings; apiKey: string; searchKey: string }[],
    daw: { Connected: true, Detail: "Feedback seen 1s ago" } as DAWStatus,
    hosted: null as unknown as HostedStatus,
    signIn: null as unknown as SignInState,
    next: {} as Scripted,
    stopped: 0,
    undone: 0,
    plan: null as PlannedCall[] | null,
};

let ids = 0;

export function reset() {
    ids = 0;
    world.threads = [];
    world.active = "";
    world.journal = [];
    world.settings = {
        BaseURL: "http://localhost:11434/v1",
        Model: "bonsai-tonelab:latest",
        APIKeySet: false,
        DAWBackend: "reaper",
        DAWHost: "127.0.0.1",
        DAWPort: 8000,
        DAWFeedback: 9000,
        DAWAvailable: ["reaper", "ableton"],
        PreviewByDefault: false,
        Theme: "system",
        SearchProvider: "",
        SearchURL: "",
        SearchKeySet: false,
        SearchAvailable: ["brave", "searxng", "tonelab"],
    };
    world.apiKey = "";
    world.searchKey = "";
    world.saves = [];
    world.daw = { Connected: true, Detail: "Feedback seen 1s ago" };
    world.hosted = {
        URL: "https://api.tonelab.dev",
        SignedIn: false,
        Email: "",
        Plan: "",
        Active: false,
        Reason: "",
        Month: { used: 0, limit: 0, resets_at: "" },
        Day: { used: 0, limit: 0, resets_at: "" },
        Searches: { used: 0, limit: 0, resets_at: "" },
        Models: ["tonelab"],
        Error: "",
    };
    world.signIn = { Running: false, Done: false, UserCode: "", VerifyURL: "", Error: "" };
    world.next = {};
    world.stopped = 0;
    world.undone = 0;
    world.plan = null;
}

reset();

// Go's empty slice crosses the bridge as null; the window has to cope.
const orNull = <T>(list: T[]): T[] | null => (list.length === 0 ? null : list);

const tick = () => new Promise<void>((done) => setTimeout(done, 0));

function current(): Thread {
    let thread = world.threads.find((t) => t.ID === world.active);
    if (!thread) {
        thread = { ID: `c${++ids}`, Title: "New conversation", Started: new Date().toISOString(), Messages: [] };
        world.threads.unshift(thread);
        world.active = thread.ID;
    }
    return thread;
}

function outward(thread: Thread): Conversation {
    return { ...thread, Messages: orNull(thread.Messages) };
}

function summaries(): ConversationSummary[] {
    return world.threads.map((t) => ({ ID: t.ID, Title: t.Title, Active: t.ID === world.active }));
}

async function turn(text: string, preview: boolean): Promise<AgentResponse> {
    await tick();
    const thread = current();
    const script = world.next;
    world.next = {};
    if (thread.Title === "New conversation") {
        thread.Title = text.length > 40 ? `${text.slice(0, 40)}…` : text;
    }
    thread.Messages.push({ From: "you", Text: text, Changed: null, Plan: null, Error: null });
    if (script.hold) await script.hold;

    const response: AgentResponse = {
        Message: script.reply ?? "Done.",
        Conversation: thread.ID,
        Steps: null,
        Plan: preview ? orNull(script.plan ?? []) : null,
        Changed: preview ? null : (script.changed ?? null),
        Error: script.error ?? null,
    };
    world.plan = preview ? (script.plan ?? null) : null;
    thread.Messages.push({
        From: "tonelab",
        Text: response.Message,
        Changed: response.Changed,
        Plan: response.Plan,
        Error: response.Error,
    });
    world.journal.unshift({
        At: "12:00",
        Command: text,
        Answer: response.Message,
        Steps: null,
        Error: response.Error,
        Preview: preview,
    });
    return response;
}

export const AgentService = {
    SendCommand: (text: string) => turn(text, false),
    PreviewCommand: (text: string) => turn(text, true),
    async ApplyPlan(): Promise<AgentResponse> {
        await tick();
        const thread = current();
        if (!world.plan) {
            return { Message: "", Conversation: thread.ID, Steps: null, Plan: null, Changed: null, Error: { Code: "no_plan", Message: "There is no plan to apply." } };
        }
        world.plan = null;
        const response: AgentResponse = { Message: "Applied.", Conversation: thread.ID, Steps: null, Plan: null, Changed: world.next.changed ?? null, Error: null };
        world.next = {};
        thread.Messages.push({ From: "tonelab", Text: response.Message, Changed: response.Changed, Plan: null, Error: null });
        return response;
    },
    async Stop(): Promise<AgentResponse> {
        await tick();
        world.stopped++;
        return { Message: "", Conversation: "", Steps: null, Plan: null, Changed: null, Error: null };
    },
    async Undo(): Promise<AgentResponse> {
        await tick();
        world.undone++;
        const thread = current();
        const message = "The DAW reversed its last change.";
        thread.Messages.push({ From: "tonelab", Text: message, Changed: null, Plan: null, Error: null });
        return { Message: message, Conversation: thread.ID, Steps: null, Plan: null, Changed: null, Error: null };
    },
    async Forget(): Promise<AgentResponse> {
        await tick();
        return { Message: "", Conversation: "", Steps: null, Plan: null, Changed: null, Error: null };
    },
    async GetDAWStatus(): Promise<DAWStatus> {
        await tick();
        return { ...world.daw };
    },
    async History(): Promise<JournalEntry[] | null> {
        await tick();
        return orNull(world.journal);
    },
    async Conversations(): Promise<ConversationSummary[] | null> {
        await tick();
        return orNull(summaries());
    },
    async CurrentConversation(): Promise<Conversation> {
        await tick();
        return outward(current());
    },
    async StartConversation(): Promise<Conversation> {
        await tick();
        world.active = "";
        return outward(current());
    },
    async OpenConversation(id: string): Promise<Conversation> {
        await tick();
        const thread = world.threads.find((t) => t.ID === id);
        if (!thread) throw new Error("no such conversation");
        world.active = id;
        return outward(thread);
    },
    async RenameConversation(id: string, name: string): Promise<AgentResponse> {
        await tick();
        const thread = world.threads.find((t) => t.ID === id);
        if (thread) thread.Title = name;
        return { Message: "", Conversation: id, Steps: null, Plan: null, Changed: null, Error: null };
    },
    async DeleteConversation(id: string): Promise<Conversation> {
        await tick();
        world.threads = world.threads.filter((t) => t.ID !== id);
        if (world.active === id) world.active = world.threads[0]?.ID ?? "";
        return outward(current());
    },
};

export const SettingsService = {
    async Get(): Promise<Settings> {
        await tick();
        return { ...world.settings, APIKeySet: world.apiKey !== "", SearchKeySet: world.searchKey !== "" };
    },
    async Save(incoming: Settings, apiKey: string, searchKey: string): Promise<SettingsResult> {
        await tick();
        world.saves.push({ settings: incoming, apiKey, searchKey });
        if (incoming.BaseURL.trim() === "") {
            return { Saved: false, RestartNeeded: false, Message: "", Error: { Code: "invalid_settings", Message: "The endpoint cannot be empty." } };
        }
        const restart = incoming.DAWBackend !== world.settings.DAWBackend
            || incoming.DAWHost !== world.settings.DAWHost
            || incoming.DAWPort !== world.settings.DAWPort
            || incoming.DAWFeedback !== world.settings.DAWFeedback;
        world.settings = { ...world.settings, ...incoming };
        if (apiKey !== "") world.apiKey = apiKey;
        if (searchKey !== "") world.searchKey = searchKey;
        return {
            Saved: true,
            RestartNeeded: restart,
            Message: restart ? "Saved. The DAW settings apply after a restart." : "Saved.",
            Error: null,
        };
    },
};

export const HostedService = {
    async Status(): Promise<HostedStatus> {
        await tick();
        return { ...world.hosted };
    },
    async SignIn(): Promise<SignInState> {
        await tick();
        if (world.signIn.Error) return { ...world.signIn };
        world.signIn = { Running: true, Done: false, UserCode: "ABCD-EFGH", VerifyURL: "https://tonelab.dev/device", Error: "" };
        return { ...world.signIn };
    },
    async State(): Promise<SignInState> {
        await tick();
        return { ...world.signIn };
    },
    async Cancel(): Promise<void> {
        await tick();
        world.signIn = { Running: false, Done: false, UserCode: "", VerifyURL: "", Error: "" };
    },
    async SignOut(): Promise<HostedStatus> {
        await tick();
        world.hosted = { ...world.hosted, SignedIn: false, Email: "", Plan: "", Active: false, Reason: "" };
        return { ...world.hosted };
    },
};
