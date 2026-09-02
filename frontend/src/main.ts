import { AgentService, SettingsService } from "../bindings/tonelab";
import type { AgentResponse, JournalEntry, Settings } from "../bindings/tonelab/models";

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

// How stale the connection light may be. The backend decides what counts as
// connected; this only decides how often it is asked.
const statusInterval = 3000;

/* Theme -------------------------------------------------------------- */

// Applied to the root rather than swapped stylesheet, so a change is one
// attribute and the transition is free. "system" leaves the attribute off and
// lets the media query decide.
let chosenTheme = "system";

function applyTheme(name: string) {
    chosenTheme = name;
    const dark = name === "dark" || (name === "system" && matchMedia("(prefers-color-scheme: dark)").matches);
    document.documentElement.dataset.theme = dark ? "dark" : "light";

    for (const button of document.querySelectorAll<HTMLButtonElement>(".choice")) {
        button.setAttribute("aria-pressed", String(button.dataset.theme === name));
    }
}

matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => {
    if (chosenTheme === "system") {
        applyTheme("system");
    }
});

for (const button of document.querySelectorAll<HTMLButtonElement>(".choice")) {
    // Applied at once rather than on save: a theme you cannot see until you
    // commit to it is one you cannot choose.
    button.addEventListener("click", () => applyTheme(button.dataset.theme!));
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
    }
    if (view === "settings") {
        loadSettings();
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
    said.textContent = text;

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

    try {
        const response = previewMode.checked
            ? await AgentService.PreviewCommand(text)
            : await AgentService.SendCommand(text);
        waiting.remove();
        report(response);
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
    await AgentService.Forget();
    thread.querySelectorAll(".msg").forEach((node) => node.remove());
    empty.hidden = false;
    input.focus();
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
            line.textContent = describeStep(step.Tool, step.Arguments, step.Outcome, step.Failed);
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
    outcome.className = entry.Error ? "said" : "";
    outcome.textContent = entry.Error
        ? `${entry.Error.Code}: ${entry.Error.Message}`
        : entry.Answer;
    if (entry.Error) {
        outcome.style.color = "var(--alarm)";
    }
    turn.append(outcome);
    return turn;
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
        default:
            said = tool;
    }
    if (failed) {
        const reason = parse(outcome).error?.message;
        return `${said} — refused${reason ? `: ${reason}` : ""}`;
    }
    return said;
}

function parse(text: string): any {
    try {
        return JSON.parse(text);
    } catch {
        return {};
    }
}

/* Settings ---------------------------------------------------------- */

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
    el<HTMLInputElement>("preview-default").checked = settings.PreviewByDefault;
    previewMode.checked = settings.PreviewByDefault;
    applyTheme(settings.Theme || "system");
    el("settings-note").textContent = "";
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
    };

    const result = await SettingsService.Save(settings, el<HTMLInputElement>("api-key").value);
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
input.focus();
