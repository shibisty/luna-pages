# luna-pages

A small CMS for every application registered in luna-dashboard: the application's site,
layouts, templates with typed fields and pages, designed in GrapesJS. Every `/v1` request carries
the application's API key in `X-API-Key`, checked through [luna-keys](../luna-keys) against the
dashboard; the key decides the application. Design: the luna-pages design doc (Site / Layout /
Template / Page, `content` and `metadata`).

An API-only [fullstack](https://github.com/shibisty/fullstack.go) application on Postgres.

State: stage 3 of the design. The editor contract (drafts), publishing with revisions, and the
read API that renders pages (`pages:read`, browser keys allowed) work; the GrapesJS plugins and
the dashboard's editor come next.

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
- **grapesjs-pages v2** (its `remote` option, contract `openapi.v2.yaml`) is the editor this API
  was built for: point `remote.url` at `…/v1/editor` and put the key in `remote.headers` (from a
  backend proxy: never a server key in a browser).
- **grapesjs-pages v1** works unchanged with the base URL `…/v1/editor` and the key in its
  `headers` option: it sends `{id, name, data}` to `/pages/{id}`; the page gets a slug from its
  name (`/home-sweet-home`, then `-2`…).
- **Pages**: `slug` (`/` or `/a/b-c`, unique per application), `layout_id`, `parent_id`,
  `position`; with `template_id` a page is an *entry* of that template. `values` sets keys
  (`field.price`, `seo.description`, the client's own), `null` deletes one, keys not sent stay.
- **Templates**: `fields` = `[{name, type, label, required, default, options, format, fields}]`
  (omitted on a write: they stay; `[]` clears them);
  types `text`, `richtext`, `image`, `link`, `number`, `boolean`, `date`, `select`, `list` (a
  repeater with its item's fields, one level).
- **Limits**: 4 MiB a request, 2 MiB a project, 1,000 records of a kind, 100 fields, 200 values
  of 64 KiB.

## Publishing

| Request | |
|---|---|
| `POST /v1/editor/{kind}/{id}/publish` | `{html, css, data?}` (`editor.getHtml()`, `editor.getCss()`) becomes the live revision; an entry sends no body |
| `POST /v1/editor/site/publish` | `{css}`: the site's global CSS |
| `POST /v1/editor/pages/{id}/unpublish` | takes a page off the site |
| `GET /v1/editor/{kind}/{id}/revisions`, `GET /v1/editor/site/revisions` | the published revisions, newest (live) first |
| `POST /v1/editor/{kind}/{id}/revisions/{n}/restore` | the revision's project becomes the draft (not published) |

- **Sanitizing.** What the editor sends is untrusted: the HTML goes through an allow-list
  (`app/render`): no `<script>`, `<style>`, `<object>`, forms' inputs; no `on*` attributes; URLs
  only `http(s)`, `mailto`, `tel`, relative, and `data:image/…` (not SVG) for images; `<iframe>`
  only over https from YouTube, Vimeo, Google Maps, Spotify; `target="_blank"` links get
  `rel="noopener noreferrer"`; `data-*`, `aria-*`, `class`, `style` (cleaned) stay. The CSS loses
  `expression()`, `behavior`, `javascript:` URLs, `@import` (except font hosts) and anything that
  would close a `<style>`. Site settings widen it: `"custom_code": true` keeps scripts,
  `"iframe_hosts": [...]` adds hosts.
- **Layouts** need `<page-slot></page-slot>` (grapesjs-pages' slot) or `<luna-slot></luna-slot>` (or an
  element with `data-page-slot` / `data-luna-slot`) where the page goes. **Templates** answer with warnings for fields without an element.
- **Entries** publish their draft values, checked against the template's fields (required,
  types, select options, list items; `422` names the key, e.g. `field.specs.0.name`); the
  template must be published first. An entry always renders with its template's live revision.
- **Revisions**: every publish adds one; the newest 20 are kept per record.

## The read API

`pages:read`, which browser keys may hold (from the key's origins): published content only.

| Request | |
|---|---|
| `GET /v1/render?slug=/products/coffee-grinder` (or `?id=`) | `{html, css, title, seo, meta, version, …}`: the page in its layout, an entry filled with its values, CSS in order (site, layout, page or template) |
| `GET /v1/pages?template=product&parent=&page=&per_page=&sort=` | published pages with `fields`, `seo`, `meta` (catalogues); sort by `published_at`, `title`, `slug`, `position`, `-` for descending |
| `GET /v1/pages/{id}` | one published page's header and values, no HTML |
| `GET /v1/site` | name, settings, global CSS, menu (published pages with `in_menu: true`) |

- **Caching**: `/render` and `/site` answer with an ETag that changes with anything that can
  change the result (`304` on `If-None-Match`) and `Cache-Control: public, max-age=60`; rendered
  pages are also kept in memory until something is published.
- **Previews**: `?draft=1` on `/render` (needs `pages:write`, always allowed on the internal API)
  fills an entry with its draft values; designs are always the live ones.
- **Filling fields** (`render.Fill`): elements marked `data-field="<name>"` get the value by type:
  text escaped, rich text sanitized, `src`/`alt` for images, `href` and text for links, numbers by
  `format` (`0.00`, `#,##0`), dates by `YYYY`/`MM`/`DD`, a false boolean removes the element, a list
  repeats its first child (or its `data-field-item` child) per item. A value that is missing keeps
  the template's placeholder. The cases in `app/render/testdata/fill.json` are shared with
  grapesjs-storage's renderer.

## The content API

grapesjs-storage's REST contract (`RestContentStore`), under `/v1/content` with `pages:write`
and under `/internal/v1/apps/{app}/content`. It is a view of the same records: templates and
entries are the editor API's templates and pages.

| Request | |
|---|---|
| `GET /v1/content/templates`, `GET /v1/content/templates/{id}` | templates with their `fields` (and `data` for one) |
| `PUT /v1/content/templates/{id}` | saves `name`, `fields` and the project; does not bump the version, so grapesjs-pages gets no `412` |
| `GET /v1/content/entries?template=`, `GET /v1/content/entries/{id}` | entries with plain keys in `values` (`title`, not `field.title`) |
| `PUT /v1/content/entries/{id}` | `{template_id, name, slug?, values}` creates or replaces |
| `DELETE /v1/content/entries/{id}` | 204 |
| `POST /v1/content/entries/{id}/publish` | the same check and revision as the editor API's publish |

## Documentation

- **OpenAPI 3.1**: [`docs/openapi.yaml`](docs/openapi.yaml) at `GET /openapi.yaml`,
  [`docs/internal.yaml`](docs/internal.yaml) at `GET /internal/openapi.yaml`; Swagger UI at
  `/docs` and `/internal/docs` with `API_DOCS=true`. A test keeps both in step with the routes.
- **Postman**: [`docs/postman/luna-pages.postman_collection.json`](docs/postman/luna-pages.postman_collection.json)
  with [`docs/postman/luna-local.postman_environment.json`](docs/postman/luna-local.postman_environment.json);
  set `apiKey` to a server key with `pages:read` and `pages:write`.

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
