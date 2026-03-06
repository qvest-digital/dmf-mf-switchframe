// Package control provides the REST API and state broadcast for the
// Switchframe video switcher.
//
// The [API] type exposes HTTP endpoints for all switcher commands (cut,
// preview, transition, FTB, audio mixing, recording, SRT output, graphics,
// macros, replay, and operator management). Commands use POST with JSON
// bodies; queries use GET returning JSON. Routes are registered via
// [API.RegisterOnMux] and support both /api/ and /api/v1/ prefixes.
//
// Key types:
//   - [API]: HTTP handler registry and dependency wiring
//   - [StatePublisher]: JSON serialization of control room state for MoQ broadcast
//
// State broadcast uses a callback-driven model: the switcher and audio mixer
// invoke the state callback after each mutation, which serializes the full
// [internal.ControlRoomState] to JSON and publishes it on the MoQ "control"
// track. Late-joining clients receive the latest full snapshot.
package control
