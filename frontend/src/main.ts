import { renderMarkdown } from "./markdown";
import { AgentService, HostedService, SettingsService } from "../bindings/tonelab/backend/app";
import type {
    AgentResponse,
    ChatMessage,
    ConversationSummary,
    JournalEntry,
    Settings,
} from "../bindings/tonelab/backend/app/models";

type Tone = "answer" | "problem" | "working";

const el = <T extends HTMLElement>(id: string) => document.getElementById(id) as T;

const thread = el("thread");
const empty = el("empty");
const composer = el<HTMLFormElement>("composer");
const input = el<HTMLTextAreaElement>("input");
const send = el<HTMLButtonElement>("send");
const stop = el<HTMLButtonElement>("stop");
const previewMode = el<HTMLInputElement>("preview-mode");
const status = el("status");
const statusText = el("status-text");
const historyView = el("history");
const chats = el("chats");
const pick = el<HTMLButtonElement>("pick");
const pickName = el("pick-name");
const pickList = el("pick-list");


// How stale the connection light may be. The backend decides what counts as
// connected; this only decides how often it is asked.
const statusInterval = 3000;

// Which conversation the window is showing. A turn can finish after the user
// has moved to another one, and its answer belongs where it was asked.
let showing = "";

/* Greeting ----------------------------------------------------------- */

// An empty screen is the one place with room for a sentence rather than
// instructions. Chosen by the hour, because the same line every morning stops
// being read after the second day.
const greetings: Record<string, string[]> = {
    night: [
        "Let's make something at this hour.",
        "The quiet part of the day.",
        "Still going. Good.",
    ],
    morning: [
        "Fresh ears this morning.",
        "Let's hear it.",
        "Start where you left off.",
    ],
    afternoon: [
        "What are we shaping today?",
        "Let's get into it.",
        "Tell me what to move.",
    ],
    evening: [
        "Let's create through your night.",
        "The good hours.",
        "What needs fixing tonight?",
    ],
};

function greet() {
    const hour = new Date().getHours();
    const part = hour < 5 ? "night" : hour < 12 ? "morning" : hour < 18 ? "afternoon" : "evening";
    const lines = greetings[part];
    el("greeting").textContent = lines[Math.floor(Math.random() * lines.length)];
}

/* Theme -------------------------------------------------------------- */

// Applied to the root rather than swapped stylesheet, so a change is one
// attribute and the transition is free. "system" leaves the attribute off and
// lets the media query decide.
let chosenTheme = "system";

function applyTheme(name: string) {
    chosenTheme = name;
    const dark = name === "dark" || (name === "system" && matchMedia("(prefers-color-scheme: dark)").matches);
    document.documentElement.dataset.theme = dark ? "dark" : "light";

    for (const button of document.querySelectorAll<HTMLButtonElement>("#theme .choice")) {
        button.setAttribute("aria-pressed", String(button.dataset.theme === name));
    }
}

matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => {
    if (chosenTheme === "system") {
        applyTheme("system");
    }
});

// Applied at once rather than on save: a look you cannot see until you commit
// to it is one you cannot choose. Marked as unsaved too, so leaving the screen
// and coming back does not undo it.
for (const button of document.querySelectorAll<HTMLButtonElement>("#theme .choice")) {
    button.addEventListener("click", () => {
        applyTheme(button.dataset.theme!);
        settingsTouched = true;
    });
}


/* Views ------------------------------------------------------------- */

// Shown and hidden rather than routed: three views is not what a router is
// for; a fourth, or a view with state that outlives it, is where that changes.
function show(view: string) {
    for (const tab of document.querySelectorAll<HTMLButtonElement>(".tab")) {
        tab.setAttribute("aria-selected", String(tab.dataset.view === view));
    }
    for (const section of document.querySelectorAll<HTMLElement>(".view")) {
        section.hidden = section.id !== `view-${view}`;
    }
    if (view === "history") {
        renderHistory();
        renderConversations();
    }
    if (view === "settings") {
        // Reloaded only when nothing is half-typed. Reading the file on every
        // visit threw away unsaved edits, which showed up first as the theme
        // snapping back but applied to every field on the screen.
        if (!settingsTouched) {
            loadSettings();
        }
    }
    if (view === "chat") {
        input.focus();
    }
}

