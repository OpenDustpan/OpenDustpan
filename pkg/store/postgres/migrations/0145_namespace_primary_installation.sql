-- A namespace names its primary installation, not a home region. The column
-- keeps its values: the installation name replaces the region name.
ALTER TABLE namespaces RENAME COLUMN home_region TO primary_installation;
