# Root Terragrunt configuration for the local run-all fixture.
#
# Each unit directory holds its own backend, which names its own workspace on
# the server. The run applies every unit in its own workspace, with no change
# order. Copy this directory to a live directory, set the backend of each
# unit, then run `terragrunt run-all apply`.

remote_state {
  backend = "remote"

  config = {
    hostname     = "dustpan.example.com"
    organization = "acme"
  }
}