for (const tab of document.querySelectorAll<HTMLButtonElement>(".tab")) {
    tab.addEventListener("click", () => show(tab.dataset.view!));
}

/* Thread ------------------------------------------------------------ */

function append(from: "you" | "tonelab", text: string, tone: Tone = "answer"): HTMLElement {
    empty.hidden = true;

    const message = document.createElement("div");
    message.className = "msg";
    message.dataset.from = from;
    message.dataset.tone = tone;

    const who = document.createElement("span");
    who.className = "who";
    who.textContent = from;

    const said = document.createElement("div");
    said.className = "said";
    if (from === "tonelab" && tone === "answer") {
        // The model writes markdown; laid out rather than shown with its
        // asterisks, and from escaped text, so nothing it says is markup.
        said.replaceChildren(renderMarkdown(text));
    } else {
        said.textContent = text;
    }

    message.append(who, said);
    thread.append(message);
    thread.scrollTop = thread.scrollHeight;
    return message;
}

// What the DAW confirmed, shown apart from what the model said about it: the
// model is the one account of a turn that cannot check itself.
function attachChanges(message: HTMLElement, changed: AgentResponse["Changed"]) {
    if (!changed || changed.length === 0) {
        return;
    }
    const list = document.createElement("ul");
    list.className = "did";

    for (const change of changed) {
        const line = document.createElement("li");
        const target = `track ${change.Track} ${change.Param}`;
        if (change.NewValue === null || change.NewValue === undefined) {
            line.className = "unconfirmed";
            line.textContent = `${target}: sent ${format(change.Requested)}, not confirmed`;
        } else {
            line.className = "confirmed";
            line.textContent = `${target}: ${format(change.NewValue)}`;
        }
        list.append(line);
    }
    message.append(list);
    thread.scrollTop = thread.scrollHeight;
}

// A plan is offered for acceptance, since nothing has happened yet and the
// steps applied are the ones shown rather than a second answer to the same
// question.
function attachPlan(message: HTMLElement, plan: AgentResponse["Plan"]) {
    if (!plan || plan.length === 0) {
        return;
    }
    const box = document.createElement("div");
    box.className = "plan";

    const list = document.createElement("ul");
    list.className = "did";
    for (const step of plan) {
        const line = document.createElement("li");
        line.textContent = step.Description;
        list.append(line);
    }

    const apply = document.createElement("button");
    apply.className = "send";
    apply.type = "button";
    apply.textContent = "Do it";
    apply.addEventListener("click", async () => {
        apply.disabled = true;
        const response = await AgentService.ApplyPlan();
        box.remove();
        report(response);
    });

    box.append(list, apply);
    message.append(box);
    thread.scrollTop = thread.scrollHeight;
}

function format(value: unknown): string {
    if (typeof value === "boolean") {
        return value ? "on" : "off";
    }
    if (typeof value === "number") {
        return value.toFixed(2);
    }
    return String(value);
}

// A few codes change what the user should do next; the rest carry a message
// already written for them.
function explain(code: string, message: string): string {
    switch (code) {
        case "llm_unreachable":
            return `${message} Check the endpoint in Settings, and that a local model is running.`;
        case "llm_unauthorized":
            return `${message} Check the API key in Settings.`;
        case "llm_timeout":
            return `${message}`;
        case "daw_command_failed":
            return `${message} Check the DAW is running.`;
        default:
            return message;
    }
}

function report(response: AgentResponse) {
    if (response.Error) {
        append("tonelab", explain(response.Error.Code, response.Error.Message), "problem");
        return;
    }
    const message = append("tonelab", response.Message || "Done.");
    attachChanges(message, response.Changed);
    attachPlan(message, response.Plan);
}

// Chips seen before are drawn settled: the list is rebuilt after every
// rename, delete or switch, and replaying the entrance each time made a
// click look like a page load.
const knownChips = new Set<string>();

