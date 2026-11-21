package migrations

import (
	"context"

	ormstore "migration-orm"
)

// grapesjs-site-pages 2.10+ and grapesjs-storage 1.x:
//
//   - components: linked components (contract 2.1), records like layouts;
//   - pages.data_of: a data record of grapesjs-storage, a page that takes
//     its design from another page (deleted with it);
//   - data_templates, data_entries: grapesjs-storage 1.x's content — a
//     template is a group of fields, an entry the data of a page (its id is
//     the page's) or a standalone entry; draft and published values;
//   - sites.languages: the site's languages ({default, enabled}).
//
// The 0.x model (templates with fields, pages with template_id and
// metadata field.<name>) stays as it is.
const storageComponents = `
CREATE TABLE components (
    app_id      uuid NOT NULL,
    id          uuid NOT NULL,
    name        varchar(200) NOT NULL,
    version     bigint NOT NULL DEFAULT 1,
    created_at  timestamp NOT NULL DEFAULT (now() AT TIME ZONE 'UTC'),
    updated_at  timestamp NOT NULL DEFAULT (now() AT TIME ZONE 'UTC'),
    PRIMARY KEY (app_id, id)
);

ALTER TABLE content DROP CONSTRAINT content_owner_kind_check;
ALTER TABLE content ADD CONSTRAINT content_owner_kind_check
    CHECK (owner_kind IN ('site', 'layout', 'template', 'page', 'component'));

ALTER TABLE pages ADD COLUMN data_of uuid;
ALTER TABLE pages ADD CONSTRAINT pages_data_of_fkey
    FOREIGN KEY (app_id, data_of) REFERENCES pages (app_id, id) ON DELETE CASCADE;
CREATE INDEX pages_data_of ON pages (app_id, data_of) WHERE data_of IS NOT NULL;

CREATE TABLE data_templates (
    app_id      uuid NOT NULL,
    id          varchar(64) NOT NULL,
    name        varchar(200) NOT NULL DEFAULT '',
    fields      jsonb NOT NULL DEFAULT '[]',
    data        jsonb,
    version     bigint NOT NULL DEFAULT 1,
    created_at  timestamp NOT NULL DEFAULT (now() AT TIME ZONE 'UTC'),
    updated_at  timestamp NOT NULL DEFAULT (now() AT TIME ZONE 'UTC'),
    PRIMARY KEY (app_id, id)
);

CREATE TABLE data_entries (
    app_id       uuid NOT NULL,
    id           varchar(64) NOT NULL,
    template_id  varchar(64) NOT NULL,
    name         varchar(200) NOT NULL DEFAULT '',
    slug         varchar(255) NOT NULL DEFAULT '',
    draft        jsonb NOT NULL DEFAULT '{}',
    published    jsonb,
    version      bigint NOT NULL DEFAULT 1,
    created_at   timestamp NOT NULL DEFAULT (now() AT TIME ZONE 'UTC'),
    updated_at   timestamp NOT NULL DEFAULT (now() AT TIME ZONE 'UTC'),
    published_at timestamp,
    PRIMARY KEY (app_id, id)
);
CREATE INDEX data_entries_template ON data_entries (app_id, template_id);

ALTER TABLE sites ADD COLUMN languages jsonb;
`

const dropStorageComponents = `
ALTER TABLE sites DROP COLUMN IF EXISTS languages;
DROP TABLE IF EXISTS data_entries, data_templates, components;
ALTER TABLE pages DROP COLUMN IF EXISTS data_of;
DELETE FROM content WHERE owner_kind = 'component';
ALTER TABLE content DROP CONSTRAINT content_owner_kind_check;
ALTER TABLE content ADD CONSTRAINT content_owner_kind_check CHECK (owner_kind IN ('site', 'layout', 'template', 'page'));
`

func init() {
	register(ormstore.Migration{
		Name: "2026_11_21_000010_storage_components",
		Up: func(ctx context.Context, db *ormstore.DB) error {
			_, err := db.ExecContext(ctx, storageComponents)
			return err
		},
		Down: func(ctx context.Context, db *ormstore.DB) error {
			_, err := db.ExecContext(ctx, dropStorageComponents)
			return err
		},
	})
}
