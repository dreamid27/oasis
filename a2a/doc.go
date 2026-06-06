// Package a2a implements the A2A (Agent2Agent) v1.0 protocol: expose any
// core.Agent as an A2A server (NewServer) and consume remote A2A agents as
// core.Agent values (Dial). JSON-RPC 2.0 + SSE + REST transports, no external
// dependencies.
//
// A2A is the Linux Foundation's open standard for agent-to-agent interop:
// agents built on different frameworks delegate work to each other through
// stateful tasks over HTTP. This package owns the protocol types, the wire
// constants, the task state machine, and the transports; governance, identity,
// routing, and the HTTP listener belong to the application.
//
// Wire format. The package implements the JSON binding of the A2A v1.0
// specification (the protobuf definition is the source of truth for shapes,
// but where examples and proto disagree the JSON binding wins). Field names
// are camelCase; enum values are SCREAMING_SNAKE_CASE per the ProtoJSON
// convention adopted in A2A ADR-001. Large artifact and data payloads travel
// as json.RawMessage end to end so they pass through without re-encoding.
//
// See https://a2a-protocol.org for the canonical specification.
package a2a
