import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, test } from "vitest";
import { App } from "@/App";
import { world } from "@/test/fake";

async function open() {
    render(<App />);
    return await screen.findByRole("textbox", { name: "Command" });
}

test("a command is sent, and the DAW's confirmation is shown apart from the answer", async () => {
    const user = userEvent.setup();
    const input = await open();
    world.next = {
        reply: "Track 1 is now at **a quarter**.",
        changed: [{ Track: 1, Param: "volume", Requested: 0.25, NewValue: 0.25, Note: "" }],
    };
    await user.type(input, "Set track 1 volume to a quarter{Enter}");

    expect(await screen.findByText("a quarter")).toBeInTheDocument();
    expect(screen.getByText("track 1 volume: 0.25")).toHaveClass("text-success");
    expect(screen.getByText("Set track 1 volume to a quarter")).toBeInTheDocument();
    expect(input).toHaveValue("");
});

test("an unconfirmed change is not shown as done", async () => {
    const user = userEvent.setup();
    const input = await open();
    world.next = { changed: [{ Track: 2, Param: "send 1 volume", Requested: 0.5, NewValue: null, Note: "unverified" }] };
    await user.type(input, "Send 1 up{Enter}");
    expect(await screen.findByText("track 2 send 1 volume: sent 0.50, not confirmed")).toHaveClass("text-warning");
});

test("a failed turn reads as a problem with what to do next", async () => {
    const user = userEvent.setup();
    const input = await open();
    world.next = { error: { Code: "llm_unauthorized", Message: "The endpoint refused the key." } };
    await user.type(input, "Mute the vocals{Enter}");
    expect(await screen.findByText("The endpoint refused the key. Check the API key in Settings.")).toHaveClass("text-destructive");
});

test("a preview offers the plan, and Do it applies exactly that plan", async () => {
    const user = userEvent.setup();
    const input = await open();
    await user.click(screen.getByRole("switch", { name: "Show me the plan first" }));
    world.next = {
        reply: "Here is what I would do.",
        plan: [{ Tool: "set_param", Arguments: "{}", Description: "Set volume on track 1 to 0.80" }],
    };
    await user.type(input, "Turn track 1 up{Enter}");
    expect(await screen.findByText("Set volume on track 1 to 0.80")).toBeInTheDocument();

    world.next = { changed: [{ Track: 1, Param: "volume", Requested: 0.8, NewValue: 0.8, Note: "" }] };
    await user.click(screen.getByRole("button", { name: "Do it" }));
    expect(await screen.findByText("track 1 volume: 0.80")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Do it" })).not.toBeInTheDocument();
    expect(world.plan).toBeNull();
});

test("Stop replaces Send while a turn runs", async () => {
    const user = userEvent.setup();
    const input = await open();
    let release!: () => void;
    world.next = { hold: new Promise<void>((done) => { release = done; }) };
    await user.type(input, "Something slow{Enter}");
    const stop = await screen.findByRole("button", { name: "Stop the agent" });
    expect(screen.queryByRole("button", { name: "Send" })).not.toBeInTheDocument();
    await user.click(stop);
    expect(world.stopped).toBe(1);
    release();
    expect(await screen.findByRole("button", { name: "Send" })).toBeInTheDocument();
});

test("undo goes straight to the DAW", async () => {
    const user = userEvent.setup();
    await open();
    await user.click(screen.getByRole("button", { name: "Undo last change" }));
    expect(await screen.findByText("The DAW reversed its last change.")).toBeInTheDocument();
    expect(world.undone).toBe(1);
});

test("a reply that finishes after switching conversations is not drawn in the wrong one", async () => {
    const user = userEvent.setup();
    const input = await open();
    let release!: () => void;
    world.next = { reply: "Belongs to the first.", hold: new Promise<void>((done) => { release = done; }) };
    await user.type(input, "First question{Enter}");
    await screen.findByText("Working…");

    await user.click(screen.getByRole("button", { name: "New conversation" }));
    await waitFor(() => expect(screen.queryByText("First question")).not.toBeInTheDocument());
    release();
    await waitFor(() => expect(screen.queryByText("Working…")).not.toBeInTheDocument());
    expect(screen.queryByText("Belongs to the first.")).not.toBeInTheDocument();

    // And it is waiting in the thread it was asked in.
    const trigger = screen.getAllByRole("button", { name: /New conversation/ }).find((b) => b.getAttribute("aria-haspopup") === "menu")!;
    await user.click(trigger);
    await user.click(within(await screen.findByRole("menu")).getByText("First question"));
    expect(await screen.findByText("Belongs to the first.")).toBeInTheDocument();
});

test("the conversation menu appears only with something to switch to", async () => {
    const user = userEvent.setup();
    const input = await open();
    expect(screen.queryByRole("button", { name: /New conversation/ })).toBeInTheDocument();
    await user.type(input, "One{Enter}");
    await screen.findByText("Done.");
    expect(screen.queryByRole("button", { name: /^One$/ })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "New conversation" }));
    expect(await screen.findByRole("button", { name: /New conversation/ })).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /New conversation/ })).toHaveLength(2);
});
