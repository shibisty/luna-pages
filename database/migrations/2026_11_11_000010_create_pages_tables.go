package migrations

import (
	"context"

	ormstore "migration-orm"
)

// The tables of luna-pages. Every row belongs to one application (app_id,
// the dashboard's application id). Layouts, templates and pages are keyed
// by (app_id, id) with ids the editor generates (UUIDs), so one
// application can never collide with or probe another's ids.
//
// Written in SQL: composite keys, partial unique indexes and
// ON DELETE SET NULL (column) are beyond the schema builder. PostgreSQL 15+.
const createPages = `
CREATE TABLE sites (
    app_id      uuid PRIMARY KEY,
    name        varchar(200) NOT NULL DEFAULT '',
    settings    jsonb NOT NULL DEFAULT '{}',
    version     bigint NOT NULL DEFAULT 1,
    created_at  timestamp NOT NULL DEFAULT (now() AT TIME ZONE 'UTC'),
    updated_at  timestamp NOT NULL DEFAULT (now() AT TIME ZONE 'UTC')
);

CREATE TABLE layouts (
    app_id      uuid NOT NULL,
    id          uuid NOT NULL,
    name        varchar(200) NOT NULL,
    version     bigint NOT NULL DEFAULT 1,
    created_at  timestamp NOT NULL DEFAULT (now() AT TIME ZONE 'UTC'),
    updated_at  timestamp NOT NULL DEFAULT (now() AT TIME ZONE 'UTC'),
    PRIMARY KEY (app_id, id)
);

CREATE TABLE templates (
    app_id      uuid NOT NULL,
    id          uuid NOT NULL,
    name        varchar(200) NOT NULL,
    fields      jsonb NOT NULL DEFAULT '[]',
    version     bigint NOT NULL DEFAULT 1,
    created_at  timestamp NOT NULL DEFAULT (now() AT TIME ZONE 'UTC'),
    updated_at  timestamp NOT NULL DEFAULT (now() AT TIME ZONE 'UTC'),
    PRIMARY KEY (app_id, id)
);

CREATE TABLE pages (
    app_id       uuid NOT NULL,
    id           uuid NOT NULL,
    slug         varchar(255) NOT NULL,
    title        varchar(300) NOT NULL DEFAULT '',
    kind         varchar(8) NOT NULL DEFAULT 'free' CHECK (kind IN ('free', 'entry')),
    layout_id    uuid,
    template_id  uuid,
    parent_id    uuid,
    position     integer NOT NULL DEFAULT 0,
    status       varchar(10) NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'archived')),
    published_at timestamp,
    version      bigint NOT NULL DEFAULT 1,
    created_at   timestamp NOT NULL DEFAULT (now() AT TIME ZONE 'UTC'),
    updated_at   timestamp NOT NULL DEFAULT (now() AT TIME ZONE 'UTC'),
    PRIMARY KEY (app_id, id),
    UNIQUE (app_id, slug),
    CHECK ((kind = 'entry') = (template_id IS NOT NULL)),
    FOREIGN KEY (app_id, layout_id) REFERENCES layouts (app_id, id) ON DELETE SET NULL (layout_id),
    FOREIGN KEY (app_id, template_id) REFERENCES templates (app_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (app_id, parent_id) REFERENCES pages (app_id, id) ON DELETE SET NULL (parent_id)
);
CREATE INDEX pages_template ON pages (app_id, template_id) WHERE template_id IS NOT NULL;

-- Every design: the GrapesJS project of a site, layout, template or page,
-- and, once published, its cleaned HTML and CSS. One draft row per owner,
-- changed in place; every publish adds a published row (the revisions).
CREATE TABLE content (
    id          bigserial PRIMARY KEY,
    app_id      uuid NOT NULL,
    owner_kind  varchar(10) NOT NULL CHECK (owner_kind IN ('site', 'layout', 'template', 'page')),
    owner_id    uuid NOT NULL,
    state       varchar(10) NOT NULL CHECK (state IN ('draft', 'published')),
    revision    integer NOT NULL DEFAULT 0,
    project     jsonb,
    html        text,
    css         text,
    created_by  varchar(100) NOT NULL DEFAULT '',
    created_at  timestamp NOT NULL DEFAULT (now() AT TIME ZONE 'UTC')
);
CREATE UNIQUE INDEX content_draft ON content (app_id, owner_kind, owner_id) WHERE state = 'draft';
CREATE UNIQUE INDEX content_revision ON content (app_id, owner_kind, owner_id, revision) WHERE state = 'published';

-- Every value about a page: field values (field.<name>), SEO (seo.<name>)
-- and the client's own keys; a draft and a published set.
CREATE TABLE metadata (
    id          bigserial PRIMARY KEY,
    app_id      uuid NOT NULL,
    page_id     uuid NOT NULL,
    state       varchar(10) NOT NULL CHECK (state IN ('draft', 'published')),
    key         varchar(128) NOT NULL,
    value       jsonb NOT NULL,
    updated_at  timestamp NOT NULL DEFAULT (now() AT TIME ZONE 'UTC'),
    UNIQUE (app_id, page_id, state, key),
    FOREIGN KEY (app_id, page_id) REFERENCES pages (app_id, id) ON DELETE CASCADE
);
`

const dropPages = `DROP TABLE IF EXISTS metadata, content, pages, templates, layouts, sites`

func init() {
	register(ormstore.Migration{
		Name: "2026_11_11_000010_create_pages_tables",
		Up: func(ctx context.Context, db *ormstore.DB) error {
			_, err := db.ExecContext(ctx, createPages)
			return err
		},
		Down: func(ctx context.Context, db *ormstore.DB) error {
			_, err := db.ExecContext(ctx, dropPages)
			return err
		},
	})
}
