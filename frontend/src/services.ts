// The one door to the backend. Every screen imports from here, and the
// test config points this path at the fake, so no component knows whether
// it is talking to Wails or to a stand-in.
export { AgentService, HostedService, MCPService, SettingsService } from "../bindings/github.com/winterstim/tonelab/internal/app";
export type * from "../bindings/github.com/winterstim/tonelab/internal/app/models";
export type { Window as UsageWindow } from "../bindings/github.com/winterstim/tonelab/internal/hosted/models";
export type { Usage } from "../bindings/github.com/winterstim/tonelab/internal/agent/models";
