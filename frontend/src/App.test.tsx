import { render, screen } from "@testing-library/react";
import { expect, test } from "vitest";
import { App } from "./App";
import { world } from "./test/fake";

test("the bar shows the DAW's answer, not a guess", async () => {
    world.daw = { Connected: false, Detail: "No feedback for 12s" };
    render(<App />);
    expect(await screen.findByText("DAW not answering")).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Command" })).toBeInTheDocument();
});
