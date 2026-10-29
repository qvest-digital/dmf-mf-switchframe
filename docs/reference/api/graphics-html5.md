# API: HTML5 graphics

> **Where you are:** [docs](../../../README.md) → reference → api → graphics-html5
> **Read this first:** [api.md](../api.md)
> **See also:** [html5-graphics.md](../../integration/cef-protocol.md) · [integration/cef-protocol.md](../../integration/cef-protocol.md) · [api/graphics.md](graphics.md)

**TL;DR** Eight endpoints drive the out-of-process CEF (Chromium Embedded Framework) sidecar for browser-rendered graphics. A template catalog is exposed for discovery; per-layer load/play/stop/next/custom/data-update and unload endpoints wire one browser instance to one graphics layer. Returns empty responses when the CEF sidecar is not configured.

## Endpoints

### `GET /api/graphics/html5/templates`

**Purpose:** list available HTML5 templates discovered by the CEF sidecar.
**Handler:** [`control/api_html5gfx.go`](../../../server/control/api_html5gfx.go) → `(*API).handleHTML5Templates`.

**Response 200:** `[]htmlgfx.TemplateInfo`. Empty array when no CEF sidecar is configured (rather than 501).

---

### `POST /api/graphics/{id}/html5`

**Purpose:** allocate a browser instance for the layer and load a URL.
**Handler:** `(*API).handleHTML5Load`.

**Request body** (`control.html5LoadRequest`):

```json
{ "url": "https://example.com/lowerthird.html", "data": { "title": "John Doe" } }
```

The `url` must be `https`, or a `file` URL below the templates directory (`--cef-templates-dir`) after symlinks are resolved; anything else, plain `http` included, is refused with 400 before a browser is allocated. The sidecar is an unsandboxed Chromium whose pixels go to program, so a `file` URL elsewhere would put a mounted secret on air and an `http` URL reaches cluster-internal services. The sidecar holds every navigation and resource load of its browsers to the same rule, redirects and frames included, so a template's own script cannot reach past it either. Requests a service worker makes are held to it too, and popups are refused. It lets through `wss`, and `data`, `blob`, `about:blank` and `about:srcdoc`, which carry content the page already holds.

The `data` field is forwarded to the sidecar which passes it to the template via the OGraf adapter, and sent again once the sidecar first reports a format other than `unknown` for the page: data sent with a load reaches the page before its adapter exists. See [html5-graphics.md](../../integration/cef-protocol.md).

An OGraf graphic published as a manifest and a module, with no page of its own, is loaded through the `ograf-host` template: its URL with `?manifest=<https manifest URL>` appended. The host page fetches the manifest, imports its `main` module relative to it, both over `https` only, and loads the graphic in real-time mode. A missing or refused manifest, a module that does not load, or a `load()` that does not answer 2xx sets the layer's `html5Status` to `error`, and the engine logs the reason.

**Errors:** 400 (bad id, invalid JSON, URL refused), 503 (sidecar not running), 409 (browser pool exhausted), plus any CEF allocation failure.

---

### `DELETE /api/graphics/{id}/html5`

**Purpose:** stop and free the browser instance on a layer.
**Handler:** `(*API).handleHTML5Unload`.

**Response 204.**

---

### `PUT /api/graphics/{id}/html5/data`

**Purpose:** update the template's data model without reloading the page.
**Handler:** `(*API).handleHTML5UpdateData`.

**Request body** (`control.html5UpdateDataRequest`): `{ "data": { "title": "Jane Doe" } }`.

---

### `POST /api/graphics/{id}/html5/play`

**Purpose:** trigger the template's play animation.
**Handler:** `(*API).handleHTML5Play`.

---

### `POST /api/graphics/{id}/html5/stop`

**Purpose:** trigger the template's stop animation.
**Handler:** `(*API).handleHTML5Stop`.

---

### `POST /api/graphics/{id}/html5/next`

**Purpose:** advance the template to the next scene (for multi-page templates).
**Handler:** `(*API).handleHTML5Next`.

---

### `POST /api/graphics/{id}/html5/custom`

**Purpose:** invoke a template-defined custom action.
**Handler:** `(*API).handleHTML5Custom`.

**Request body** (`control.html5CustomRequest`): `{ "action": "highlight", "payload": { "color": "#ff0" } }`.

## Related docs

- **Reference:** [api.md](../api.md) · [api/graphics.md](graphics.md)
- **Integration:** [cef-protocol.md](../../integration/cef-protocol.md)
- **Concepts:** [html5-graphics.md](../../integration/cef-protocol.md)