function renderChip(summary: ConversationSummary): HTMLElement {
    const chip = document.createElement("button");
    chip.className = "thread-chip";
    chip.dataset.settled = String(knownChips.has(summary.ID));
    knownChips.add(summary.ID);
    chip.type = "button";
    chip.setAttribute("aria-pressed", String(summary.Active));

    const name = document.createElement("span");
    name.className = "chip-name";
    name.textContent = summary.Title;

    // Renaming happens in place: a guess made from the first thing said
    // should be correctable without a dialogue about correcting it.
    name.addEventListener("dblclick", (event) => {
        event.stopPropagation();
        name.contentEditable = "true";
        name.focus();
        getSelection()?.selectAllChildren(name);
    });

    const commit = async () => {
        if (name.contentEditable !== "true") {
            return;
        }
        name.contentEditable = "false";
        const chosen = (name.textContent ?? "").trim();
        if (chosen === "" || chosen === summary.Title) {
            name.textContent = summary.Title;
            return;
        }
        await AgentService.RenameConversation(summary.ID, chosen);
        await renderConversations();
    };

    name.addEventListener("blur", commit);
    name.addEventListener("keydown", (event) => {
        if (event.key === "Enter") {
            event.preventDefault();
            commit();
        }
        if (event.key === "Escape") {
            name.textContent = summary.Title;
            name.contentEditable = "false";
        }
    });

    const edit = document.createElement("span");
    edit.className = "chip-drop";
    edit.setAttribute("role", "button");
    edit.setAttribute("aria-label", `Rename ${summary.Title}`);
    edit.append(icon("pencil"));
    edit.addEventListener("click", (event) => {
        event.stopPropagation();
        name.contentEditable = "true";
        name.focus();
        getSelection()?.selectAllChildren(name);
    });

    const drop = document.createElement("span");
    drop.className = "chip-drop";
    drop.setAttribute("role", "button");
    drop.setAttribute("aria-label", `Delete ${summary.Title}`);
    drop.append(icon("trash"));
    drop.addEventListener("click", async (event) => {
        event.stopPropagation();
        const remaining = await AgentService.DeleteConversation(summary.ID);
        await renderConversations();
        drawThread(remaining.Messages ?? [], remaining.ID);
    });

    chip.append(name, edit, drop);
    chip.addEventListener("click", async () => {
        if (name.contentEditable === "true") {
            return;
        }
        const opened = await AgentService.OpenConversation(summary.ID);
        await renderConversations();
        drawThread(opened.Messages ?? [], opened.ID);
    });
    return chip;
}

/* Sending ----------------------------------------------------------- */

let running = false;

async function submit() {
    const text = input.value.trim();
    if (text === "" || running) {
        return;
    }

    append("you", text);
    input.value = "";
    resize();
    setRunning(true);

    const waiting = append("tonelab", "Working…", "working");

    const asked = showing;

    try {
        const response = previewMode.checked
            ? await AgentService.PreviewCommand(text)
            : await AgentService.SendCommand(text);
        waiting.remove();

        // Drawn only if the window is still on the conversation that asked.
        // The answer is kept either way; it is waiting in that thread.
        if (response.Conversation === "" || response.Conversation === asked) {
            report(response);
        }
    } catch (error) {
        waiting.remove();
        // Reaching here means the call itself broke, rather than the command
        // failing, which the backend reports inside the response.
        const reason = (error instanceof Error ? error.message : String(error)).trim();
        append("tonelab", reason
            ? `The backend could not be reached. ${reason}`
            : "The backend could not be reached. Restart the app if this persists.", "problem");
    } finally {
        setRunning(false);
        input.focus();
        renderConversations();
    }
}

// Stop replaces Send while a turn runs, so the button under the cursor is
// always the one that applies.
function setRunning(active: boolean) {
    running = active;
    send.hidden = active;
    stop.hidden = !active;
}

composer.addEventListener("submit", (event) => {
    event.preventDefault();
    submit();
});

// Enter sends, Shift+Enter makes a new line, which is what a text box in a
// chat is expected to do.
input.addEventListener("keydown", (event) => {
    if (event.key === "Enter" && !event.shiftKey) {
        event.preventDefault();
        submit();
    }
});

function resize() {
    input.style.height = "auto";
    input.style.height = `${Math.min(input.scrollHeight, 160)}px`;
}

input.addEventListener("input", resize);

stop.addEventListener("click", async () => {
    await AgentService.Stop();
});

el("undo").addEventListener("click", async () => {
    report(await AgentService.Undo());
});

