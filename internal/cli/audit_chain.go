package cli

// AuditChainStatusCommand answers the audit-chain status row of the command
// table with run: a client of GET /api/server/audit/chain.
func AuditChainStatusCommand(run RunFunc) Command {
	return Command{
		Path:     []string{commandAuditChain, commandStatus},
		Synopsis: "Show the state of the audit chain and whether a change waits.",
		Settings: SettingsFor("server", "ca-file"),
		Run:      run,
	}
}

// AuditChainApproveCommand answers the audit-chain approve row of the
// command table with run: a client of POST /api/server/audit/chain.
func AuditChainApproveCommand(run RunFunc) Command {
	return Command{
		Path:     []string{commandAuditChain, commandApprove},
		Synopsis: "Approve the change of the audit chain to the setting of the server.",
		Settings: SettingsFor("server", "ca-file"),
		Run:      run,
	}
}
