package errcode

// JobLogNotAvailable means that the phase of the job has no log to read yet.
const JobLogNotAvailable = "job.log-not-available"

// jobLogSpecs are the codes of the read of a job log.
func jobLogSpecs() []Spec {
	return []Spec{
		{Code: JobLogNotAvailable, Meaning: "the phase of the job has no log to read yet", Args: nil},
	}
}