el("clear").addEventListener("click", async () => {
    // Starts a thread rather than destroying one: the old conversation stays
    // in the list, which is what the words on the button mean.
    const started = await AgentService.StartConversation();
    await renderConversations();
    drawThread([], started.ID);
    input.focus();
});

/* Conversations ------------------------------------------------------ */

// The thread lives in the backend, because one that only exists in the page
// cannot survive being switched away from.

// Faded out before it is rebuilt and back in after, so switching or starting
// a conversation reads as one movement rather than a screen blinking into a
// different one.
async function drawThread(messages: ChatMessage[], id = showing) {
    showing = id;
    thread.dataset.swapping = "true";
    await new Promise((done) => setTimeout(done, 110));

    thread.querySelectorAll(".msg").forEach((node) => node.remove());
    empty.hidden = messages.length > 0;
    if (messages.length === 0) {
        greet();
    }

    for (const message of messages) {
        if (message.Error) {
            append("tonelab", explain(message.Error.Code, message.Error.Message), "problem");
            continue;
        }
        const node = append(message.From as "you" | "tonelab", message.Text || "Done.");
        attachChanges(node, message.Changed);
        // Plans are not redrawn: a plan is an offer made once, and one
        // reopened hours later would invite accepting something stale.
    }

    thread.dataset.swapping = "false";
}

async function renderConversations() {
    const threads = (await AgentService.Conversations()) ?? [];

    // The full list, where a conversation can be renamed or thrown away.
    chats.replaceChildren();
    for (const summary of threads) {
        chats.append(renderChip(summary));
    }

    // And the name of the one being spoken to, which is all the chat needs.
    const active = threads.find((summary) => summary.Active);
    pickName.textContent = active ? active.Title : "New conversation";
    pick.hidden = threads.length < 2;
}

/* Picker ------------------------------------------------------------- */

// Opened on demand rather than shown always: switching is frequent enough
// that leaving the chat for it would be a tax, and rare enough that a
// permanent row would take height from the thread it points at.
function closePicker() {
    pickList.hidden = true;
    pick.parentElement!.dataset.open = "false";
    pick.setAttribute("aria-expanded", "false");
}

pick.addEventListener("click", async (event) => {
    event.stopPropagation();
    if (!pickList.hidden) {
        closePicker();
        return;
    }

    const threads = (await AgentService.Conversations()) ?? [];
    pickList.replaceChildren();
    for (const summary of threads) {
        const item = document.createElement("button");
        item.className = "pick-item";
        item.type = "button";
        item.textContent = summary.Title;
        item.setAttribute("aria-pressed", String(summary.Active));
        item.addEventListener("click", async () => {
            closePicker();
            const opened = await AgentService.OpenConversation(summary.ID);
            await renderConversations();
            drawThread(opened.Messages ?? [], opened.ID);
        });
        pickList.append(item);
    }

    pickList.hidden = false;
    pick.parentElement!.dataset.open = "true";
    pick.setAttribute("aria-expanded", "true");
});

// Anywhere else dismisses it, which is what a menu is expected to do.
document.addEventListener("click", () => closePicker());
document.addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
        closePicker();
    }
});

/* History ----------------------------------------------------------- */

async function renderHistory() {
    // A Go slice with no elements crosses as null rather than an empty array.
    const entries = (await AgentService.History()) ?? [];
    historyView.replaceChildren();

    if (entries.length === 0) {
        const nothing = document.createElement("p");
        nothing.className = "hint";
        nothing.textContent = "Nothing yet.";
        historyView.append(nothing);
        return;
    }
    for (const entry of entries) {
        historyView.append(renderTurn(entry));
    }
}

