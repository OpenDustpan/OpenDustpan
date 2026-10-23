-- The output sharing setting of each namespace: the sharing rule that a new
-- unit of the namespace gets. nobody gives a new unit no rule, and namespace
-- gives it one rule that names the namespace of the unit. A namespace that
-- never set it reads nobody. The store checks the value at each write and at
-- each read, and the CHECK refuses a row that holds another value.
ALTER TABLE namespaces ADD COLUMN output_sharing_default text NOT NULL DEFAULT 'nobody'
    CONSTRAINT namespaces_output_sharing_default_check
    CHECK (output_sharing_default IN ('nobody', 'namespace'));
