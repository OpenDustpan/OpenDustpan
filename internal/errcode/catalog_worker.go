package errcode

// WorkerAbsent means that no worker claimed or renewed a claim for 2 lease periods of the worker, so a request
// that starts a durable operation waits for a worker that does not run.
const WorkerAbsent = "worker.absent"

// workerSpecs are the codes of the workers of the installation.
func workerSpecs() []Spec {
	return []Spec{
		{
			Code: WorkerAbsent,
			Meaning: "no worker claimed or renewed a claim for 2 lease periods, " +
				"so a request that starts a durable operation is refused",
			Args: nil,
		},
	}
}