// Read as sentences, with the raw call behind a disclosure: someone reading
// their history wants to know what happened, and someone debugging wants the
// call. Showing the second to everyone is what made this unreadable before.
function renderTurn(entry: JournalEntry): HTMLElement {
    const turn = document.createElement("article");
    turn.className = "turn";

    const head = document.createElement("div");
    head.className = "turn-head";

    const time = document.createElement("span");
    time.className = "turn-time";
    time.textContent = entry.At;

    const command = document.createElement("span");
    command.textContent = entry.Command;
    head.append(time, command);

    if (entry.Preview) {
        const tag = document.createElement("span");
        tag.className = "turn-tag";
        tag.textContent = "proposed only";
        head.append(tag);
    }
    turn.append(head);

    const steps = entry.Steps ?? [];
    if (steps.length > 0) {
        const list = document.createElement("ul");
        list.className = "turn-steps";
        for (const step of steps) {
            const line = document.createElement("li");
            line.dataset.failed = String(step.Failed);
            line.append(icon(step.Failed ? "alert" : iconFor(step.Tool)));

            const said = document.createElement("span");
            said.textContent = describeStep(step.Tool, step.Arguments, step.Outcome, step.Failed);
            line.append(said);
            list.append(line);
        }
        turn.append(list);

        const raw = document.createElement("details");
        raw.className = "raw";
        const summary = document.createElement("summary");
        summary.textContent = "Exact calls";
        const pre = document.createElement("pre");
        pre.textContent = steps
            .map((step) => `${step.Tool} ${step.Arguments}\n  ${step.Outcome}`)
            .join("\n\n");
        raw.append(summary, pre);
        turn.append(raw);
    }

    const outcome = document.createElement("div");
    outcome.className = entry.Error ? "said turn-problem" : "said";
    if (entry.Error) {
        outcome.textContent = `${entry.Error.Code}: ${entry.Error.Message}`;
    } else {
        outcome.replaceChildren(renderMarkdown(entry.Answer));
    }
    turn.append(outcome);
    return turn;
}

// Referencing the sprite rather than building paths, so an icon is one line
// here and its shape lives in one place.
function icon(name: string): SVGSVGElement {
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("class", "icon");
    const use = document.createElementNS("http://www.w3.org/2000/svg", "use");
    use.setAttribute("href", `#i-${name}`);
    svg.append(use);
    return svg;
}

// The kind of step, so a history can be scanned rather than read.
function iconFor(tool: string): string {
    switch (tool) {
        case "list_tracks": return "list";
        case "get_param": return "read";
        case "set_param": return "set";
        case "set_fx_param": return "set";
        case "get_fx_param": return "read";
        case "list_fx": return "list";
        case "find_params": return "list";
        case "search": return "list";
        case "fetch_page": return "read";
        case "undo": return "undo";
        default: return "settings";
    }
}

// Translated rather than printed. The tool names and JSON are ours, not the
// user's, and a history that reads like a log is one nobody reads.
function describeStep(tool: string, args: string, outcome: string, failed: boolean): string {
    const parsed = parse(args);
    const track = parsed.track_id ?? "";
    const name = parsed.param_name ?? "";

    let said: string;
    switch (tool) {
        case "list_tracks":
            said = `Looked up the tracks`;
            break;
        case "get_param":
            said = `Read ${name} on track ${track}`;
            break;
        case "set_param":
            said = `Set ${name} on track ${track} to ${format(parsed.value)}`;
            break;
        case "undo":
            said = "Asked the DAW to undo";
            break;
        case "list_fx":
            said = `Looked up the effects on track ${track}`;
            break;
        case "find_params":
            said = `Searched track ${track} for "${parsed.query ?? ""}"`;
            break;
        case "search":
            said = `Searched the web for "${parsed.query ?? ""}"`;
            break;
        case "fetch_page":
            said = `Read ${parsed.url ?? "a page"}`;
            break;
        case "get_fx_param":
            said = `Read ${fxTarget(outcome, parsed)} on track ${track}`;
            break;
        case "set_fx_param":
            said = `Set ${fxTarget(outcome, parsed)} on track ${track} to ${format(parsed.value)}`;
            break;
        default:
            said = tool;
    }
    if (failed) {
        const reason = parse(outcome).error?.message;
        return `${said}, refused${reason ? `: ${reason}` : ""}`;
    }
    return said;
}

// The call carries positions and the outcome carries names; a person wants
// the names, so they are taken from the outcome when the call succeeded.
function fxTarget(outcome: string, parsed: any): string {
    const value = parse(outcome).value ?? {};
    if (value.fx_name && value.name) {
        return `${value.name} on ${value.fx_name}`;
    }
    return `effect ${parsed.fx_id ?? "?"} parameter ${parsed.param_id ?? "?"}`;
}

