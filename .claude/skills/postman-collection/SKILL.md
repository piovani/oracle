---
name: postman-collection
description: Create or update docs/postman-collection.json, the Postman collection that documents the oracle HTTP API (`oracle api`). Use when a route is added, changed or removed in internal/command/api.go or internal/handler, when asked to document the API, or to generate, refresh or test the Postman collection.
---

# Postman collection of the oracle API

`docs/postman-collection.json` is the shared documentation of the HTTP API: a single
Postman Collection **v2.1** file that anyone imports with Postman's *Import* button.
Keep it a single file in `docs/`; do not create a `postman/` directory or v3 YAML files.

The helper script runs from the repository root (Docker must be running for `capture` and `test`):

```
python3 -I .claude/skills/postman-collection/scripts/api.py check                  # routes vs collection, conventions
python3 -I .claude/skills/postman-collection/scripts/api.py capture GET /x POST /x # real responses, for examples
python3 -I .claude/skills/postman-collection/scripts/api.py test                   # check + run the collection
```

`capture` and `test` build the API from the working tree and run it on a free port against
`postgres-test` (port 5433), never the `DB_*` database. Use them instead of `curl`: on this
machine `curl -X` gets `400 Bad Request` from the server even when the API is fine.

## Workflow

1. **Find the routes.** They are registered in `internal/command/api.go`
   (`mux.HandleFunc("GET /path", handler.X)`); the handlers are in `internal/handler`.
   Read the handler to learn inputs, outputs, status codes and side effects.
   Run `check` to list what is missing or stale in the collection.
2. **Capture real behavior.** Run `capture` for the success case and for each error a caller
   must handle: the wrong method (Go's mux answers `405` with `Allow`), invalid input,
   missing resources. Examples come from these responses, never from guesses. Volatile
   headers (`Date`, `Content-Length`) are already dropped.
3. **Edit the collection** (conventions below). Edit the JSON in place and keep the
   existing items, descriptions and tests; change only what the route change requires.
4. **Verify.** Run `test`: it must print `check ok` and finish with 0 failed assertions.
   `postman collection lint` does not validate v2.1 JSON ("Scanned: 0"), so do not
   report it as validation.
5. If a route is new, mention it in the "HTTP API" section of `README.md`.

The Swagger spec (`docs/swagger.*`, served at `/swagger/`) documents the same routes and is
generated from swag annotations on the handlers with `make swagger`. When a route changes,
update its annotations and regenerate too, so the two documents agree; `/swagger/` itself is
not an API route and is not in the collection (`check` skips it).

## Conventions

- `info.schema` is `https://schema.getpostman.com/json/collection/v2.1.0/collection.json`;
  `info.name` is `oracle API`; `info.description` explains startup (`PORT`, DB connection
  before listening), auth (none today) and the 404/405 behavior. Update it when that changes.
- One collection variable, `baseUrl` (default `http://localhost:8080`, no trailing slash).
  Every URL is `{{baseUrl}}/...`, written as `raw` + `host: ["{{baseUrl}}"]` + `path`.
  Path parameters use `:name` in `raw`/`path` plus a `variable` entry with an example value.
- Item `name` is a short action in English ("Health check", "List quotes"). Group items
  in folders only once there are several routes per area.
- `request.description` (Markdown, English) says what the route does, its inputs (required
  vs optional, formats), the response guarantees, the errors and whether it has side effects.
  Write only what the code or a captured response shows.
- A `test` event per request, with `pm.test` assertions on the status code, the
  `Content-Type` and the shape of the body (required fields and formats, not exact values
  that change, like timestamps).
- `response` holds the saved examples: at least the success case and one error case, named
  `<code> <reason>` ("200 OK", "405 Method Not Allowed"). Each has `originalRequest` (the
  method that produced it), `code`, `status`, `header`, `body` (string, as captured, with
  the trailing newline Go's encoder writes) and `_postman_previewlanguage` (`json` or `text`).
- No secrets in the file. If auth is added, reference it with a variable (`{{apiToken}}`)
  left empty in the collection and set it in Postman, not here.
- Write JSON with 2-space indentation, UTF-8, ending in a newline.
