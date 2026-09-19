import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, test } from "vitest";
import { App } from "@/App";
import { world } from "@/test/fake";

async function openHistory() {
    const user = userEvent.setup();
    render(<App />);
    await screen.findByRole("textbox", { name: "Command" });
    await user.click(screen.getByRole("tab", { name: "History" }));
    return user;
}

test("turns read as sentences, with the exact calls behind a disclosure", async () => {
    world.journal = [{
        At: "21:04",
        Command: "Mute the vocals",
        Answer: "Muted **Vocals**.",
        Preview: false,
        Error: null,
        Steps: [
            { Tool: "list_tracks", Arguments: "{}", Outcome: "[...]", Failed: false },
            { Tool: "set_param", Arguments: '{"track_id":2,"param_name":"mute","value":true}', Outcome: "{}", Failed: false },
            { Tool: "set_param", Arguments: '{"track_id":9,"param_name":"mute","value":true}', Outcome: '{"error":{"message":"track 9 does not exist"}}', Failed: true },
        ],
    }, {
        At: "21:00", Command: "Louder", Answer: "", Preview: true, Error: null,
        Steps: [{ Tool: "set_param", Arguments: '{"track_id":1,"param_name":"volume","value":0.9}', Outcome: "", Failed: false }],
    }];
    await openHistory();
    expect(await screen.findByText("Looked up the tracks")).toBeInTheDocument();
    expect(screen.getByText("Set mute on track 2 to on")).toBeInTheDocument();
    expect(screen.getByText("Set mute on track 9 to on, refused: track 9 does not exist")).toBeInTheDocument();
    expect(screen.getByText("Vocals")).toBeInTheDocument();
    expect(screen.getByText("proposed only")).toBeInTheDocument();
    expect(screen.getAllByText("Exact calls")).toHaveLength(2);
});

test("a conversation can be renamed and deleted on its row", async () => {
    world.threads = [
        { ID: "a", Title: "Mute the vocals", Started: "", Messages: [] },
        { ID: "b", Title: "Louder drums", Started: "", Messages: [] },
    ];
    world.active = "a";
    const user = await openHistory();
    await user.click(await screen.findByRole("button", { name: "Rename Louder drums" }));
    const field = screen.getByRole("textbox", { name: "Conversation name" });
    await user.clear(field);
    await user.type(field, "Drums up{Enter}");
    expect(await screen.findByRole("button", { name: "Rename Drums up" })).toBeInTheDocument();
    expect(world.threads[1].Title).toBe("Drums up");

    await user.click(screen.getByRole("button", { name: "Delete Mute the vocals" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "Delete Mute the vocals" })).not.toBeInTheDocument());
    expect(world.threads.map((t) => t.ID)).toEqual(["b"]);
});

test("opening a chat from History changes what Chat shows", async () => {
    world.threads = [
        { ID: "a", Title: "First", Started: "", Messages: [{ From: "you", Text: "First question", Changed: null, Plan: null, Error: null }] },
        { ID: "b", Title: "Second", Started: "", Messages: [{ From: "you", Text: "Second question", Changed: null, Plan: null, Error: null }] },
    ];
    world.active = "b";
    const user = await openHistory();
    const chats = (await screen.findByText("Chats")).parentElement!;
    await user.click(within(chats).getByText("First"));
    await user.click(screen.getByRole("tab", { name: "Chat" }));
    expect(await screen.findByText("First question")).toBeInTheDocument();
    expect(screen.queryByText("Second question")).not.toBeInTheDocument();
});
