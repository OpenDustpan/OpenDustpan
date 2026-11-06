# Local setup for agent tools

Some agent settings hold an address that belongs to one account. Do not
commit such an address. This page says where to put it.

## Repowise MCP server

Repowise gives indexed documentation, symbols and history for the code. The
address of a Repowise workspace holds an ID of your account, so the
repository does not contain it.

1. Get the MCP address of your workspace from your Repowise account.
2. Make the file `opencode.local.jsonc` in the root of the repository. The
   file `.gitignore` covers it. Put this in it:

   ```jsonc
   {
     "mcp": {
       "repowise": {
         "type": "remote",
         "url": "<the MCP address of your workspace>",
         "enabled": true
       }
     }
   }
   ```

3. Start OpenCode with the variable `OPENCODE_CONFIG` set to the path of that
   file. OpenCode then merges it with `opencode.jsonc`.

For Claude Code, add the same address as an MCP server in your user
settings, for example with `claude mcp add --transport http repowise <address>`.
The settings in `.claude/settings.json` do not hold it.

Without Repowise, the agent workflow still works. Use `rg` and the Go tools
to find code.
