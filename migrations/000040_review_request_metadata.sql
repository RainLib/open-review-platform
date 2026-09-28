-- Provider review title and author are request-level metadata. They are
-- updated by every accepted webhook, while every run keeps its immutable
-- revision evidence separately. Empty values preserve compatibility with
-- legacy/CLI-created requests that do not have provider metadata.
ALTER TABLE review_requests
    ADD COLUMN title TEXT NOT NULL DEFAULT '',
    ADD COLUMN author TEXT NOT NULL DEFAULT '';

ALTER TABLE review_requests
    ADD CONSTRAINT review_requests_title_length_check CHECK (char_length(title) <= 512),
    ADD CONSTRAINT review_requests_author_length_check CHECK (char_length(author) <= 256);

CREATE INDEX review_requests_tenant_title_idx
    ON review_requests (tenant_id, title)
    WHERE title <> '';
