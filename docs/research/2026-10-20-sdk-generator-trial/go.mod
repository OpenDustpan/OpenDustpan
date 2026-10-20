// This file only fences the trial code off from the server module, so that
// go build ./... and golangci-lint in the repository root skip it. The smoke
// scripts build each sample in a throwaway module outside the repository.
module opendustpan.invalid/sdk-generator-trial

go 1.27