function parse(text: string): any {
    try {
        return JSON.parse(text);
    } catch {
        return {};
    }
}

/* Settings ---------------------------------------------------------- */

// Whether anything on the settings screen has been changed since it was last
// loaded or saved. Unsaved work belongs to the person who typed it.
let settingsTouched = false;

el("settings").addEventListener("input", () => {
    settingsTouched = true;
});

// Which of key and address a provider needs is the backend's to know; the
// window only hides both while search is off.
function showSearchFields() {
    const off = el<HTMLSelectElement>("search-provider").value === "";
    el("search-key-field").hidden = off;
    el("search-url-field").hidden = off;
}

el("search-provider").addEventListener("change", showSearchFields);

async function loadSettings() {
    const settings = await SettingsService.Get();

    el<HTMLInputElement>("base-url").value = settings.BaseURL;
    el<HTMLInputElement>("model").value = settings.Model;
    el<HTMLInputElement>("api-key").value = "";
    el("key-hint").textContent = settings.APIKeySet
        ? "A key is saved. Leave this empty to keep it, or type a new one to replace it."
        : "No key saved. A local model usually needs none.";

    const backends = el<HTMLSelectElement>("daw-backend");
    backends.replaceChildren();
    for (const name of settings.DAWAvailable ?? []) {
        const option = document.createElement("option");
        option.value = name;
        option.textContent = name;
        backends.append(option);
    }
    backends.value = settings.DAWBackend;

    el<HTMLInputElement>("daw-host").value = settings.DAWHost;
    el<HTMLInputElement>("daw-port").value = String(settings.DAWPort);
    el<HTMLInputElement>("daw-feedback").value = String(settings.DAWFeedback);

    const providers = el<HTMLSelectElement>("search-provider");
    providers.replaceChildren();
    for (const name of ["", ...(settings.SearchAvailable ?? [])]) {
        const option = document.createElement("option");
        option.value = name;
        option.textContent = name === "" ? "Off" : name;
        providers.append(option);
    }
    providers.value = settings.SearchProvider ?? "";
    el<HTMLInputElement>("search-key").value = "";
    el<HTMLInputElement>("search-url").value = settings.SearchURL ?? "";
    el("search-key-hint").textContent = settings.SearchKeySet
        ? "A key is saved. Leave this empty to keep it, or type a new one to replace it."
        : "No key saved.";
    showSearchFields();
    el<HTMLInputElement>("preview-default").checked = settings.PreviewByDefault;
    previewMode.checked = settings.PreviewByDefault;
    applyTheme(settings.Theme || "system");
    el("settings-note").textContent = "";
    settingsTouched = false;
}

el<HTMLFormElement>("settings").addEventListener("submit", async (event) => {
    event.preventDefault();

    const settings: Settings = {
        BaseURL: el<HTMLInputElement>("base-url").value,
        Model: el<HTMLInputElement>("model").value,
        APIKeySet: false,
        DAWBackend: el<HTMLSelectElement>("daw-backend").value,
        DAWHost: el<HTMLInputElement>("daw-host").value,
        DAWPort: Number(el<HTMLInputElement>("daw-port").value),
        DAWFeedback: Number(el<HTMLInputElement>("daw-feedback").value),
        DAWAvailable: [],
        PreviewByDefault: el<HTMLInputElement>("preview-default").checked,
        Theme: chosenTheme,
        SearchProvider: el<HTMLSelectElement>("search-provider").value,
        SearchURL: el<HTMLInputElement>("search-url").value,
        SearchKeySet: false,
        SearchAvailable: [],
    };

    const result = await SettingsService.Save(settings, el<HTMLInputElement>("api-key").value, el<HTMLInputElement>("search-key").value);
    const note = el("settings-note");
    if (result.Error) {
        note.textContent = result.Error.Message;
        note.style.color = "var(--alarm)";
        return;
    }
    note.style.color = "";
    note.textContent = result.Message;
    previewMode.checked = settings.PreviewByDefault;
    el<HTMLInputElement>("api-key").value = "";
    settingsTouched = false;
});

/* Account ----------------------------------------------------------- */

