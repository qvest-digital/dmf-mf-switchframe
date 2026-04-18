# API: HTML5 graphics

> **Where you are:** [docs](../../../README.md) → reference → api → graphics-html5
> **Read this first:** [api.md](../api.md)
> **See also:** [html5-graphics.md](../../html5-graphics.md) · [integration/cef-protocol.md](../../integration/cef-protocol.md) · [api/graphics.md](graphics.md)

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

The `data` field is forwarded to the sidecar which passes it to the template via the OGraf adapter. See [html5-graphics.md](../../html5-graphics.md).

**Errors:** 400 (bad id, invalid JSON), 501 (sidecar not configured / not started), plus any CEF allocation failure.

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
- **Concepts:** [html5-graphics.md](../../html5-graphics.md)
