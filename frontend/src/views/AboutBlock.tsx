import { useEffect, useState } from "react";
import { HostedService, type Update } from "@/services";
import { Group, Hint } from "@/views/SettingsView";

// Which build this is and whether a newer one exists, asked once per
// visit. The download itself happens on the site: the app does not
// replace itself.
export function AboutBlock({ active }: { active: boolean }) {
    const [update, setUpdate] = useState<Update | null>(null);

    useEffect(() => {
        if (!active) return;
        let alive = true;
        HostedService.CheckUpdate().then((read) => { if (alive) setUpdate(read); });
        return () => { alive = false; };
    }, [active]);

    if (!update) return null;

    return (
        <Group title="About">
            <p className="text-[13px]">
                Tonelab {update.Current}
                {update.Available && (
                    <>
                        {" "}<span className="text-faint">·</span>{" "}
                        <button type="button" onClick={() => HostedService.OpenSite("/download")} className="underline underline-offset-4 hover:text-foreground">
                            {update.Latest} is available, download
                        </button>
                    </>
                )}
            </p>
            {update.Error ? <Hint>{update.Error}</Hint> : !update.Available && update.Latest && <Hint>Up to date.</Hint>}
        </Group>
    );
}
