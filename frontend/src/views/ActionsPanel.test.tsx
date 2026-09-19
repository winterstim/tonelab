import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, test } from "vitest";
import { App } from "@/App";
import { world } from "@/test/fake";

test("recent actions open beside the thread and read as sentences", async () => {
    world.journal = [{
        At: "21:04", Command: "Mute the vocals", Answer: "Muted **Vocals**.", Preview: false, Error: null,
        Steps: [
            { Tool: "list_tracks", Arguments: "{}", Outcome: "[...]", Failed: false },
            { Tool: "set_param", Arguments: '{"track_id":2,"param_name":"mute","value":true}', Outcome: "{}", Failed: false },
            { Tool: "set_param", Arguments: '{"track_id":9,"param_name":"mute","value":true}', Outcome: '{"error":{"message":"track 9 does not exist"}}', Failed: true },
        ],
    }, {
        At: "21:00", Command: "Louder", Answer: "", Preview: true, Error: null,
        Steps: [{ Tool: "set_param", Arguments: '{"track_id":1,"param_name":"volume","value":0.9}', Outcome: "", Failed: false }],
    }];
    const user = userEvent.setup();
    render(<App />);
    await screen.findByRole("textbox", { name: "Command" });
    await user.click(screen.getByRole("button", { name: "Recent actions" }));
    const panel = within(await screen.findByRole("complementary", { name: "Recent actions" }));
    expect(await panel.findByText("Looked up the tracks")).toBeInTheDocument();
    expect(panel.getByText("Set mute on track 2 to on")).toBeInTheDocument();
    expect(panel.getByText("Set mute on track 9 to on, refused: track 9 does not exist")).toBeInTheDocument();
    expect(panel.getByText("Vocals")).toBeInTheDocument();
    expect(panel.getByText("proposed only")).toBeInTheDocument();
    expect(panel.getAllByText("Exact calls")).toHaveLength(2);
    await user.click(panel.getByRole("button", { name: "Close recent actions" }));
    await waitFor(() => expect(screen.queryByRole("complementary", { name: "Recent actions" })).not.toBeInTheDocument());
});

test("a conversation can be renamed and deleted from its row in the sidebar", async () => {
    world.threads = [
        { ID: "a", Title: "Mute the vocals", Started: "", Messages: [] },
        { ID: "b", Title: "Louder drums", Started: "", Messages: [] },
    ];
    world.active = "a";
    const user = userEvent.setup();
    render(<App />);
    await screen.findByRole("textbox", { name: "Command" });
    await user.click(await screen.findByRole("button", { name: "More for Louder drums" }));
    await user.click(await screen.findByRole("menuitem", { name: "Rename" }));
    const field = await screen.findByRole("textbox", { name: "Conversation name" });
    await user.clear(field);
    await user.type(field, "Drums up{Enter}");
    expect(await screen.findByRole("button", { name: "More for Drums up" })).toBeInTheDocument();
    expect(world.threads[1].Title).toBe("Drums up");

    await waitFor(() => expect(screen.queryByRole("menu")).not.toBeInTheDocument());
    // Opened from the keyboard: user-event's synthetic pointer does not
    // reopen a Radix menu in jsdom after a field unmounted mid-click, and
    // the mouse path is checked in a real browser instead.
    screen.getByRole("button", { name: "More for Mute the vocals" }).focus();
    await user.keyboard("{Enter}");
    await user.click(await screen.findByRole("menuitem", { name: "Delete" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "More for Mute the vocals" })).not.toBeInTheDocument());
    expect(world.threads.map((t) => t.ID)).toEqual(["b"]);
});
