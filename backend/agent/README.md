# /agent — orchestrator, tools, schemas

Highest-stakes domain in this codebase — it wraps the DAW command layer as tool-calling-safe operations with strict JSON Schema validation and structured, actionable errors.

Nothing here gets written until:
1. The Agent↔Tools JSON Schema contract is committed.


Guardrails (dry-run/preview mode, limits on destructive commands) are designed alongside the tools themselves, not bolted on later — OSC is UDP, delivery isn't guaranteed, and a live DAW project is one bad tool call away from real damage.
