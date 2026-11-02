-- Preferences of a person: the settings of the console that follow the person
-- to every device. One row holds the settings that a person changed, as one
-- JSON object of strings with the flat field names as keys. A field that the
-- object does not hold has its default, so a person that never changed a
-- preference has no row. A row belongs to a person, not to a namespace, so a
-- write needs no fence. The row goes away with its person. updated_at_ms is
-- the time of the last change in Unix milliseconds.
CREATE TABLE person_preferences (
    principal_id  text   NOT NULL PRIMARY KEY REFERENCES principals (id),
    document      text   NOT NULL CHECK (char_length(document) <= 4096 AND jsonb_typeof(document::jsonb) = 'object'),
    updated_at_ms bigint NOT NULL
);
