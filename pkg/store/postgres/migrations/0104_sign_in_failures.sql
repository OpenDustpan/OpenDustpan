CREATE TABLE sign_in_failures (
    source TEXT NOT NULL,
    window_end_ms BIGINT NOT NULL,
    count BIGINT NOT NULL,
    PRIMARY KEY (source, window_end_ms)
);

CREATE INDEX sign_in_failures_window_end ON sign_in_failures USING btree (window_end_ms);
