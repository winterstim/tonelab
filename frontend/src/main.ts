import { AgentService, TransportService } from "../bindings/tonelab";
import type { AgentResponse } from "../bindings/tonelab/models";

const form = document.getElementById("command-form") as HTMLFormElement;
const input = document.getElementById("command-input") as HTMLInputElement;
const send = document.getElementById("command-send") as HTMLButtonElement;
const answer = document.getElementById("answer") as HTMLElement;
const previewMode = document.getElementById("preview-mode") as HTMLInputElement;
const history = document.getElementById("history") as HTMLDetailsElement;
const historyBody = document.getElementById("history-body") as HTMLElement;
const apply = document.getElementById("apply") as HTMLButtonElement;
const status = document.getElementById("daw-status") as HTMLElement;
const statusText = document.getElementById("daw-status-text") as HTMLElement;

// How often to ask whether the DAW is still there. The backend decides what
// counts as connected; this only decides how stale the display may be.
const statusInterval = 2000;

type Tone = "answer" | "problem" | "working";

function show(text: string, tone: Tone) {
    answer.textContent = text;
    answer.dataset.tone = tone;
}

// The backend distinguishes its failures by code so they can be acted on
// differently. Only a few change what the user should do next; the rest carry
// a message already written for them.
function explain(code: string, message: string): string {
    switch (code) {
        case "llm_unreachable":
            return `${message} Check the endpoint in your config file, and that a local model is running.`;
        case "llm_unauthorized":
            return `${message} Check the API key in your config file.`;
        case "daw_command_failed":
            return `${message} Check the DAW is running and listening for OSC.`;
        default:
            return message;
    }
}

// A change the DAW did not report is shown as unconfirmed rather than
// omitted, since silence about it is what a user would read as success.
function describe(changed: AgentResponse["Changed"]): string {
    if (!changed || changed.length === 0) {
        return "";
    }
    const lines = changed.map((change) => {
        const target = `track ${change.Track} ${change.Param}`;
        return change.NewValue === null || change.NewValue === undefined
            ? `• ${target}: sent ${format(change.Requested)}, not confirmed by the DAW`
            : `• ${target}: ${format(change.NewValue)}`;
    });
    return "\n\n" + lines.join("\n");
}

// Shown as intentions rather than results, since nothing has happened yet.
function proposed(plan: AgentResponse["Plan"]): string {
    if (!plan || plan.length === 0) {
        return "";
    }
    return "\n\nWould:\n" + plan.map((step) => `• ${step.Description}`).join("\n");
}

function format(value: unknown): string {
    if (typeof value === "boolean") {
        return value ? "on" : "off";
    }
    return String(value);
}

form.addEventListener("submit", async (event) => {
    event.preventDefault();

    const text = input.value.trim();
    if (text === "") {
        return;
    }

    // Disabled while working, since a second command sent mid-flight would
    // reach a DAW whose state the first has already changed.
    send.disabled = true;
    show("Working…", "working");

    try {
        const response = previewMode.checked
            ? await AgentService.PreviewCommand(text)
            : await AgentService.SendCommand(text);

        // A plan is offered for acceptance rather than applied, and the button
        // stays hidden when the turn proposed nothing to accept.
        apply.hidden = !response.Plan || response.Plan.length === 0;

        if (response.Error) {
            show(explain(response.Error.Code, response.Error.Message), "problem");
        } else {
            // The model's summary, then what the DAW actually confirmed. The
            // model is the one account of the turn that cannot check itself,
            // so it is shown beside the DAW's rather than instead of it.
            show(response.Message + describe(response.Changed) + proposed(response.Plan), "answer");
            if (!previewMode.checked) {
                input.value = "";
            }
        }
    } catch (error) {
        // Reaching here means the call itself broke rather than the command
        // failing, which the backend reports inside the response instead.
        // A rejected binding call can carry an empty message, and appending
        // nothing reads as a truncated sentence.
        const reason = (error instanceof Error ? error.message : String(error)).trim();
        show(reason
            ? `The backend could not be reached. ${reason}`
            : "The backend could not be reached. Restart the app if this persists.", "problem");
    } finally {
        send.disabled = false;
        input.focus();
        refreshHistory();
    }
});

// Rendered as text rather than parsed into prose: a history that interprets
// what happened is another account to be wrong, and the raw call is what
// someone checking the agent actually wants.
async function refreshHistory() {
    const entries = await AgentService.History();
    if (entries.length === 0) {
        history.hidden = true;
        return;
    }
    history.hidden = false;
    historyBody.textContent = entries.map((entry) => {
        const head = `${entry.At}  ${entry.Preview ? "[preview] " : ""}${entry.Command}`;
        const steps = (entry.Steps ?? []).map(
            (step) => `    ${step.Failed ? "✗" : "→"} ${step.Tool} ${step.Arguments}\n      ${step.Outcome}`);
        const outcome = entry.Error
            ? `    ✗ ${entry.Error.Code}: ${entry.Error.Message}`
            : `    ${entry.Answer}`;
        return [head, ...steps, outcome].join("\n");
    }).join("\n\n");
}

async function refreshStatus() {
    try {
        const daw = await AgentService.GetDAWStatus();
        status.dataset.connected = String(daw.Connected);
        statusText.textContent = daw.Connected ? "DAW connected" : daw.Detail;
    } catch {
        status.dataset.connected = "false";
        statusText.textContent = "Backend not responding";
    }
}

apply.addEventListener("click", async () => {
    apply.disabled = true;
    try {
        const response = await AgentService.ApplyPlan();
        if (response.Error) {
            show(explain(response.Error.Code, response.Error.Message), "problem");
        } else {
            show(response.Message + describe(response.Changed), "answer");
            input.value = "";
        }
    } finally {
        // One acceptance per plan: the button returns only with a new preview.
        apply.disabled = false;
        apply.hidden = true;
        refreshHistory();
    }
});

document.getElementById("undo")!.addEventListener("click", async () => {
    const response = await AgentService.Undo();
    if (response.Error) {
        show(explain(response.Error.Code, response.Error.Message), "problem");
    } else {
        show(response.Message, "answer");
    }
    refreshHistory();
});

document.getElementById("transport-play")!.addEventListener("click", async () => {
    show(await TransportService.Play(), "answer");
});
document.getElementById("transport-stop")!.addEventListener("click", async () => {
    show(await TransportService.Stop(), "answer");
});

refreshStatus();
refreshHistory();
setInterval(refreshStatus, statusInterval);
input.focus();