// The account block has three faces: signed out, waiting for the browser,
// signed in. Which one shows is the backend's answer, never a guess made
// here, so a reload lands on the truth.
function showAccount(face: "out" | "pending" | "in") {
    el("account-out").hidden = face !== "out";
    el("account-pending").hidden = face !== "pending";
    el("account-in").hidden = face !== "in";
}

function whenResets(at: string): string {
    const ms = new Date(at).getTime() - Date.now();
    if (!Number.isFinite(ms) || ms <= 0) return "resets now";
    const hours = Math.round(ms / 3600000);
    if (hours < 48) return `resets in ${Math.max(1, hours)}h`;
    return `resets in ${Math.round(hours / 24)}d`;
}

function meter(label: string, used: number, limit: number, resetsAt: string): HTMLElement {
    const row = document.createElement("div");
    row.className = "meter";
    const share = limit > 0 ? used / limit : 0;
    row.dataset.level = share >= 1 ? "full" : share >= 0.8 ? "high" : "ok";
    const name = document.createElement("span");
    name.textContent = label;
    const bar = document.createElement("span");
    bar.className = "bar";
    const fill = document.createElement("span");
    fill.style.width = `${Math.min(100, Math.round(share * 100))}%`;
    bar.append(fill);
    const note = document.createElement("span");
    note.className = "hint";
    note.textContent = `${Math.round(share * 100)}%, ${whenResets(resetsAt)}`;
    row.append(name, bar, note);
    return row;
}

async function loadAccount() {
    const status = await HostedService.Status();
    const models = el<HTMLDataListElement>("models");
    models.replaceChildren();
    for (const name of status.Models ?? []) {
        const option = document.createElement("option");
        option.value = name;
        models.append(option);
    }
    if (!status.SignedIn) {
        showAccount("out");
        el("account-note").textContent = "";
        return;
    }
    showAccount("in");
    el("account-email").textContent = status.Email || "Signed in";
    el("account-plan").textContent = status.Plan ? `on the ${status.Plan} plan` : "";
    el("account-reason").textContent = status.Error || (status.Active ? "" : status.Reason);
    const usage = el("usage");
    usage.replaceChildren();
    if (status.Active) {
        usage.append(
            meter("This month", status.Month.used, status.Month.limit, status.Month.resets_at),
            meter("Today", status.Day.used, status.Day.limit, status.Day.resets_at),
            meter("Searches", status.Searches.used, status.Searches.limit, status.Searches.resets_at),
        );
    }
}

// The browser leg can take minutes; the window asks every two seconds
// whether it is done rather than holding a call open.
let signInTimer: number | undefined;

async function watchSignIn() {
    const state = await HostedService.State();
    if (state.Running) {
        showAccount("pending");
        el("user-code").textContent = state.UserCode;
        el("verify-url").textContent = state.VerifyURL;
        return;
    }
    window.clearInterval(signInTimer);
    signInTimer = undefined;
    if (state.Error) {
        showAccount("out");
        el("account-note").textContent = state.Error;
    } else {
        await loadAccount();
        if (state.Done) await loadSettings();
    }
}

el("sign-in").addEventListener("click", async () => {
    const state = await HostedService.SignIn("");
    if (state.Error) {
        el("account-note").textContent = state.Error;
        return;
    }
    await watchSignIn();
    signInTimer = window.setInterval(watchSignIn, 2000);
});

el("sign-in-cancel").addEventListener("click", async () => {
    await HostedService.Cancel();
    await watchSignIn();
});

el("sign-out").addEventListener("click", async () => {
    await HostedService.SignOut();
    await loadAccount();
    await loadSettings();
});

/* Status ------------------------------------------------------------ */

async function refreshStatus() {
    try {
        const daw = await AgentService.GetDAWStatus();
        status.dataset.connected = String(daw.Connected);
        statusText.textContent = daw.Connected ? "DAW connected" : "DAW not answering";
        status.title = daw.Detail;
    } catch {
        status.dataset.connected = "false";
        statusText.textContent = "Backend not responding";
    }
}

refreshStatus();
setInterval(refreshStatus, statusInterval);
loadSettings();
loadAccount();

// The window draws what the backend already holds, so reopening it after a
// reload shows the conversation rather than an empty room.
AgentService.CurrentConversation().then((current) => {
    drawThread(current.Messages ?? [], current.ID);
    renderConversations();
});

input.focus();
