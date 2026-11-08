package cli

// AuditChainStatusCommand answers the audit-chain status row of the command
// table with run: a client of GET /api/dustpan/v1/audit/chain.
func AuditChainStatusCommand(run RunFunc) Command {
	return Command{
		Path:     []string{commandAuditChain, commandStatus},
		Synopsis: "Show the state of the audit chain and whether a change waits.",
		Settings: SettingsFor("server", "ca-file"),
		Run:      run,
	}
}

// AuditChainApproveCommand answers the audit-chain approve row of the
// command table with run. The command sends no request: the approval needs a
// console session with a recent sign-in.
func AuditChainApproveCommand(run RunFunc) Command {
	return Command{
		Path:     []string{commandAuditChain, commandApprove},
		Synopsis: "Show where to approve the change of the audit chain: the console.",
		Settings: SettingsFor(),
		Run:      run,
	}
}
