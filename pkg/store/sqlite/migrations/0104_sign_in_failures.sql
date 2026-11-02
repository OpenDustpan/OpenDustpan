CREATE TABLE sign_in_failures (
    source TEXT NOT NULL,
    window_end_ms INTEGER NOT NULL,
    count INTEGER NOT NULL,
    PRIMARY KEY (source, window_end_ms)
) STRICT;

CREATE INDEX sign_in_failures_window_end ON sign_in_failures (window_end_ms);
