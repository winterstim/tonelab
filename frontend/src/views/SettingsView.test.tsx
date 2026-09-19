import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, test } from "vitest";
import { App } from "@/App";
import { world } from "@/test/fake";
import { whenResets } from "@/lib/format";

async function openSettings() {
    const user = userEvent.setup();
    render(<App />);
    await screen.findByRole("textbox", { name: "Command" });
    await user.click(screen.getByRole("button", { name: "Settings" }));
    await screen.findByRole("button", { name: "Save" });
    return user;
}

test("saving sends the form and never a key it did not type", async () => {
    world.apiKey = "tl_secret";
    const user = await openSettings();
    expect(await screen.findByText(/A key is saved/)).toBeInTheDocument();
    const key = screen.getByLabelText(/^API key/);
    expect(key).toHaveValue("");

    await user.clear(screen.getByLabelText("Model"));
    await user.type(screen.getByLabelText("Model"), "gpt-oss-20b");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText("Saved.")).toBeInTheDocument();
    expect(world.saves).toHaveLength(1);
    expect(world.saves[0].settings.Model).toBe("gpt-oss-20b");
    expect(world.saves[0].apiKey).toBe("");
    expect(world.apiKey).toBe("tl_secret");
});

test("a refused save shows the reason and keeps the edits", async () => {
    const user = await openSettings();
    await user.clear(screen.getByLabelText(/^Endpoint/));
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("The endpoint cannot be empty.")).toHaveClass("text-destructive");
    expect(screen.getByLabelText(/^Endpoint/)).toHaveValue("");
});

test("half-typed edits survive a look at the chat", async () => {
    const user = await openSettings();
    await user.type(screen.getByLabelText("Host"), "9");
    await user.click(screen.getByRole("button", { name: "Start a new conversation" }));
    await user.click(screen.getByRole("button", { name: "Settings" }));
    expect(screen.getByLabelText("Host")).toHaveValue("127.0.0.19");
});

test("a DAW change says it needs a restart", async () => {
    const user = await openSettings();
    await user.selectOptions(screen.getByLabelText("Backend"), "ableton");
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent(/after a restart/));
});

test("the theme applies at once and is saved", async () => {
    const user = await openSettings();
    await user.click(screen.getByRole("button", { name: "dark" }));
    expect(document.documentElement.dataset.theme).toBe("dark");
    await user.click(screen.getByRole("button", { name: "Save" }));
    await screen.findByText("Saved.");
    expect(world.saves[0].settings.Theme).toBe("dark");
});

test("search fields show only when a provider is chosen", async () => {
    const user = await openSettings();
    expect(screen.queryByLabelText(/Search API key/)).not.toBeInTheDocument();
    await user.selectOptions(screen.getByLabelText("Provider"), "brave");
    expect(screen.getByLabelText(/Search API key/)).toBeInTheDocument();
});

test("signing in shows the code, and the signed-in face when the browser approves", async () => {
    const user = await openSettings();
    await user.click(screen.getByRole("button", { name: "Sign in with Tonelab" }));
    expect(await screen.findByText("ABCD-EFGH")).toBeInTheDocument();

    world.signIn = { Running: false, Done: true, UserCode: "", VerifyURL: "", Error: "" };
    world.hosted = {
        ...world.hosted, SignedIn: true, Email: "web3@example.com", Plan: "Studio", Active: true,
        Month: { used: 2_500_000, limit: 5_000_000, resets_at: new Date(Date.now() + 20 * 86400e3).toISOString() },
        Day: { used: 450_000, limit: 500_000, resets_at: new Date(Date.now() + 5 * 3600e3).toISOString() },
        Searches: { used: 300, limit: 300, resets_at: new Date(Date.now() + 5 * 3600e3).toISOString() },
    };
    world.settings = { ...world.settings, BaseURL: "https://api.tonelab.dev/v2", Model: "tonelab" };

    expect(await screen.findByText("web3@example.com", {}, { timeout: 4000 })).toBeInTheDocument();
    expect(screen.getByText("on the Studio plan")).toBeInTheDocument();
    expect(screen.getByRole("progressbar", { name: "Today" })).toBeInTheDocument();
    expect(screen.getByText(/90%, resets in 5 h/)).toBeInTheDocument();
    expect(screen.getByText(/100%, resets in 5 h/)).toBeInTheDocument();
    // The endpoint the sign-in adopted is what the form now shows.
    expect(screen.getByLabelText(/^Endpoint/)).toHaveValue("https://api.tonelab.dev/v2");
});

test("cancelling the sign-in returns to the signed-out face", async () => {
    const user = await openSettings();
    await user.click(screen.getByRole("button", { name: "Sign in with Tonelab" }));
    await screen.findByText("ABCD-EFGH");
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(await screen.findByRole("button", { name: "Sign in with Tonelab" })).toBeInTheDocument();
});

test("signing out reloads the settings the sign-in had changed", async () => {
    world.hosted = { ...world.hosted, SignedIn: true, Email: "web3@example.com", Plan: "", Active: false, Reason: "No plan is active." };
    const user = await openSettings();
    const account = (await screen.findByText("Tonelab account")).parentElement!;
    expect(within(account).getByText("no plan yet")).toBeInTheDocument();
    expect(within(account).getByText("No plan is active.")).toBeInTheDocument();
    world.settings = { ...world.settings, BaseURL: "http://localhost:11434/v1" };
    await user.click(screen.getByRole("button", { name: "Sign out" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Sign in with Tonelab" })).toBeInTheDocument());
});

test("reset times read as hours soon and as a date later", () => {
    const now = Date.UTC(2026, 8, 19, 12);
    expect(whenResets(new Date(now + 3 * 3600e3).toISOString(), now)).toBe("resets in 3 h");
    expect(whenResets(new Date(now - 1000).toISOString(), now)).toBe("resets now");
    expect(whenResets(new Date(Date.UTC(2026, 9, 1, 12)).toISOString(), now)).toMatch(/^resets on /);
    expect(whenResets("", now)).toBe("resets now");
});

test("the sidebar's account row starts signing in when signed out", async () => {
    const user = userEvent.setup();
    render(<App />);
    await screen.findByRole("textbox", { name: "Command" });
    const sidebar = within(screen.getByRole("complementary", { name: "Sidebar" }));
    await user.click(await sidebar.findByRole("button", { name: /^Sign in with Tonelab/ }));
    expect(await screen.findByText("ABCD-EFGH")).toBeInTheDocument();
});

test("a newer release is offered as a download on the site", async () => {
    world.update = { Current: "v0.1.0", Latest: "v0.2.0", Available: true, Error: "" };
    const user = await openSettings();
    await user.click(await screen.findByRole("button", { name: "v0.2.0 is available, download" }));
    expect(world.opened).toEqual(["/download"]);
});
