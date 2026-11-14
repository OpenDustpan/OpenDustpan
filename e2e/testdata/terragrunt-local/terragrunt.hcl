# Root Terragrunt configuration for the local run-all fixture.
#
# Each unit directory holds its own backend, which names its own workspace on
# the server. The run applies every unit in its own workspace, with no change
# order. The test copies this file to the live directory as root.hcl, sets
# the backend of each unit, then runs `terragrunt run --all apply`. The root
# keeps the root.hcl name on purpose: a root named terragrunt.hcl would run
# as a unit of its own. It holds no remote_state block on purpose: each unit
# names its own backend in its own OpenTofu configuration, and Terragrunt
# refuses a run whose Terragrunt configuration names remote state that the
# OpenTofu configuration does not use.
