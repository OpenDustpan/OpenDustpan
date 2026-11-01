-- The session generation that each console session carries. The issue of a
-- session stores the generation of its principal that the sign-in read, and
-- the lookup of a session compares it with the stored generation of
-- principal_session_generations. A session whose generation is not the
-- stored one does not authenticate, so a password set, change or reset ends
-- each session of the person. A session from before this column has the
-- generation 0, which no principal has, so it ends at its next request.
ALTER TABLE sessions ADD COLUMN generation bigint NOT NULL DEFAULT 0 CHECK (generation >= 0);
