package audit

// LegacyAgentChangeKey is the change key under which the job service wrote
// the agent of a start entry before the agent block existed. New entries
// never carry it. Old entries keep it until their retention ends, and their
// hash covers it as a change, so a read accepts it.
const LegacyAgentChangeKey = "agent"

// AgentOrigin answers the agent that acted for the subject of e. The agent
// block wins: when e holds an Origin, AgentOrigin answers it and ignores a
// legacy change key. Where e holds no block and carries the legacy change
// key, AgentOrigin answers an origin with the legacy value as the run and
// the session, which is how the block of a start of an agent holds the
// reference. It answers nil for an entry with no agent data, and for a
// legacy value that an origin cannot hold, so that a hostile row never
// reaches a reader as an agent. It changes nothing in e and no hash: the
// hash covers the stored block and the stored changes as they are.
func (e Entry) AgentOrigin() *Origin {
	if e.Origin != nil {
		return e.Origin
	}

	for i := range e.Changes {
		if e.Changes[i].Key != LegacyAgentChangeKey || e.Changes[i].New == "" {
			continue
		}

		origin, err := NewOrigin(OriginArgs{
			Run: e.Changes[i].New, Session: e.Changes[i].New, Tool: "", ArgsDigest: "", BreakGlass: false,
			SecretStage: "", DelegationID: "",
		})
		if err != nil {
			return nil
		}

		return origin
	}

	return nil
}
