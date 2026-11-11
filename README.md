# luna-pages

A small CMS for every application registered in luna-dashboard: the application's site,
layouts, templates with typed fields and pages, designed in GrapesJS. Every `/v1` request carries
the application's API key in `X-API-Key`, checked through [luna-keys](../luna-keys) against the
dashboard; the key decides the application. Design: the luna-pages design doc (Site / Layout /
Template / Page, `content` and `metadata`).

An API-only [fullstack](https://github.com/shibisty/fullstack.go) application on Postgres.

State: stage 2 of the design. The editor contract works (drafts); publishing, rendering and the
public read API (`pages:read`, browser keys allowed) come next.

## The editor contract

What the GrapesJS plugins `grapesjs-pages` (v2) and `grapesjs-storage` talk to, under
`/v1/editor` with a server key holding `pages:write` (a client's admin calls it through its
backend), and under `/internal/v1/apps/{app}/editor` for the dashboard's editor.

| Request | |
|---|---|
| `GET/PUT /v1/editor/site` | the site: name, settings, its draft project (global CSS, symbols) |
| `GET /v1/editor/{layouts\|templates\|pages}` | `{"items": [headers]}`, no projects |
| `GET /v1/editor/{kind}/{id}` | the record with its draft project in `data`, a page's draft `values` |
| `PUT /v1/editor/{kind}/{id}` | creates (201) or replaces (200) |
| `DELETE /v1/editor/{kind}/{id}` | 204; 409 for a template that pages use |

- **Ids** are UUIDs the editor generates; records are keyed by application and id, so ids of
  two applications never meet.
- **Versions**: every record has a `version` (also the `ETag`) that changes on every write;
  `If-Match: "7"` makes a write or delete conditional (`412` when it moved on), as
  grapesjs-pages' sync expects. Without `If-Match` a write is unconditional.
- **grapesjs-pages v1** works unchanged with the base URL `…/v1/editor` and the key in its
  `headers` option: it sends `{id, name, data}` to `/pages/{id}`; the page gets a slug from its
  name (`/home-sweet-home`, then `-2`…).
- **Pages**: `slug` (`/` or `/a/b-c`, unique per application), `layout_id`, `parent_id`,
  `position`; with `template_id` a page is an *entry* of that template. `values` sets keys
  (`field.price`, `seo.description`, the client's own), `null` deletes one, keys not sent stay.
- **Templates**: `fields` = `[{name, type, label, required, default, options, format, fields}]`;
  types `text`, `richtext`, `image`, `link`, `number`, `boolean`, `date`, `select`, `list` (a
  repeater with its item's fields, one level).
- **Limits**: 4 MiB a request, 2 MiB a project, 1,000 records of a kind, 100 fields, 200 values
  of 64 KiB.

## Documentation

- **OpenAPI 3.1**: [`docs/openapi.yaml`](docs/openapi.yaml) at `GET /openapi.yaml`,
  [`docs/internal.yaml`](docs/internal.yaml) at `GET /internal/openapi.yaml`; Swagger UI at
  `/docs` and `/internal/docs` with `API_DOCS=true`. A test keeps both in step with the routes.
- **Postman**: [`docs/postman/luna-pages.postman_collection.json`](docs/postman/luna-pages.postman_collection.json)
  with [`docs/postman/luna-local.postman_environment.json`](docs/postman/luna-local.postman_environment.json);
  set `apiKey` to a server key with `pages:write`.

## Run

From the workspace root (`luna/`): `gtr install`. Then here:

```bash
cp .env.example .env     # DATABASE_DSN, LUNA_DASHBOARD_URL, LUNA_DASHBOARD_TOKEN
gtr dev                  # http://localhost:8082
```

## Tests

```bash
TEST_DATABASE_DSN=postgres://… go test ./...
```

The database tests need an empty Postgres database (15 or newer) and are skipped without it.

## License

MIT
